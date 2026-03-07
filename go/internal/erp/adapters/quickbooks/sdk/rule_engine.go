package quickbooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// ============================================================================
//  1. Enums & Constants
// ============================================================================

type Field string

const (
	FieldDescription Field = "description"
	FieldVendor      Field = "vendor"
	FieldCustomer    Field = "customer"
	FieldCategory    Field = "category"
	FieldAmount      Field = "amount"
	FieldDate        Field = "date"
	FieldTime        Field = "time"
	FieldMemo        Field = "memo"
	FieldRole        Field = "role"
	FieldUUID        Field = "uuid"
	FieldMCC         Field = "mcc"
	FieldInvoiceText Field = "invoice_text"
)

type Operator string

const (
	// String Operators (Case-Insensitive)
	OpEquals      Operator = "equals"
	OpContains    Operator = "contains"
	OpNotContains Operator = "not_contains"
	OpStartsWith  Operator = "startswith"
	OpEndsWith    Operator = "endswith"
	OpIn          Operator = "in"
	OpNotIn       Operator = "not_in"

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
	Customer    string
	Description string
	Amount      float64
	Category    string
	Date        time.Time
	Time        time.Time // time-of-day component; only H/M/S are compared
	Memo        string
	Role        string
	UUID        string
	MCC         string
	InvoiceText string
}

type RuleCondition struct {
	ID       int
	Field    Field
	Operator Operator
	Value    string

	// Internal compiled state
	compiledRegex      *regexp.Regexp
	compiledSet        map[string]struct{} // string/date set membership
	compiledNumericSet []float64           // numeric set membership (epsilon-compared)
	compiledNumeric    float64
	compiledDate       time.Time
	compiledTime       time.Time // H/M/S only; date portion is zeroed
	isCompiled         bool
}

