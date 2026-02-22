package quickbooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgtype"
)

// ============================================================================
//  1. Enums & Constants
// ============================================================================

type Field string

const (
	FieldDescription Field = "description"
	FieldVendor      Field = "vendor"
	FieldCategory    Field = "category"
	FieldAmount      Field = "amount"
	FieldDate        Field = "date"
	FieldMemo        Field = "memo"
)

type Operator string

const (
	// String Operators (Case-Insensitive)
	OpEquals     Operator = "equals"
	OpContains   Operator = "contains"
	OpStartsWith Operator = "startswith"
	OpEndsWith   Operator = "endswith"
	OpIn         Operator = "in"
	OpNotIn      Operator = "not_in"

	// String Operators (Case-Sensitive)
	OpEqualsCS   Operator = "equals_cs"
	OpContainsCS Operator = "contains_cs"

	// Existence
	OpIsNull    Operator = "is_null"
	OpIsNotNull Operator = "is_not_null"

	// Numeric/Date
	OpGt    Operator = "gt"
	OpGte   Operator = "gte"
	OpLt    Operator = "lt"
	OpLte   Operator = "lte"
	OpRegex Operator = "regex"
)

type LogicChoice string

const (
	LogicAnd LogicChoice = "AND"
	LogicOr  LogicChoice = "OR"
)

// ============================================================================
//  2. Structs
// ============================================================================

type Transaction struct {
	ID          string
	EntityID    string
	Vendor      string
	Description string
	Amount      float64
	Category    string
	Date        time.Time
	Memo        string
}

type RuleCondition struct {
	ID       int
	Field    Field
	Operator Operator
	Value    string

	// Internal compiled state
	compiledRegex   *regexp.Regexp
	compiledSet     map[string]struct{}
	compiledNumeric float64
	compiledDate    time.Time
	isCompiled      bool
}

type RuleGroup struct {
	ID       int
	Name     string
	Logic    LogicChoice
	Priority int
	Keywords string // Auto-generated for optimization
	Active   bool

	Conditions []*RuleCondition
	Children   []*RuleGroup
	Parent     *RuleGroup // Needed for circular dependency checks
}

// --- Audit / Explanation Structs ---

type MatchExplanation struct {
	GroupID      int                `json:"group_id"`
	GroupName    string             `json:"group_name"`
	Logic        string             `json:"logic"`
	FinalResult  bool               `json:"final_result"`
	ConditionExp []ConditionResult  `json:"conditions"`
	ChildrenExp  []MatchExplanation `json:"children,omitempty"`
}

type ConditionResult struct {
	ConditionID int    `json:"condition_id"`
	Field       string `json:"field"`
	Operator    string `json:"operator"`
	TargetValue string `json:"target_value"`
	TxValue     string `json:"tx_value"`
	Result      bool   `json:"result"`
}

// HumanReadableReason translates the MatchExplanation JSON trace into a human-readable string.
func (m *MatchExplanation) HumanReadableReason() string {
	if !m.FinalResult {
		return ""
	}

	var reasons []string

	// Format individual conditions
	for _, c := range m.ConditionExp {
		if c.Result {
			reasons = append(reasons, formatConditionReason(c))
		}
	}

	// Format child groups recursively
	for _, child := range m.ChildrenExp {
		if child.FinalResult {
			// Extract just the "Because X or Y" part from the child
			childReason := child.HumanReadableReason()
			if strings.HasPrefix(childReason, "Categorized by Rule:") {
				parts := strings.SplitN(childReason, "Because ", 2)
				if len(parts) == 2 {
					reasons = append(reasons, parts[1])
				}
			}
		}
	}

	if len(reasons) == 0 {
		return fmt.Sprintf("Categorized by Rule: %s.", m.GroupName)
	}

	joinWord := " and "
	if strings.ToUpper(m.Logic) == "OR" {
		joinWord = " or "
	}

	return fmt.Sprintf("Categorized by Rule: %s. Because %s.", m.GroupName, strings.Join(reasons, joinWord))
}

