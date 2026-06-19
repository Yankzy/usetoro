package vcoo

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// LLMTextGenerator represents the interface to generate text using an LLM.
type LLMTextGenerator interface {
	GenerateText(ctx context.Context, systemPrompt, userPrompt string) (string, error)
}

// GenerateDailyBrief generates the plain-text Daily Command Brief using LLM or template fallback.
func GenerateDailyBrief(ctx context.Context, llm LLMTextGenerator, founderEmail string, velocityDelta float64, restrictionActive bool, multiplier float64, activeBlockers []string, tasks []Task, targets []Target) (string, error) {
	// 1. Calculate daily distribution metrics
	var outboundDials float64 = 50
	var onboardingSessions float64 = 1
	var videoProofs float64 = 2
	var xPosts float64 = 5

	for _, t := range targets {
		switch t.ID {
		case "OUTBOUND_CALLS":
			outboundDials = math.Round((t.Metric / 5.0) * multiplier)
		case "ONBOARDING_RUNS":
			onboardingSessions = math.Round((t.Metric / 5.0) * multiplier)
		case "VIDEO_PROOFS":
			videoProofs = math.Round((t.Metric / 5.0) * multiplier)
		case "X_DAILY_POSTS":
			xPosts = math.Round((t.Metric / 5.0) * multiplier)
		}
	}

	blockerText := "No active blockers identified."
	if len(activeBlockers) > 0 {
		blockerText = strings.Join(activeBlockers, ", ")
	}

	activeMilestones := make([]string, 0)
	for _, tk := range tasks {
		if tk.Status == "ACTIVE" {
			activeMilestones = append(activeMilestones, tk.ID)
		}
	}
	milestoneText := "Standard Sprint Pathway"
	if len(activeMilestones) > 0 {
		milestoneText = strings.Join(activeMilestones, ", ")
	}

	statusStr := "TARGET NORMAL"
	restrictionStr := "INACTIVE. Standard tracking parameters applied."
	if velocityDelta < 0 {
		statusStr = "TARGET REGRESSION DETECTED"
		restrictionStr = fmt.Sprintf("ACTIVE. Multiplier %.1fx applied to outbound distribution pipelines.", multiplier)
	}

	// If LLM is provided, attempt to generate a premium brief
	if llm != nil {
		systemPrompt := `You are the Virtual COO. You format nightly progress updates into Daily Command Briefs.
Rules:
1. Avoid conversational padding (no "Here is your brief", "Good morning", "Hope you're well").
2. Follow the output format exactly.
3. Generate natural-sounding engineering sprint directives based on the blockers.
4. Scale metrics exactly as provided in the user prompt.`

		userPrompt := fmt.Sprintf(`To: %s
From: coo@inbound.yourplatform.com
Subject: [COMMAND BRIEF] SYSTEM DIRECTIVE - VELOCITY TRACKING %s

Velocity Delta: %.2f (%s)
Restriction Protocol: %s
Active Blockers: %s
Active Milestones: %s
Today's Targets:
- Outbound VoIP Dials: %.0f calls [REVENUE ACQUISITION ROUTING]
- White-Glove Onboarding Sessions: %.0f active calls scheduled
- Community Audio Lounge Drops: %.0f deep-dive value insertion
- X Posts Target: %.0f posts

Please render the final plain text command brief.`,
			founderEmail,
			ternary(restrictionActive, "ACTIVE", "NORMAL"),
			velocityDelta, statusStr,
			restrictionStr,
			blockerText,
			milestoneText,
			outboundDials,
			onboardingSessions,
			videoProofs,
			xPosts,
		)

		brief, err := llm.GenerateText(ctx, systemPrompt, userPrompt)
		if err == nil && len(strings.TrimSpace(brief)) > 0 {
			return brief, nil
		}
	}

	// Fallback deterministic template
	return FormatDeterministicBrief(founderEmail, velocityDelta, restrictionActive, multiplier, blockerText, outboundDials, onboardingSessions, videoProofs, xPosts, milestoneText, statusStr, restrictionStr), nil
}

func FormatDeterministicBrief(founderEmail string, velocityDelta float64, restrictionActive bool, multiplier float64, blockerText string, outboundDials, onboardingSessions, videoProofs, xPosts float64, milestoneText, statusStr, restrictionStr string) string {
	subjectStatus := "NORMAL"
	if restrictionActive {
		subjectStatus = "ACTIVE"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("To: %s\n", founderEmail))
	sb.WriteString("From: coo@inbound.yourplatform.com\n")
	sb.WriteString(fmt.Sprintf("Subject: [COMMAND BRIEF] SYSTEM DIRECTIVE - VELOCITY TRACKING %s\n\n", subjectStatus))
	sb.WriteString("---\n[SYSTEM OVERVIEW STATUS]\n")
	sb.WriteString(fmt.Sprintf("Current Weekly Velocity Delta: %.2f [STATUS: %s]\n", velocityDelta, statusStr))
	sb.WriteString(fmt.Sprintf("Restriction Protocol: %s\n", restrictionStr))
	sb.WriteString("---\n\n")

	sb.WriteString("### SECTION 1: CORE SPRINT PATHWAY (09:00 - 13:00)\n")
	if blockerText != "No active blockers identified." {
		sb.WriteString(fmt.Sprintf("[BLOCKER IDENTIFIED]: %s.\n", blockerText))
		sb.WriteString(fmt.Sprintf("[DIRECTIVE]: Execute 3 engineering sprints to refactor the %s. Do not compile secondary modules until this block is cleared.\n\n", blockerText))
	} else {
		sb.WriteString(fmt.Sprintf("[ACTIVE MILESTONE]: %s.\n", milestoneText))
		sb.WriteString("[DIRECTIVE]: Progress milestones on schedule. Keep sandbox constraints minimized.\n\n")
	}

	sb.WriteString("### SECTION 2: 10X DISTRIBUTION MATRIX (14:00 - 18:00)\n")
	sb.WriteString("[TARGET METRICS FOR TODAY]:\n")
	sb.WriteString(fmt.Sprintf("- Outbound VoIP Dials: %.0f calls [REVENUE ACQUISITION ROUTING]\n", outboundDials))
	sb.WriteString(fmt.Sprintf("- White-Glove Onboarding Sessions: %.0f active calls scheduled\n", onboardingSessions))
	sb.WriteString(fmt.Sprintf("- Community Audio Lounge Drops: %.0f deep-dive value insertion\n\n", videoProofs))

	sb.WriteString("### SECTION 3: SYSTEM CONTEXT INFILTRATION\n")
	sb.WriteString(fmt.Sprintf("- Deploy exactly %.0f screen-only visual terminal capture sequences directly onto X feed.\n", xPosts))
	if milestoneText != "Standard Sprint Pathway" {
		sb.WriteString(fmt.Sprintf("- Highlight the exact %s schemas verified in TECH-SWEEP-PRD-V1.\n", milestoneText))
	} else {
		sb.WriteString("- Highlight the exact multi-tenant AlloyDB indexing schemas verified in TECH-SWEEP-PRD-V1.\n")
	}
	sb.WriteString("\n---\n[SYSTEM MONITORING ACTIVE]\n")
	sb.WriteString("Send your nightly execution payload to this address before 23:00 to populate tomorrow's system matrix.\n")

	return sb.String()
}

func ternary(cond bool, t, f string) string {
	if cond {
		return t
	}
	return f
}