type RuleGroup struct {
	ID       int
	Name     string
	Logic    LogicChoice
	Priority int
	Keywords string // Auto-generated for candidate selection optimization
	Active   bool

	TargetAccountID pgtype.UUID
	TargetVendorID  pgtype.UUID

	Conditions []*RuleCondition
	Children   []*RuleGroup
	Parent     *RuleGroup // Nested rule groups

	keywordSet map[string]struct{} // pre-split Keywords for O(1) lookup in FilterCandidates
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

// ============================================================================
//  3. Management Logic (Validation & Auto-Keywords)
// ============================================================================

// validOperators defines the set of operators that are semantically meaningful
// for each field type. Conditions outside this matrix compile but can never
// match, silently degrading into "always false" — caught at validation time.
var validOperators = map[Field]map[Operator]struct{}{
	FieldDescription: stringOps, FieldVendor: stringOps, FieldCustomer: stringOps,
	FieldCategory: stringOps, FieldMemo: stringOps, FieldRole: stringOps,
	FieldUUID: stringOps, FieldMCC: stringOps, FieldInvoiceText: stringOps,
	FieldAmount: numericOps,
	FieldDate:   dateOps,
	FieldTime:   timeOps,
}

var (
	stringOps = toSet(
		OpEquals, OpEqualsCS, OpContains, OpContainsCS, OpNotContains,
		OpStartsWith, OpEndsWith, OpIn, OpNotIn, OpIsNull, OpIsNotNull, OpRegex,
	)
	numericOps = toSet(OpEquals, OpGt, OpGte, OpLt, OpLte, OpIn, OpNotIn, OpIsNull, OpIsNotNull)
	dateOps    = toSet(OpEquals, OpGt, OpGte, OpLt, OpLte, OpIn, OpNotIn)
	timeOps    = toSet(OpEquals, OpGt, OpGte, OpLt, OpLte)
)

func toSet(ops ...Operator) map[Operator]struct{} {
	m := make(map[Operator]struct{}, len(ops))
	for _, o := range ops {
		m[o] = struct{}{}
	}
	return m
}

// Validate checks for circular dependencies, operator/field compatibility,
// and malformed values.
func (g *RuleGroup) Validate() error {
	if g.Parent != nil {
		node := g.Parent
		for node != nil {
			if node.ID == g.ID && g.ID != 0 {
				return errors.New("circular dependency detected")
			}
			node = node.Parent
		}
	}

	for _, c := range g.Conditions {
		if err := c.validateCompatibility(); err != nil {
			return fmt.Errorf("condition %d: %w", c.ID, err)
		}
		if err := c.compile(); err != nil {
			return fmt.Errorf("condition %d invalid: %v", c.ID, err)
		}
	}

	for _, child := range g.Children {
		child.Parent = g
		if err := child.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// validateCompatibility rejects operator/field pairs that compile but can never
// produce a meaningful match (e.g. regex on amount, gt on a string field).
func (c *RuleCondition) validateCompatibility() error {
	allowed, knownField := validOperators[c.Field]
	if !knownField {
		return fmt.Errorf("unknown field %q", c.Field)
	}
	if _, ok := allowed[c.Operator]; !ok {
		return fmt.Errorf("operator %q is not valid for field %q", c.Operator, c.Field)
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
		isStringField := c.Field == FieldDescription || c.Field == FieldVendor || c.Field == FieldCustomer ||
			c.Field == FieldCategory || c.Field == FieldMemo || c.Field == FieldRole ||
			c.Field == FieldUUID || c.Field == FieldMCC || c.Field == FieldInvoiceText
		isStringOp := c.Operator == OpContains || c.Operator == OpEquals || c.Operator == OpStartsWith || c.Operator == OpEndsWith

		if isStringField && isStringOp {
			addTokens(c.Value)
		}
		if isStringField && (c.Operator == OpIn || c.Operator == OpNotIn) {
			var list []interface{}
			if json.Unmarshal([]byte(c.Value), &list) == nil {
				for _, v := range list {
					addTokens(fmt.Sprint(v))
				}
			}
		}
	}

	// 2. Recurse into children so the parent's keyword set is a union of the
	// entire subtree. This prevents FilterCandidates from excluding a parent
	// whose child conditions would match (P0-B: descendant-safe filtering).
	for _, child := range g.Children {
		for _, t := range strings.Fields(child.DeriveKeywords()) {
			uniqueKeywords[t] = struct{}{}
		}
	}

	// Build the sorted string representation and cache the pre-split set.
	keys := make([]string, 0, len(uniqueKeywords))
	for k := range uniqueKeywords {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	g.keywordSet = uniqueKeywords
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
	// Pre-split keywords into a set for O(1) lookup in FilterCandidates,
	// avoiding strings.Fields() per rule per transaction at evaluation time.
	if g.Keywords != "" {
		kws := strings.Fields(g.Keywords)
		g.keywordSet = make(map[string]struct{}, len(kws))
		for _, k := range kws {
			g.keywordSet[k] = struct{}{}
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
	// JSON Lists (OpIn / OpNotIn): parse into type-appropriate lookup structures.
	if c.Operator == OpIn || c.Operator == OpNotIn {
		var list []interface{}
		if err := json.Unmarshal([]byte(c.Value), &list); err != nil {
			return err
		}

		if c.Field == FieldAmount {
			c.compiledNumericSet = make([]float64, 0, len(list))
			for _, v := range list {
				f, err := strconv.ParseFloat(fmt.Sprint(v), 64)
				if err != nil {
					return fmt.Errorf("non-numeric value in amount set: %v", v)
				}
				c.compiledNumericSet = append(c.compiledNumericSet, f)
			}
		} else {
			c.compiledSet = make(map[string]struct{}, len(list))
			for _, v := range list {
				s := fmt.Sprint(v)
				if c.Field == FieldDate {
					if _, err := time.Parse("2006-01-02", s); err == nil {
						c.compiledSet[s] = struct{}{}
						continue
					}
				}
				c.compiledSet[strings.ToLower(s)] = struct{}{}
			}
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
	// Times (HH:MM or HH:MM:SS)
	if isNum && c.Field == FieldTime {
		c.compiledTime, err = time.Parse("15:04:05", c.Value)
		if err != nil {
			c.compiledTime, err = time.Parse("15:04", c.Value)
			if err != nil {
				return err
			}
		}
	}

	c.isCompiled = true
	return nil
}

// ============================================================================
//  4. Optimization Layer (Candidate Selection)
// ============================================================================

// splitTokens splits s on non-alphanumeric runes and returns the resulting tokens.
// Used for whole-word "contains" semantics and candidate filter tokenization.
func splitTokens(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	})
}

// FilterCandidates reduces the rule set based on transaction content.
func FilterCandidates(tx Transaction, rules []*RuleGroup) []*RuleGroup {
	candidates := make([]*RuleGroup, 0, len(rules))
	txTokens := extractTxTokens(tx)

	for _, r := range rules {
		if !r.Active {
			continue
		}
		if len(r.keywordSet) == 0 {
			candidates = append(candidates, r)
			continue
		}
		if keywordSetOverlaps(r.keywordSet, txTokens) {
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
	add(tx.Customer)
	add(tx.Description)
	add(tx.Category)
	add(tx.Memo)
	add(tx.MCC)
	add(tx.InvoiceText)
	add(tx.Role)
	add(tx.UUID)
	return tokens
}

// keywordSetOverlaps returns true if any keyword in the pre-split rule set
// exists in the transaction token set. Iterates the smaller set for efficiency.
func keywordSetOverlaps(ruleKws, txTokens map[string]struct{}) bool {
	small, big := ruleKws, txTokens
	if len(small) > len(big) {
		small, big = big, small
	}
	for k := range small {
		if _, exists := big[k]; exists {
			return true
		}
	}
	return false
}

// ============================================================================
//  5. Evaluation Logic (The Engine)
// ============================================================================

// Evaluate runs the transaction through the rule group and its conditions.
// It returns the match result and a structured explanation trace for audit logging.
// Audit-log persistence is handled by the caller (service layer) after a
// proposed_transactions row exists, fixing the NOT NULL FK constraint issue.
func (g *RuleGroup) Evaluate(tx Transaction) (bool, MatchExplanation) {
	exp := MatchExplanation{
		GroupID:      g.ID,
		GroupName:    g.Name,
		Logic:        string(g.Logic),
		ConditionExp: make([]ConditionResult, 0),
		ChildrenExp:  make([]MatchExplanation, 0),
	}

	results := make([]bool, 0, len(g.Conditions)+len(g.Children))

	for _, c := range g.Conditions {
		txVal, pass := c.evaluate(tx)
		exp.ConditionExp = append(exp.ConditionExp, ConditionResult{
			ConditionID: c.ID, Field: string(c.Field), Operator: string(c.Operator),
			TargetValue: c.Value, TxValue: txVal, Result: pass,
		})
		results = append(results, pass)
	}

	for _, child := range g.Children {
		if !child.Active {
			continue
		}
		pass, childExp := child.Evaluate(tx)
		exp.ChildrenExp = append(exp.ChildrenExp, childExp)
		results = append(results, pass)
	}

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
	} else {
		exp.FinalResult = false
		for _, r := range results {
			if r {
				exp.FinalResult = true
				break
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
	case FieldCustomer:
		val = tx.Customer
	case FieldDescription:
		val = tx.Description
	case FieldCategory:
		val = tx.Category
	case FieldMemo:
		val = tx.Memo
	case FieldRole:
		val = tx.Role
	case FieldUUID:
		val = tx.UUID
	case FieldMCC:
		val = tx.MCC
	case FieldInvoiceText:
		val = tx.InvoiceText
	case FieldAmount:
		// Use %g to preserve full float64 precision in the audit trail.
		// %.2f would round 100.005 → "100.01" while evaluation uses 100.005,
		// creating an auditor-visible mismatch (audit finding #5).
		val = fmt.Sprintf("%g", tx.Amount)
		return val, c.evalNumeric(tx.Amount)
	case FieldDate:
		val = tx.Date.Format("2006-01-02")
		return val, c.evalDate(tx.Date)
	case FieldTime:
		val = tx.Time.Format("15:04:05")
		return val, c.evalTime(tx.Time)
	default:
		return "", false
	}
	return val, c.evalString(val)
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
		// Whole-word semantics: value must appear as a complete token (same tokenization as filter).
		for _, tok := range splitTokens(lowerVal) {
			if tok == lowerTarget {
				return true
			}
		}
		return false
	case OpNotContains:
		for _, tok := range splitTokens(lowerVal) {
			if tok == lowerTarget {
				return false
			}
		}
		return true
	case OpContainsCS:
		// Whole-word semantics, case-sensitive.
		for _, tok := range splitTokens(rawVal) {
			if tok == target {
				return true
			}
		}
		return false
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

const numericEpsilon = 1e-5

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
		d := val - t
		return d < numericEpsilon && d > -numericEpsilon
	case OpIn:
		return numericSetContains(c.compiledNumericSet, val)
	case OpNotIn:
		return !numericSetContains(c.compiledNumericSet, val)
	case OpIsNull:
		return val == 0
	case OpIsNotNull:
		return val != 0
	}
	return false
}

func numericSetContains(set []float64, val float64) bool {
	for _, s := range set {
		d := val - s
		if d < numericEpsilon && d > -numericEpsilon {
			return true
		}
	}
	return false
}

func (c *RuleCondition) evalDate(val time.Time) bool {
	// Normalize to UTC midnight to match how time.Parse instantiates compiledDate.
	utc := val.UTC()
	d := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
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
	case OpIn:
		key := d.Format("2006-01-02")
		_, ok := c.compiledSet[key]
		return ok
	case OpNotIn:
		key := d.Format("2006-01-02")
		_, ok := c.compiledSet[key]
		return !ok
	}
	return false
}

func (c *RuleCondition) evalTime(val time.Time) bool {
	// Extract H/M/S in UTC; anchor to a zero date so time.Time comparisons work correctly.
	utc := val.UTC()
	d := time.Date(0, 1, 1, utc.Hour(), utc.Minute(), utc.Second(), 0, time.UTC)
	ct := c.compiledTime
	t := time.Date(0, 1, 1, ct.Hour(), ct.Minute(), ct.Second(), 0, time.UTC)

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