func formatConditionReason(c ConditionResult) string {
	field := c.Field
	val := c.TargetValue
	if c.Field == "amount" {
		val = "$" + val
	}

	switch c.Operator {
	case string(OpEquals), string(OpEqualsCS):
		return fmt.Sprintf("the %s was exactly '%s'", field, val)
	case string(OpContains), string(OpContainsCS):
		return fmt.Sprintf("the %s contained '%s'", field, val)
	case string(OpStartsWith):
		return fmt.Sprintf("the %s started with '%s'", field, val)
	case string(OpEndsWith):
		return fmt.Sprintf("the %s ended with '%s'", field, val)
	case string(OpGt):
		return fmt.Sprintf("the %s was greater than %s", field, val)
	case string(OpGte):
		return fmt.Sprintf("the %s was greater than or equal to %s", field, val)
	case string(OpLt):
		return fmt.Sprintf("the %s was less than %s", field, val)
	case string(OpLte):
		return fmt.Sprintf("the %s was less than or equal to %s", field, val)
	case string(OpIsNull):
		return fmt.Sprintf("the %s was empty", field)
	case string(OpIsNotNull):
		return fmt.Sprintf("the %s was not empty", field)
	case string(OpIn):
		return fmt.Sprintf("the %s was one of %s", field, val)
	case string(OpNotIn):
		return fmt.Sprintf("the %s was not one of %s", field, val)
	case string(OpRegex):
		return fmt.Sprintf("the %s matched the pattern '%s'", field, val)
	default:
		return fmt.Sprintf("the %s matched '%s'", field, val)
	}
}

// ============================================================================
//  3. Management Logic (Validation & Auto-Keywords)
// ============================================================================

// Validate checks for circular dependencies and malformed values.
func (g *RuleGroup) Validate() error {
	// 1. Check Circular Dependency
	if g.Parent != nil {
		node := g.Parent
		for node != nil {
			if node.ID == g.ID && g.ID != 0 {
				return errors.New("circular dependency detected")
			}
			node = node.Parent
		}
	}

	// 2. Validate Conditions
	for _, c := range g.Conditions {
		// Attempt to compile to catch invalid regex/numbers
		if err := c.compile(); err != nil {
			return fmt.Errorf("condition %d invalid: %v", c.ID, err)
		}
	}

	// 3. Recursive Validation
	for _, child := range g.Children {
		child.Parent = g // Ensure parent pointer is set
		if err := child.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// DeriveKeywords scans conditions to auto-generate search tokens.
func (g *RuleGroup) DeriveKeywords() string {
	uniqueKeywords := make(map[string]struct{})

	// Helper to tokenize text
	addTokens := func(text string) {
		tokens := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
			return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
		})
		for _, t := range tokens {
			if len(t) >= 2 {
				uniqueKeywords[t] = struct{}{}
			}
		}
	}

	// 1. Extract from local string conditions
	for _, c := range g.Conditions {
		isStringField := c.Field == FieldDescription || c.Field == FieldVendor || c.Field == FieldCategory || c.Field == FieldMemo
		isStringOp := c.Operator == OpContains || c.Operator == OpEquals || c.Operator == OpStartsWith

		if isStringField && isStringOp {
			addTokens(c.Value)
		}
	}

	// 2. Recurse (Optional: Python didn't strictly recurse for keywords,
	// but mostly relied on top-level. We'll stick to top-level for safety).

	// Convert map to sorted string
	keys := make([]string, 0, len(uniqueKeywords))
	for k := range uniqueKeywords {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, " ")
}

// Compile prepares the rule for execution. Call this after loading from DB.
func (g *RuleGroup) Compile() error {
	for _, c := range g.Conditions {
		if err := c.compile(); err != nil {
			return err
		}
	}
	for _, child := range g.Children {
		if err := child.Compile(); err != nil {
			return err
		}
	}
	return nil
}

