package workers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Yankzy/usetoro/internal/erp/ase"
)

var clsTestWorker = &ClassificationStageWorker{logger: testLogger()}

// ---------------------------------------------------------------------------
// renderPrompt
// ---------------------------------------------------------------------------

func TestRenderPrompt_GroupKeyPlaceholders(t *testing.T) {
	tmpl := "Class: {macro_class}, Direction: {cash_direction}"

	tests := []struct {
		name     string
		groupKey string
		want     string
	}{
		{"OUTFLOW", "OUTFLOW", "Class: OUTFLOW, Direction: OUTFLOW"},
		{"INFLOW", "INFLOW", "Class: INFLOW, Direction: INFLOW"},
		{"EXPENSE", "EXPENSE", "Class: EXPENSE, Direction: EXPENSE"},
		{"ASSET", "ASSET", "Class: ASSET, Direction: ASSET"},
		{"REVENUE", "REVENUE", "Class: REVENUE, Direction: REVENUE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clsTestWorker.renderPrompt(tmpl, tt.groupKey, nil)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderPrompt_AccountTypeOptions(t *testing.T) {
	tmpl := "Options: {account_type_options}"
	for _, mc := range []string{"ASSET", "LIABILITY", "EQUITY", "REVENUE", "EXPENSE"} {
		t.Run(mc, func(t *testing.T) {
			got := clsTestWorker.renderPrompt(tmpl, mc, nil)
			if !strings.Contains(got, ase.AccountTypeOptions[mc]) {
				t.Errorf("missing account_type_options for %s: %q", mc, got)
			}
		})
	}
}

func TestRenderPrompt_MacroClassSpecificRules(t *testing.T) {
	tmpl := "Rules: {macro_class_specific_rules}"
	for _, mc := range []string{"ASSET", "LIABILITY", "EQUITY", "REVENUE", "EXPENSE"} {
		t.Run(mc, func(t *testing.T) {
			got := clsTestWorker.renderPrompt(tmpl, mc, nil)
			want := ase.MacroClassSpecificRules[mc]
			if !strings.Contains(got, want) {
				t.Errorf("missing macro_class_specific_rules for %s", mc)
			}
		})
	}
}

func TestRenderPrompt_ClassificationRulesFromCtxMap(t *testing.T) {
	tmpl := "Rules: {classification_rules}"
	rules := "CUSTOM RULE SET FOR TESTING"
	ctx := map[string]interface{}{"classification_rules": rules}
	t.Run("injected from ctxMap", func(t *testing.T) {
		got := clsTestWorker.renderPrompt(tmpl, "EXPENSE", ctx)
		if !strings.Contains(got, rules) {
			t.Errorf("missing classification_rules from ctxMap: %q", got)
		}
	})
	t.Run("passes through when absent", func(t *testing.T) {
		got := clsTestWorker.renderPrompt(tmpl, "EXPENSE", nil)
		if !strings.Contains(got, "{classification_rules}") {
			t.Errorf("placeholder should pass through when not in ctxMap: %q", got)
		}
	})
}

func TestBuildOutflowRules(t *testing.T) {
	t.Run("without hints", func(t *testing.T) {
		rules := buildOutflowRules(nil, nil)
		if !strings.Contains(rules, "CUSTOMER REFUND / CHARGEBACK (REVENUE)") {
			t.Errorf("missing contra-revenue rule 4")
		}
		if !strings.Contains(rules, "STOP here — this is NOT an expense") {
			t.Errorf("missing STOP enforcement")
		}
		if strings.Contains(rules, "Known customers detected") {
			t.Errorf("should not contain hints when none provided")
		}
	})
	t.Run("with hints", func(t *testing.T) {
		rules := buildOutflowRules([]string{"Acme Corp", "Bob's Shop"}, nil)
		if !strings.Contains(rules, "Known Revenue Customers Identified in this Batch: Acme Corp, Bob's Shop.") {
			t.Errorf("missing hints in rules: %s", rules)
		}
	})
}

func TestBuildInflowRules(t *testing.T) {
	t.Run("without hints", func(t *testing.T) {
		rules := buildInflowRules(nil, nil)
		if !strings.Contains(rules, "VENDOR REFUND (EXPENSE)") {
			t.Errorf("missing contra-expense rule 4")
		}
		if !strings.Contains(rules, "STOP here — this is NOT revenue") {
			t.Errorf("missing STOP enforcement")
		}
		if strings.Contains(rules, "Known vendors detected") {
			t.Errorf("should not contain hints when none provided")
		}
	})
	t.Run("with hints", func(t *testing.T) {
		rules := buildInflowRules([]string{"Amazon", "Home Depot"}, nil)
		if !strings.Contains(rules, "Known Expense Vendors Identified in this Batch: Amazon, Home Depot.") {
			t.Errorf("missing hints in rules: %s", rules)
		}
	})
}

func TestFindEntityHints(t *testing.T) {
	t.Run("matches found", func(t *testing.T) {
		rows := []map[string]interface{}{
			{"raw_description": "AWS Refund 12345"},
			{"raw_description": "Office supplies purchase"},
			{"raw_description": "Home Depot credit"},
		}
		names := []string{"AWS", "Home Depot", "Staples"}
		hits := findEntityHints(rows, names)
		if len(hits) != 2 {
			t.Fatalf("expected 2 hits, got %d: %v", len(hits), hits)
		}
		if hits[0] != "AWS" || hits[1] != "Home Depot" {
			t.Errorf("unexpected hits: %v", hits)
		}
	})
	t.Run("case insensitive", func(t *testing.T) {
		rows := []map[string]interface{}{
			{"raw_description": "amazon REFUND"},
		}
		hits := findEntityHints(rows, []string{"Amazon"})
		if len(hits) != 1 {
			t.Fatalf("expected case-insensitive match, got %d", len(hits))
		}
	})
	t.Run("deduplicates hits", func(t *testing.T) {
		rows := []map[string]interface{}{
			{"raw_description": "AWS payment"},
			{"raw_description": "AWS refund"},
		}
		hits := findEntityHints(rows, []string{"AWS"})
		if len(hits) != 1 {
			t.Fatalf("expected deduplicated, got %d: %v", len(hits), hits)
		}
	})
	t.Run("no matches", func(t *testing.T) {
		rows := []map[string]interface{}{
			{"raw_description": "Unknown vendor"},
		}
		hits := findEntityHints(rows, []string{"AWS", "Staples"})
		if len(hits) != 0 {
			t.Errorf("expected no hits, got %v", hits)
		}
	})
	t.Run("empty rows", func(t *testing.T) {
		hits := findEntityHints(nil, []string{"AWS"})
		if len(hits) != 0 {
			t.Errorf("expected no hits for nil rows")
		}
	})
	t.Run("empty names", func(t *testing.T) {
		rows := []map[string]interface{}{
			{"raw_description": "AWS refund"},
		}
		hits := findEntityHints(rows, nil)
		if len(hits) != 0 {
			t.Errorf("expected no hits for nil names")
		}
	})
	t.Run("empty description", func(t *testing.T) {
		rows := []map[string]interface{}{
			{"raw_description": ""},
		}
		hits := findEntityHints(rows, []string{"AWS"})
		if len(hits) != 0 {
			t.Errorf("expected no hits for empty description")
		}
	})
	t.Run("intra-file word match", func(t *testing.T) {
		rows := []map[string]interface{}{
			{"raw_description": "AMAZON REFUND"},
		}
		opposingDescs := []string{"AMAZON PURCHASE", "OFFICE DEPOT"}
		hits := findIntraFileHints(rows, opposingDescs, nil)
		if len(hits) != 1 || hits[0] != "AMAZON PURCHASE" {
			t.Errorf("expected intra-file hit on AMAZON, got %v", hits)
		}
	})
	t.Run("intra-file skips known DB names", func(t *testing.T) {
		rows := []map[string]interface{}{
			{"raw_description": "AWS REFUND"},
		}
		opposingDescs := []string{"AWS PAYMENT"}
		knownNames := []string{"AWS"} // already in DB
		hits := findIntraFileHints(rows, opposingDescs, knownNames)
		if len(hits) != 0 {
			t.Errorf("should skip AWS because it is a known DB name: %v", hits)
		}
	})
	t.Run("intra-file no match on stop words", func(t *testing.T) {
		rows := []map[string]interface{}{
			{"raw_description": "VENMO CASHOUT"},
		}
		opposingDescs := []string{"payment", "deposit", "transfer"}
		hits := findIntraFileHints(rows, opposingDescs, nil)
		if len(hits) != 0 {
			t.Errorf("stop words should not match: %v", hits)
		}
	})
	t.Run("intra-file deduplicates", func(t *testing.T) {
		rows := []map[string]interface{}{
			{"raw_description": "UBER REFUND"},
			{"raw_description": "UBER CREDIT"},
		}
		opposingDescs := []string{"UBER *EATS"}
		hits := findIntraFileHints(rows, opposingDescs, nil)
		if len(hits) != 1 {
			t.Errorf("expected deduplicated, got %d: %v", len(hits), hits)
		}
	})
}

func TestExtractDescs(t *testing.T) {
	rows := []map[string]interface{}{
		{"raw_description": "AWS payment"},
		{"raw_description": ""},
		{"other_field": "no desc"},
		{"raw_description": "Office supplies"},
	}
	descs := extractDescs(rows)
	if len(descs) != 2 {
		t.Fatalf("expected 2 descs, got %d: %v", len(descs), descs)
	}
}

func TestRenderPrompt_CtxMapKeys(t *testing.T) {
	tmpl := "Industry: {company_industry_description}, Accounts: {json_list_of_bank_accounts}, Vendors: {json_list_of_existing_vendors_or_customers_with_ids}, COA: {json_list_of_all_filtered_accounts}"
	ctx := map[string]interface{}{
		"company_industry_description":              "Construction — B2B — Acme Corp",
		"json_list_of_bank_accounts":                []string{"checking", "savings"},
		"json_list_of_existing_vendors_or_customers_with_ids": "### VENDORS\n[id:1] Bob's",
		"json_list_of_all_filtered_accounts":                 "### Expense\n[erp_id:42] Supplies",
	}
	got := clsTestWorker.renderPrompt(tmpl, "EXPENSE", ctx)

	checks := []string{
		"Construction — B2B — Acme Corp",
		`["checking","savings"]`,
		"### VENDORS",
		"### Expense",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in output:\n%s", want, got)
		}
	}
	// Ensure no placeholders remain.
	for _, ph := range []string{"{company_industry_description}", "{json_list_of_bank_accounts}",
		"{json_list_of_existing_vendors_or_customers_with_ids}", "{json_list_of_all_filtered_accounts}"} {
		if strings.Contains(got, ph) {
			t.Errorf("placeholder %s not resolved: %q", ph, got)
		}
	}
}

func TestRenderPrompt_UnknownPlaceholderPassesThrough(t *testing.T) {
	got := clsTestWorker.renderPrompt("Keep {unknown_key} as-is", "EXPENSE", nil)
	if !strings.Contains(got, "{unknown_key}") {
		t.Errorf("unknown placeholder should pass through unchanged: %q", got)
	}
}

func TestRenderPrompt_EmptyTemplate(t *testing.T) {
	got := clsTestWorker.renderPrompt("", "EXPENSE", nil)
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestRenderPrompt_NilCtxMap(t *testing.T) {
	got := clsTestWorker.renderPrompt("{macro_class}", "ASSET", nil)
	if got != "ASSET" {
		t.Errorf("expected ASSET, got %q", got)
	}
}

func TestRenderPrompt_StringAndNonStringCtxKeys(t *testing.T) {
	// If a ctx value is already a string, it's inserted directly; otherwise JSON-marshaled.
	ctx := map[string]interface{}{
		"str_key":  "plain text",
		"json_key": map[string]string{"a": "b"},
	}
	got := clsTestWorker.renderPrompt("{str_key} | {json_key}", "X", ctx)
	if !strings.Contains(got, "plain text") {
		t.Errorf("string key not inserted directly: %q", got)
	}
	if !strings.Contains(got, `{"a":"b"}`) {
		t.Errorf("non-string key not JSON-marshaled: %q", got)
	}
}

func TestRenderPrompt_ContextKeyNotInTemplateSkipped(t *testing.T) {
	ctx := map[string]interface{}{"unused_key": "value"}
	got := clsTestWorker.renderPrompt("{macro_class}", "ASSET", ctx)
	if got != "ASSET" {
		t.Errorf("expected ASSET, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// groupRows
// ---------------------------------------------------------------------------

func makeRow(id, desc, amount, merchant, macroClass string) map[string]interface{} {
	row := map[string]interface{}{
		"id":         id,
		"raw_amount": amount,
	}
	if desc != "" {
		row["raw_description"] = desc
	}
	if merchant != "" {
		row["merchant_name"] = merchant
	}
	if macroClass != "" {
		row["macro_class"] = macroClass
	}
	return row
}

func TestGroupRows_Direction_Mixed(t *testing.T) {
	rows := []map[string]interface{}{
		makeRow("1", "desc1", "-50", "", ""),
		makeRow("2", "desc2", "100", "", ""),
		makeRow("3", "desc3", "-200", "", ""),
		makeRow("4", "desc4", "75", "", ""),
	}
	groups := clsTestWorker.groupRows(rows, "direction", "NEGATIVE")
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if len(groups["OUTFLOW"]) != 2 {
		t.Errorf("expected 2 outflows, got %d", len(groups["OUTFLOW"]))
	}
	if len(groups["INFLOW"]) != 2 {
		t.Errorf("expected 2 inflows, got %d", len(groups["INFLOW"]))
	}
}

func TestGroupRows_Direction_AllOutflow(t *testing.T) {
	rows := []map[string]interface{}{
		makeRow("1", "d1", "-50", "", ""),
		makeRow("2", "d2", "-100", "", ""),
	}
	groups := clsTestWorker.groupRows(rows, "direction", "NEGATIVE")
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if len(groups["OUTFLOW"]) != 2 {
		t.Errorf("expected 2 outflows")
	}
	if _, ok := groups["INFLOW"]; ok {
		t.Errorf("should not have INFLOW group")
	}
}

func TestGroupRows_Direction_AllInflow(t *testing.T) {
	rows := []map[string]interface{}{
		makeRow("1", "d1", "50", "", ""),
		makeRow("2", "d2", "100", "", ""),
	}
	groups := clsTestWorker.groupRows(rows, "direction", "NEGATIVE")
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if len(groups["INFLOW"]) != 2 {
		t.Errorf("expected 2 inflows")
	}
	if _, ok := groups["OUTFLOW"]; ok {
		t.Errorf("should not have OUTFLOW group")
	}
}

func TestGroupRows_Direction_PositiveOutflowSign(t *testing.T) {
	// outflow_is = "POSITIVE": positive amounts are outflow
	rows := []map[string]interface{}{
		makeRow("1", "d1", "50", "", ""),
		makeRow("2", "d2", "-100", "", ""),
	}
	groups := clsTestWorker.groupRows(rows, "direction", "POSITIVE")
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if len(groups["OUTFLOW"]) != 1 || groups["OUTFLOW"][0]["id"] != "1" {
		t.Errorf("row 1 (positive) should be OUTFLOW under POSITIVE convention")
	}
	if len(groups["INFLOW"]) != 1 || groups["INFLOW"][0]["id"] != "2" {
		t.Errorf("row 2 (negative) should be INFLOW under POSITIVE convention")
	}
}

func TestGroupRows_Direction_EmptyAmountSkipped(t *testing.T) {
	rows := []map[string]interface{}{
		{"id": "1", "raw_amount": "50"},
		{"id": "2"}, // no raw_amount
		{"id": "3", "raw_amount": "not-a-number"},
		{"id": "4", "raw_amount": "-25"},
	}
	groups := clsTestWorker.groupRows(rows, "direction", "NEGATIVE")
	outflows := len(groups["OUTFLOW"])
	inflows := len(groups["INFLOW"])
	if outflows != 1 || inflows != 1 {
		t.Errorf("expected 1 outflow + 1 inflow, got %d + %d", outflows, inflows)
	}
}

func TestGroupRows_Direction_Empty(t *testing.T) {
	groups := clsTestWorker.groupRows(nil, "direction", "NEGATIVE")
	if len(groups) != 0 {
		t.Errorf("expected empty map, got %d groups", len(groups))
	}
}

func TestGroupRows_MacroClass_Mixed(t *testing.T) {
	rows := []map[string]interface{}{
		makeRow("1", "d1", "", "", "EXPENSE"),
		makeRow("2", "d2", "", "", "EXPENSE"),
		makeRow("3", "d3", "", "", "ASSET"),
		makeRow("4", "d4", "", "", "REVENUE"),
		makeRow("5", "d5", "", "", "EXPENSE"),
	}
	groups := clsTestWorker.groupRows(rows, "macro_class", "")
	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(groups))
	}
	if len(groups["EXPENSE"]) != 3 {
		t.Errorf("expected 3 EXPENSE, got %d", len(groups["EXPENSE"]))
	}
	if len(groups["ASSET"]) != 1 {
		t.Errorf("expected 1 ASSET, got %d", len(groups["ASSET"]))
	}
	if len(groups["REVENUE"]) != 1 {
		t.Errorf("expected 1 REVENUE, got %d", len(groups["REVENUE"]))
	}
}

func TestGroupRows_MacroClass_SingleClass(t *testing.T) {
	rows := []map[string]interface{}{
		makeRow("1", "d1", "", "", "LIABILITY"),
		makeRow("2", "d2", "", "", "LIABILITY"),
	}
	groups := clsTestWorker.groupRows(rows, "macro_class", "")
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if len(groups["LIABILITY"]) != 2 {
		t.Errorf("expected 2 rows")
	}
}

func TestGroupRows_MacroClass_EmptyMacroClass(t *testing.T) {
	rows := []map[string]interface{}{
		makeRow("1", "d1", "", "", "EXPENSE"),
		makeRow("2", "d2", "", "", ""), // no macro_class
		makeRow("3", "d3", "", "", ""),
	}
	groups := clsTestWorker.groupRows(rows, "macro_class", "")
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if len(groups["EXPENSE"]) != 1 {
		t.Errorf("expected 1 EXPENSE row")
	}
	if len(groups["UNKNOWN"]) != 2 {
		t.Errorf("expected 2 UNKNOWN rows, got %d", len(groups["UNKNOWN"]))
	}
}

func TestGroupRows_MacroClass_Empty(t *testing.T) {
	groups := clsTestWorker.groupRows(nil, "macro_class", "")
	if len(groups) != 0 {
		t.Errorf("expected empty map, got %d groups", len(groups))
	}
}

func TestGroupRows_None(t *testing.T) {
	rows := []map[string]interface{}{
		makeRow("1", "d1", "-50", "", "EXPENSE"),
		makeRow("2", "d2", "100", "", "REVENUE"),
		makeRow("3", "d3", "0", "", "ASSET"),
	}
	groups := clsTestWorker.groupRows(rows, "none", "")
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if len(groups["default"]) != 3 {
		t.Errorf("expected 3 rows in default, got %d", len(groups["default"]))
	}
}

func TestGroupRows_None_Empty(t *testing.T) {
	groups := clsTestWorker.groupRows(nil, "none", "")
	if len(groups) != 1 {
		t.Fatalf("expected 1 group (empty default), got %d", len(groups))
	}
	if len(groups["default"]) != 0 {
		t.Errorf("expected empty default, got %d rows", len(groups["default"]))
	}
}

func TestGroupRows_UnknownGroupBy(t *testing.T) {
	rows := []map[string]interface{}{makeRow("1", "d1", "50", "", "")}
	groups := clsTestWorker.groupRows(rows, "unknown_strategy", "")
	if len(groups) != 1 {
		t.Fatalf("expected 1 default group for unknown strategy, got %d", len(groups))
	}
	if len(groups["default"]) != 1 {
		t.Errorf("expected 1 row in default")
	}
}

func TestGroupRows_TransactionType_Mixed(t *testing.T) {
	rows := []map[string]interface{}{
		makeRow("1", "d1", "", "", "EXPENSE"),
		makeRow("2", "d2", "", "", "REVENUE"),
		makeRow("3", "d3", "", "", "ASSET"),
		makeRow("4", "d4", "", "", "REVENUE"),
		makeRow("5", "d5", "", "", "LIABILITY"),
	}
	groups := clsTestWorker.groupRows(rows, "transaction_type", "")
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if len(groups["OUTFLOW"]) != 3 {
		t.Errorf("expected 3 outflows (EXPENSE+ASSET+LIABILITY), got %d", len(groups["OUTFLOW"]))
	}
	if len(groups["INFLOW"]) != 2 {
		t.Errorf("expected 2 inflows (REVENUE), got %d", len(groups["INFLOW"]))
	}
}

func TestGroupRows_TransactionType_AllOutflow(t *testing.T) {
	rows := []map[string]interface{}{
		makeRow("1", "d1", "", "", "EXPENSE"),
		makeRow("2", "d2", "", "", "ASSET"),
		makeRow("3", "d3", "", "", "EQUITY"),
	}
	groups := clsTestWorker.groupRows(rows, "transaction_type", "")
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if len(groups["OUTFLOW"]) != 3 {
		t.Errorf("expected 3 outflows")
	}
}

func TestGroupRows_TransactionType_AllInflow(t *testing.T) {
	rows := []map[string]interface{}{
		makeRow("1", "d1", "", "", "REVENUE"),
		makeRow("2", "d2", "", "", "REVENUE"),
	}
	groups := clsTestWorker.groupRows(rows, "transaction_type", "")
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if len(groups["INFLOW"]) != 2 {
		t.Errorf("expected 2 inflows")
	}
}

// ---------------------------------------------------------------------------
// patchColumnMap completeness
// ---------------------------------------------------------------------------

func TestPatchColumnMap_AllExpectedMappings(t *testing.T) {
	expected := map[string]string{
		"macro_class":      "macro_class",
		"account_type":     "account_type",
		"reasoning":        "ai_reasoning",
		"new_clean_name":   "merchant_name",
		"match_confidence": "confidence_score",
		"requires_split":   "split_suggestion",
	}
	for k, want := range expected {
		got, ok := patchColumnMap[k]
		if !ok {
			t.Errorf("missing mapping for %q", k)
		} else if got != want {
			t.Errorf("patchColumnMap[%q] = %q, want %q", k, got, want)
		}
	}
	if len(patchColumnMap) != len(expected) {
		t.Errorf("patchColumnMap has %d entries, expected %d — extra or unexpected mappings",
			len(patchColumnMap), len(expected))
	}
}

// ---------------------------------------------------------------------------
// macroClassSpecificRules completeness
// ---------------------------------------------------------------------------

func TestMacroClassSpecificRules_AllClassesPresent(t *testing.T) {
	for _, mc := range []string{"ASSET", "LIABILITY", "EQUITY", "REVENUE", "EXPENSE"} {
		rules, ok := ase.MacroClassSpecificRules[mc]
		if !ok {
			t.Errorf("missing macroClassSpecificRules for %s", mc)
		}
		if rules == "" {
			t.Errorf("empty rules for %s", mc)
		}
	}
}

// ---------------------------------------------------------------------------
// accountTypeOptions completeness
// ---------------------------------------------------------------------------

func TestAccountTypeOptions_AllClassesPresent(t *testing.T) {
	for _, mc := range []string{"ASSET", "LIABILITY", "EQUITY", "REVENUE", "EXPENSE"} {
		opts, ok := ase.AccountTypeOptions[mc]
		if !ok {
			t.Errorf("missing accountTypeOptions for %s", mc)
		}
		if opts == "" {
			t.Errorf("empty accountTypeOptions for %s", mc)
		}
	}
}

// ---------------------------------------------------------------------------
// dispatchGroup patch parsing (pure logic, no NATS)
// ---------------------------------------------------------------------------

func TestParsePatchesFromProof_SingleObject(t *testing.T) {
	// Simulates what happens after unmarshaling proof.Data as an object.
	data := json.RawMessage(`{"op":"add","path":"/rows","value":{"0":{"id":"tx_1","macro_class":"EXPENSE","reasoning":"test"}}}`)
	var patchWrapper map[string]interface{}
	if err := json.Unmarshal(data, &patchWrapper); err != nil {
		t.Fatal(err)
	}
	rowsValue, ok := patchWrapper["value"]
	if !ok {
		t.Fatal("missing value")
	}
	rowsMap, ok := rowsValue.(map[string]interface{})
	if !ok {
		t.Fatal("value not a map")
	}
	var patches []map[string]interface{}
	for _, v := range rowsMap {
		if rowMap, ok := v.(map[string]interface{}); ok {
			patches = append(patches, rowMap)
		}
	}
	if len(patches) != 1 {
		t.Fatalf("expected 1 patch, got %d", len(patches))
	}
	if patches[0]["macro_class"] != "EXPENSE" {
		t.Errorf("wrong macro_class: %v", patches[0]["macro_class"])
	}
}

func TestParsePatchesFromProof_ArrayFallback(t *testing.T) {
	// Simulates the fallback: proof.Data is an array of patches.
	data := json.RawMessage(`[{"op":"add","path":"/rows","value":{"0":{"id":"tx_1","account_type":"Expense"}}}]`)
	var patchWrapper map[string]interface{}
	if err := json.Unmarshal(data, &patchWrapper); err == nil {
		t.Fatal("expected unmarshal as map to fail for array input")
	}
	// Fallback: unmarshal as array.
	var patches []map[string]interface{}
	if err := json.Unmarshal(data, &patches); err != nil {
		t.Fatal(err)
	}
	// Pick the one with path="/rows".
	for _, p := range patches {
		if path, _ := p["path"].(string); path == "/rows" {
			patchWrapper = p
			break
		}
	}
	if patchWrapper == nil {
		t.Fatal("no patch with path=/rows found")
	}
	rowsValue, ok := patchWrapper["value"]
	if !ok {
		t.Fatal("missing value")
	}
	rowsMap := rowsValue.(map[string]interface{})
	if len(rowsMap) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rowsMap))
	}
}

func TestParsePatchesFromProof_ArrayNoRowsPath(t *testing.T) {
	// No patch has path="/rows" — fall back to first element.
	data := json.RawMessage(`[{"op":"add","path":"/other","value":{"0":{"id":"tx_1","field":"val"}}}]`)
	var patchWrapper map[string]interface{}
	var patches []map[string]interface{}
	if err := json.Unmarshal(data, &patches); err != nil {
		t.Fatal(err)
	}
	for _, p := range patches {
		if path, _ := p["path"].(string); path == "/rows" {
			patchWrapper = p
			break
		}
	}
	if patchWrapper == nil && len(patches) > 0 {
		patchWrapper = patches[0]
	}
	if patchWrapper == nil {
		t.Fatal("no patch found")
	}
	if patchWrapper["path"] != "/other" {
		t.Errorf("expected /other, got %v", patchWrapper["path"])
	}
}

func TestParsePatchesFromProof_MalformedArray(t *testing.T) {
	data := json.RawMessage(`not json`)
	var patches []map[string]interface{}
	if err := json.Unmarshal(data, &patches); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}
