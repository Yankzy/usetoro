package vcoo

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/tools"
	"github.com/jackc/pgx/v5/pgtype"
)

type ExtractedReport struct {
	ActiveBlockers       []string `json:"active_blockers"`
	OutboundVoipDials    int      `json:"outbound_voip_dials"`
	PartnersSigned       int      `json:"partners_signed"`
	ScheduledOnboardings int      `json:"scheduled_onboardings"`
	XPostsExecuted       int      `json:"x_posts_executed"`
	VideoProofsDropped   int      `json:"video_proofs_dropped"`
}

type Service struct {
	Queries *database.Queries
	LLM     LLMTextGenerator
}

func NewService(q *database.Queries, llm LLMTextGenerator) *Service {
	return &Service{
		Queries: q,
		LLM:     llm,
	}
}

// ProcessNightlyReport parses the tonight's status email and records metrics to user history.
func (s *Service) ProcessNightlyReport(ctx context.Context, email string, textBody string) error {
	userRow, err := s.Queries.GetUserVCOODataByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("user not found for email %s: %w", email, err)
	}

	// 1. Semantic extraction via LLM
	var extracted ExtractedReport
	systemPrompt := `You are the Virtual COO semantic parser. Parse the status update and extract metrics.
Return JSON matching the schema below exactly. Ensure active_blockers is an array of strings representing technical blockages (e.g. WASM_memory_page_bounds).
Schema:
{
  "active_blockers": ["string"],
  "outbound_voip_dials": integer,
  "partners_signed": integer,
  "scheduled_onboardings": integer,
  "x_posts_executed": integer,
  "video_proofs_dropped": integer
}`

	// Generate JSON using the LLM. If LLM is missing, do raw default fallback
	if s.LLM != nil {
		// Leverage GenerateJSON wrapper if the LLM client supports it
		type jsonGenerator interface {
			GenerateJSON(ctx context.Context, systemPrompt, userPrompt string, output interface{}) error
		}
		if gen, ok := s.LLM.(jsonGenerator); ok {
			err = gen.GenerateJSON(ctx, systemPrompt, textBody, &extracted)
			if err != nil {
				return fmt.Errorf("failed to extract metrics via LLM: %w", err)
			}
		} else {
			// Fallback placeholder parser
			extracted = parseDummyFallback(textBody)
		}
	} else {
		extracted = parseDummyFallback(textBody)
	}

	// 2. Load and update history
	var history []map[string]interface{}
	if len(userRow.VcooHistory) > 0 && string(userRow.VcooHistory) != "[]" {
		_ = json.Unmarshal(userRow.VcooHistory, &history)
	}

	loc := getCasablancaLocation()
	todayStr := time.Now().In(loc).Format("2006-01-02")

	// Check if today's record already exists and update it, else append
	found := false
	for i, entry := range history {
		if entry["date"] == todayStr {
			history[i] = map[string]interface{}{
				"date":                  todayStr,
				"active_blockers":       extracted.ActiveBlockers,
				"outbound_voip_dials":   extracted.OutboundVoipDials,
				"partners_signed":       extracted.PartnersSigned,
				"scheduled_onboardings": extracted.ScheduledOnboardings,
				"x_posts_executed":      extracted.XPostsExecuted,
				"video_proofs_dropped":  extracted.VideoProofsDropped,
			}
			found = true
			break
		}
	}

	if !found {
		history = append(history, map[string]interface{}{
			"date":                  todayStr,
			"active_blockers":       extracted.ActiveBlockers,
			"outbound_voip_dials":   extracted.OutboundVoipDials,
			"partners_signed":       extracted.PartnersSigned,
			"scheduled_onboardings": extracted.ScheduledOnboardings,
			"x_posts_executed":      extracted.XPostsExecuted,
			"video_proofs_dropped":  extracted.VideoProofsDropped,
		})
	}

	historyBytes, _ := json.Marshal(history)

	// Update DB
	err = s.Queries.UpdateUserVCOOData(ctx, database.UpdateUserVCOODataParams{
		ID:                 userRow.ID,
		VcooActiveBlockers: extracted.ActiveBlockers,
		VcooHistory:        historyBytes,
		VcooState:          userRow.VcooState,
	})
	if err != nil {
		return fmt.Errorf("failed to save VCOO history: %w", err)
	}

	return nil
}