func (c *RuleCondition) compile() error {
	if c.isCompiled {
		return nil
	}

	var err error
	// Regex
	if c.Operator == OpRegex {
		c.compiledRegex, err = regexp.Compile(c.Value)
		if err != nil {
			return err
		}
	}
	// JSON Lists
	if c.Operator == OpIn || c.Operator == OpNotIn {
		var list []interface{}
		if err := json.Unmarshal([]byte(c.Value), &list); err != nil {
			return err
		}
		c.compiledSet = make(map[string]struct{})
		for _, v := range list {
			c.compiledSet[strings.ToLower(fmt.Sprint(v))] = struct{}{}
		}
	}
	// Numerics
	isNum := c.Operator == OpGt || c.Operator == OpGte || c.Operator == OpLt || c.Operator == OpLte || c.Operator == OpEquals
	if isNum && c.Field == FieldAmount {
		c.compiledNumeric, err = strconv.ParseFloat(c.Value, 64)
		if err != nil {
			return err
		}
	}
	// Dates
	if isNum && c.Field == FieldDate {
		c.compiledDate, err = time.Parse("2006-01-02", c.Value)
		if err != nil {
			return err
		}
	}

	c.isCompiled = true
	return nil
}

// ============================================================================
//  4. Optimization Layer (Candidate Selection)
// ============================================================================

// FilterCandidates reduces the rule set based on transaction content.
func FilterCandidates(tx Transaction, rules []*RuleGroup) []*RuleGroup {
	candidates := make([]*RuleGroup, 0)
	txTokens := extractTxTokens(tx)

	for _, r := range rules {
		if !r.Active {
			continue
		}

		// If rule has no keywords, it's a catch-all -> Must run
		if r.Keywords == "" {
			candidates = append(candidates, r)
			continue
		}

		// Check intersection
		if keywordsMatch(r.Keywords, txTokens) {
			candidates = append(candidates, r)
		}
	}
	return candidates
}

func extractTxTokens(tx Transaction) map[string]struct{} {
	tokens := make(map[string]struct{})

	add := func(s string) {
		parts := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
			return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
		})
		for _, p := range parts {
			if len(p) >= 2 {
				tokens[p] = struct{}{}
			}
		}
	}
	add(tx.Vendor)
	add(tx.Description)
	add(tx.Category)
	return tokens
}

func keywordsMatch(ruleKw string, txTokens map[string]struct{}) bool {
	// Check if ANY of the rule's keywords exist in the transaction
	kws := strings.Fields(ruleKw)
	for _, k := range kws {
		if _, exists := txTokens[k]; exists {
			return true
		}
	}
	return false
}

// ============================================================================
//  5. Evaluation Logic (The Engine)
// ============================================================================

// Evaluate runs the transaction through the rule group and its conditions.
// The queries interface is optional (can be nil); if provided, it will save
// the match explanation to the shadow_erp.rule_audit_logs database table.
func (g *RuleGroup) Evaluate(ctx context.Context, tx Transaction, queries interface {
	CreateRuleAuditLog(context.Context, database.CreateRuleAuditLogParams) (database.ShadowErpRuleAuditLog, error)
}) (bool, MatchExplanation) {
	exp := MatchExplanation{
		GroupID:      g.ID,
		GroupName:    g.Name,
		Logic:        string(g.Logic),
		ConditionExp: make([]ConditionResult, 0),
		ChildrenExp:  make([]MatchExplanation, 0),
	}

	results := []bool{}

	// 1. Evaluate Conditions
	for _, c := range g.Conditions {
		txVal, pass := c.evaluate(tx)
		exp.ConditionExp = append(exp.ConditionExp, ConditionResult{
			ConditionID: c.ID, Field: string(c.Field), Operator: string(c.Operator),
			TargetValue: c.Value, TxValue: txVal, Result: pass,
		})
		results = append(results, pass)
	}

	// 2. Evaluate Children (Recursion)
	for _, child := range g.Children {
		if !child.Active {
			continue
		}
		pass, childExp := child.Evaluate(ctx, tx, nil) // Don't persist child evaluations duplicating inserts
		exp.ChildrenExp = append(exp.ChildrenExp, childExp)
		results = append(results, pass)
	}

	// 3. Final Logic
	if len(results) == 0 {
		exp.FinalResult = false
	} else if g.Logic == LogicAnd {
		exp.FinalResult = true
		for _, r := range results {
			if !r {
				exp.FinalResult = false
				break
			}
		}
	} else { // OR
		exp.FinalResult = false
		for _, r := range results {
			if r {
				exp.FinalResult = true
				break
			}
		}
	}

	// 4. Save Execution Log to Database
	if queries != nil && g.Parent == nil { // Only persist at the top-level
		if matchInfoJSON, err := json.Marshal(exp); err == nil {

			var txUUID pgtype.UUID
			if tx.ID != "" {
				txUUID.Scan(tx.ID)
			}

			var ruleGroupID pgtype.Int4
			if exp.FinalResult {
				ruleGroupID.Scan(int32(g.ID))
			}

			// We launch this in a goroutine (or could be synchronous depending on needs)
			// For safety within a request lifecycle, synchronous is usually safer unless
			// we pass a detached background context.
			_, persistErr := queries.CreateRuleAuditLog(context.Background(), database.CreateRuleAuditLogParams{
				RealmID:             tx.EntityID, // EntityID in Transaction maps to RealmID in QBO context
				TransactionID:       txUUID,
				RuleGroupID:         ruleGroupID,
				Matched:             exp.FinalResult,
				MatchInfo:           matchInfoJSON,
				HumanReadableReason: exp.HumanReadableReason(),
			})
			if persistErr != nil {
				// We don't fail the transaction evaluation if logging fails
				fmt.Printf("Warning: failed to insert rule audit log: %v\n", persistErr)
			}
		}
	}

	return exp.FinalResult, exp
}

func (c *RuleCondition) evaluate(tx Transaction) (string, bool) {
	if !c.isCompiled {
		_ = c.compile()
	} // Lazy compile safety

	var val string
	switch c.Field {
	case FieldVendor:
		val = tx.Vendor
		return val, c.evalString(val)
	case FieldDescription:
		val = tx.Description
		return val, c.evalString(val)
	case FieldCategory:
		val = tx.Category
		return val, c.evalString(val)
	case FieldMemo:
		val = tx.Memo
		return val, c.evalString(val)
	case FieldAmount:
		val = fmt.Sprintf("%.2f", tx.Amount)
		return val, c.evalNumeric(tx.Amount)
	case FieldDate:
		val = tx.Date.Format("2006-01-02")
		return val, c.evalDate(tx.Date)
	}
	return "", false
}

// --- Type-Specific Helpers ---

func (c *RuleCondition) evalString(val string) bool {
	if c.Operator == OpIsNull {
		return val == ""
	}
	if c.Operator == OpIsNotNull {
		return val != ""
	}

	rawVal, lowerVal := val, strings.ToLower(val)
	target, lowerTarget := c.Value, strings.ToLower(c.Value)

	switch c.Operator {
	case OpEquals:
		return lowerVal == lowerTarget
	case OpEqualsCS:
		return rawVal == target
	case OpContains:
		return strings.Contains(lowerVal, lowerTarget)
	case OpContainsCS:
		return strings.Contains(rawVal, target)
	case OpStartsWith:
		return strings.HasPrefix(lowerVal, lowerTarget)
	case OpEndsWith:
		return strings.HasSuffix(lowerVal, lowerTarget)
	case OpIn:
		_, ok := c.compiledSet[lowerVal]
		return ok
	case OpNotIn:
		_, ok := c.compiledSet[lowerVal]
		return !ok
	case OpRegex:
		return c.compiledRegex != nil && c.compiledRegex.MatchString(rawVal)
	}
	return false
}

func (c *RuleCondition) evalNumeric(val float64) bool {
	t := c.compiledNumeric
	switch c.Operator {
	case OpGt:
		return val > t
	case OpGte:
		return val >= t
	case OpLt:
		return val < t
	case OpLte:
		return val <= t
	case OpEquals:
		return (val-t) < 0.00001 && (val-t) > -0.00001
	}
	return false
}

func (c *RuleCondition) evalDate(val time.Time) bool {
	// Strip time
	d := time.Date(val.Year(), val.Month(), val.Day(), 0, 0, 0, 0, val.Location())
	t := c.compiledDate

	switch c.Operator {
	case OpGt:
		return d.After(t)
	case OpGte:
		return d.After(t) || d.Equal(t)
	case OpLt:
		return d.Before(t)
	case OpLte:
		return d.Before(t) || d.Equal(t)
	case OpEquals:
		return d.Equal(t)
	}
	return false
}