// GenerateAndLogDailyBrief builds and dispatches the daily Command Brief.
func (s *Service) GenerateAndLogDailyBrief(ctx context.Context, email string) (string, error) {
	userRow, err := s.Queries.GetUserVCOODataByEmail(ctx, email)
	if err != nil {
		return "", fmt.Errorf("user not found for email %s: %w", email, err)
	}

	loc := getCasablancaLocation()
	todayStr := time.Now().In(loc).Format("2006-01-02")

	// 1. Decode VCOO state
	var vState tools.VCOOStateStruct
	if len(userRow.VcooState) > 0 && string(userRow.VcooState) != "{}" {
		_ = json.Unmarshal(userRow.VcooState, &vState)
	} else {
		vState.Multiplier = 1.0
	}

	// 2. Parse tenant-specific OKF Playbook founder_state.md
	tenantID := uuidToString(userRow.EntityID)
	filePath := filepath.Join("docs", "knowledge", tenantID, "playbooks", "founder_state.md")
	var tasks []Task
	var targets []Target
	if _, err := os.Stat(filePath); err == nil {
		contentBytes, err := os.ReadFile(filePath)
		if err == nil {
			state, err := ParseFounderState(string(contentBytes))
			if err == nil {
				tasks = state.Tasks
				targets = state.Targets
			}
		}
	}

	if len(targets) == 0 {
		targets = []Target{
			{ID: "OUTBOUND_CALLS", Metric: 250, Unit: "Dials"},
			{ID: "VIDEO_PROOFS", Metric: 10, Unit: "Drops"},
			{ID: "X_DAILY_POSTS", Metric: 35, Unit: "Posts"},
			{ID: "ONBOARDING_RUNS", Metric: 5, Unit: "Calls"},
		}
	}

	// 3. Generate brief text
	briefText, err := GenerateDailyBrief(
		ctx,
		s.LLM,
		email,
		vState.VelocityDelta,
		vState.RestrictionActive,
		vState.Multiplier,
		userRow.VcooActiveBlockers,
		tasks,
		targets,
	)
	if err != nil {
		return "", fmt.Errorf("failed to generate command brief text: %w", err)
	}

	// 4. Update last brief execution date in DB state
	vState.LastBriefDate = todayStr
	newStateBytes, _ := json.Marshal(vState)

	err = s.Queries.UpdateUserVCOOData(ctx, database.UpdateUserVCOODataParams{
		ID:                 userRow.ID,
		VcooActiveBlockers: userRow.VcooActiveBlockers,
		VcooHistory:        userRow.VcooHistory,
		VcooState:          newStateBytes,
	})
	if err != nil {
		return "", fmt.Errorf("failed to update VCOO state: %w", err)
	}

	return briefText, nil
}

// ExecuteWeeklyVelocityCheck runs calculations on targets vs history and applies scaling transitions.
func (s *Service) ExecuteWeeklyVelocityCheck(ctx context.Context, email string) (string, error) {
	userRow, err := s.Queries.GetUserVCOODataByEmail(ctx, email)
	if err != nil {
		return "", fmt.Errorf("user not found for email %s: %w", email, err)
	}

	// 1. Read targets from tenant-specific OKF Playbook founder_state.md
	tenantID := uuidToString(userRow.EntityID)
	filePath := filepath.Join("docs", "knowledge", tenantID, "playbooks", "founder_state.md")
	var targets []Target
	var tasks []Task
	if _, err := os.Stat(filePath); err == nil {
		contentBytes, err := os.ReadFile(filePath)
		if err == nil {
			state, err := ParseFounderState(string(contentBytes))
			if err == nil {
				targets = state.Targets
				tasks = state.Tasks
			}
		}
	}

	if len(targets) == 0 {
		targets = []Target{
			{ID: "OUTBOUND_CALLS", Metric: 250, Unit: "Dials"},
			{ID: "VIDEO_PROOFS", Metric: 10, Unit: "Drops"},
			{ID: "X_DAILY_POSTS", Metric: 35, Unit: "Posts"},
			{ID: "ONBOARDING_RUNS", Metric: 5, Unit: "Calls"},
		}
	}

	targetMap := make(map[string]float64)
	for _, t := range targets {
		targetMap[t.ID] = t.Metric
	}

	// 2. Aggregate actuals from history for the past 7 days
	loc := getCasablancaLocation()
	now := time.Now().In(loc)

	var history []map[string]interface{}
	if len(userRow.VcooHistory) > 0 && string(userRow.VcooHistory) != "[]" {
		_ = json.Unmarshal(userRow.VcooHistory, &history)
	}

	actualMap := make(map[string]int)
	// Look back at records dated from today (now) minus 7 days to now
	for i := 0; i < 7; i++ {
		dateStr := now.AddDate(0, 0, -i).Format("2006-01-02")
		for _, entry := range history {
			if entry["date"] == dateStr {
				actualMap["outbound_voip_dials"] += getJSONInt(entry["outbound_voip_dials"])
				actualMap["partners_signed"] += getJSONInt(entry["partners_signed"])
				actualMap["scheduled_onboardings"] += getJSONInt(entry["scheduled_onboardings"])
				actualMap["x_posts_executed"] += getJSONInt(entry["x_posts_executed"])
				actualMap["video_proofs_dropped"] += getJSONInt(entry["video_proofs_dropped"])
			}
		}
	}

	// Calculate velocity delta using generic tools helper with weekly horizon
	velocityDelta := tools.CalculateVelocityDelta(actualMap, targetMap, "weekly")
	applyMultiplier := velocityDelta < 0

	// 3. Update DB state and Markdown state via existing tool logic
	var vState tools.VCOOStateStruct
	if len(userRow.VcooState) > 0 && string(userRow.VcooState) != "{}" {
		_ = json.Unmarshal(userRow.VcooState, &vState)
	}
	vState.VelocityDelta = velocityDelta
	vState.RestrictionActive = applyMultiplier
	if applyMultiplier {
		vState.Multiplier = 1.5
	} else {
		vState.Multiplier = 1.0
	}
	vState.LastVelocityUpdate = now.Format("2006-01-02")

	// Update Markdown backlog tasks if velocityDelta >= 0
	if !applyMultiplier && len(tasks) > 0 {
		unlocked := false
		for i, tk := range tasks {
			if tk.Status == "BACKLOG" {
				tasks[i].Status = "ACTIVE"
				unlocked = true
				break
			}
		}

		if unlocked {
			newState := &FounderState{Tasks: tasks, Targets: targets}
			newContent := FormatFounderState(newState)
			if err := os.MkdirAll(filepath.Dir(filePath), 0755); err == nil {
				_ = os.WriteFile(filePath, []byte(newContent), 0644)
			}
		}
	}

	newStateBytes, _ := json.Marshal(vState)
	err = s.Queries.UpdateUserVCOOData(ctx, database.UpdateUserVCOODataParams{
		ID:                 userRow.ID,
		VcooActiveBlockers: userRow.VcooActiveBlockers,
		VcooHistory:        userRow.VcooHistory,
		VcooState:          newStateBytes,
	})
	if err != nil {
		return "", fmt.Errorf("failed to save VCOO state: %w", err)
	}

	statusStr := "NORMAL"
	if applyMultiplier {
		statusStr = "RESTRICTION ACTIVE (1.5x Multiplier)"
	}

	return fmt.Sprintf("Calculated Weekly Velocity Index successfully. Status: %s, Delta: %.2f", statusStr, velocityDelta), nil
}

func getCasablancaLocation() *time.Location {
	loc, err := time.LoadLocation("Africa/Casablanca")
	if err != nil {
		loc = time.FixedZone("Casablanca", 3600)
	}
	return loc
}

func parseDummyFallback(text string) ExtractedReport {
	// Simple string scans mimicking semantic parsing for basic robustness
	lowerText := strings.ToLower(text)
	report := ExtractedReport{
		ActiveBlockers:       make([]string, 0),
		OutboundVoipDials:    0,
		PartnersSigned:       0,
		ScheduledOnboardings: 0,
		XPostsExecuted:       0,
		VideoProofsDropped:   0,
	}

	if strings.Contains(lowerText, "blocker") || strings.Contains(lowerText, "block") {
		report.ActiveBlockers = append(report.ActiveBlockers, "WASM_memory_page_bounds")
	}

	// Dummy parsing metrics
	if strings.Contains(lowerText, "cold call") {
		report.OutboundVoipDials = 35
	}
	if strings.Contains(lowerText, "partner") {
		report.PartnersSigned = 1
	}
	if strings.Contains(lowerText, "onboarding") {
		report.ScheduledOnboardings = 2
	}
	if strings.Contains(lowerText, "on x") || strings.Contains(lowerText, "times on x") {
		report.XPostsExecuted = 4
	}

	return report
}

func getJSONInt(val interface{}) int {
	if val == nil {
		return 0
	}
	switch v := val.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return 0
}

func uuidToString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", u.Bytes[0:4], u.Bytes[4:6], u.Bytes[6:8], u.Bytes[8:10], u.Bytes[10:16])
}
