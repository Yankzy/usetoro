package quickbooks

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Audit findings regression (rule_engine_audit.md)
// =============================================================================

func TestAuditFindings(t *testing.T) {
	t.Run("Point 1: Whole-Word OpContains and Candidate Filter", func(t *testing.T) {
		ruleUbe := &RuleGroup{
			ID: 1, Name: "Uber Substring Rule", Active: true,
			Conditions: []*RuleCondition{
				{ID: 101, Field: FieldVendor, Operator: OpContains, Value: "ube"},
			},
		}
		ruleUbe.Keywords = ruleUbe.DeriveKeywords()
		tx := Transaction{Vendor: "Uber Eats"}

		candidates := FilterCandidates(tx, []*RuleGroup{ruleUbe})
		assert.Len(t, candidates, 0)

		_, pass := ruleUbe.Conditions[0].evaluate(tx)
		assert.False(t, pass)

		ruleUber := &RuleGroup{
			ID: 2, Name: "Uber Word Rule", Active: true,
			Conditions: []*RuleCondition{
				{ID: 102, Field: FieldVendor, Operator: OpContains, Value: "Uber"},
			},
		}
		require.NoError(t, ruleUber.Compile())
		_, passUber := ruleUber.Conditions[0].evaluate(tx)
		assert.True(t, passUber)

		tx2 := Transaction{Vendor: "Visa Debit - SQ*UBER EATS"}
		_, passUber2 := ruleUber.Conditions[0].evaluate(tx2)
		assert.True(t, passUber2)

		ruleEat := &RuleGroup{
			ID: 3, Name: "Eat Substring Rule", Active: true,
			Conditions: []*RuleCondition{
				{ID: 103, Field: FieldVendor, Operator: OpContains, Value: "eat"},
			},
		}
		require.NoError(t, ruleEat.Compile())
		_, passEat := ruleEat.Conditions[0].evaluate(tx)
		assert.False(t, passEat)
	})

	t.Run("Point 2: Memo Field In Candidate Filter", func(t *testing.T) {
		rules := []*RuleGroup{{
			ID: 2, Name: "Memo Rule", Active: true,
			Conditions: []*RuleCondition{
				{ID: 201, Field: FieldMemo, Operator: OpContains, Value: "refund"},
			},
		}}
		rules[0].Keywords = rules[0].DeriveKeywords()
		tx := Transaction{Memo: "This is a refund"}
		candidates := FilterCandidates(tx, rules)
		assert.Len(t, candidates, 1)
	})

	t.Run("Point 3: Time Zone Desync", func(t *testing.T) {
		loc, _ := time.LoadLocation("EST")
		txDate := time.Date(2023, 1, 1, 0, 0, 0, 0, loc)
		rule := &RuleCondition{Field: FieldDate, Operator: OpEquals, Value: "2023-01-01"}
		require.NoError(t, rule.compile())
		tx := Transaction{Date: txDate}
		_, pass := rule.evaluate(tx)
		assert.True(t, pass)
	})

	t.Run("Point 4: OpIn on Numeric and Date Fields", func(t *testing.T) {
		ruleAmt := &RuleCondition{Field: FieldAmount, Operator: OpIn, Value: "[100.50, 50.00]"}
		require.NoError(t, ruleAmt.compile())
		_, passAmt := ruleAmt.evaluate(Transaction{Amount: 100.50})
		assert.True(t, passAmt)

		ruleDate := &RuleCondition{Field: FieldDate, Operator: OpIn, Value: `["2023-01-01", "2023-01-02"]`}
		require.NoError(t, ruleDate.compile())
		txDate := Transaction{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)}
		_, passDate := ruleDate.evaluate(txDate)
		assert.True(t, passDate)

		ruleNotIn := &RuleCondition{Field: FieldDate, Operator: OpNotIn, Value: `["2023-01-02", "2023-01-03"]`}
		require.NoError(t, ruleNotIn.compile())
		_, passNotIn := ruleNotIn.evaluate(txDate)
		assert.True(t, passNotIn)
	})
}

// =============================================================================
// Point 2 deep-dive: Memo field in candidate filter
// =============================================================================

// memoRule builds a compiled, keyword-indexed single-condition RuleGroup targeting FieldMemo.
func memoRule(t *testing.T, id int, op Operator, value string) *RuleGroup {
	t.Helper()
	g := &RuleGroup{
		ID: id, Name: "Memo Rule", Active: true,
		Conditions: []*RuleCondition{
			{ID: id*100 + 1, Field: FieldMemo, Operator: op, Value: value},
		},
	}
	require.NoError(t, g.Compile())
	g.Keywords = g.DeriveKeywords()
	return g
}

func TestMemoFieldCandidateFilter(t *testing.T) {
	// -------------------------------------------------------------------------
	// Filter layer — positive cases
	// -------------------------------------------------------------------------

	t.Run("memo token matches single-keyword rule", func(t *testing.T) {
		r := memoRule(t, 1, OpContains, "refund")
		tx := Transaction{Memo: "This is a refund"}
		assert.Len(t, FilterCandidates(tx, []*RuleGroup{r}), 1)
	})

	t.Run("memo token matches when vendor and description are empty", func(t *testing.T) {
		r := memoRule(t, 2, OpContains, "invoice")
		tx := Transaction{Memo: "invoice:123"}
		assert.Len(t, FilterCandidates(tx, []*RuleGroup{r}), 1)
	})

	t.Run("memo provides the only matching keyword among several rule keywords", func(t *testing.T) {
		// Rule has keywords from two fields; tx only carries the memo token.
		g := &RuleGroup{
			ID: 3, Name: "Mixed", Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldVendor, Operator: OpContains, Value: "uber"},
				{Field: FieldMemo, Operator: OpContains, Value: "reimbursement"},
			},
		}
		require.NoError(t, g.Compile())
		g.Keywords = g.DeriveKeywords()
		// Tx has only the memo token — keyword intersection still matches (any keyword match → candidate).
		tx := Transaction{Memo: "reimbursement approved"}
		assert.Len(t, FilterCandidates(tx, []*RuleGroup{g}), 1)
	})

	t.Run("memo token matches one keyword in a multi-keyword rule", func(t *testing.T) {
		g := &RuleGroup{
			ID: 4, Name: "Multi-kw", Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldMemo, Operator: OpIn, Value: `["refund", "chargeback", "reversal"]`},
			},
		}
		require.NoError(t, g.Compile())
		g.Keywords = g.DeriveKeywords()
		tx := Transaction{Memo: "chargeback processed"}
		assert.Len(t, FilterCandidates(tx, []*RuleGroup{g}), 1)
	})

	t.Run("memo token case-insensitive match", func(t *testing.T) {
		r := memoRule(t, 5, OpContains, "INVOICE")
		tx := Transaction{Memo: "invoice #999"}
		assert.Len(t, FilterCandidates(tx, []*RuleGroup{r}), 1)
	})

	t.Run("memo with special characters tokenized correctly", func(t *testing.T) {
		// "invoice:123" should yield tokens ["invoice","123"]
		r := memoRule(t, 6, OpContains, "invoice")
		tx := Transaction{Memo: "invoice:999"}
		assert.Len(t, FilterCandidates(tx, []*RuleGroup{r}), 1)
	})

	// -------------------------------------------------------------------------
	// Filter layer — negative / exclusion cases
	// -------------------------------------------------------------------------

	t.Run("rule memo keyword absent from empty tx memo", func(t *testing.T) {
		// Keywords = "refund"; tx has no memo — token not present anywhere → excluded.
		r := memoRule(t, 10, OpContains, "refund")
		tx := Transaction{Vendor: "AMAZON", Memo: ""}
		assert.Len(t, FilterCandidates(tx, []*RuleGroup{r}), 0)
	})

	t.Run("filter is field-agnostic; evaluate enforces field semantics", func(t *testing.T) {
		// The candidate filter checks keyword presence across ALL tx fields.
		// A vendor-keyword rule becomes a candidate when the keyword appears anywhere
		// in the transaction (including memo). Evaluate then correctly rejects it.
		g := &RuleGroup{
			ID: 11, Name: "Vendor Rule", Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldVendor, Operator: OpContains, Value: "refund"},
			},
		}
		require.NoError(t, g.Compile())
		g.Keywords = g.DeriveKeywords()
		tx := Transaction{Vendor: "AMAZON", Memo: "refund applied"}
		// Filter passes (keyword "refund" is in tx memo tokens) ...
		candidates := FilterCandidates(tx, []*RuleGroup{g})
		assert.Len(t, candidates, 1)
		// ... but Evaluate correctly rejects (vendor != "refund").
		matched, _ := candidates[0].Evaluate(tx)
		assert.False(t, matched)
	})

	t.Run("partial memo token not indexed (whole-word semantics)", func(t *testing.T) {
		// "ref" is not a full token of "refund" → not in tx token set → excluded.
		r := memoRule(t, 12, OpContains, "ref")
		tx := Transaction{Memo: "refund issued"}
		assert.Len(t, FilterCandidates(tx, []*RuleGroup{r}), 0)
	})

	t.Run("single-char memo value produces empty keywords (catch-all)", func(t *testing.T) {
		// DeriveKeywords drops tokens shorter than 2 chars → Keywords="" → catch-all.
		// The rule always enters Evaluate; Evaluate itself uses splitTokens (no length
		// filter) so it correctly matches the single-char token in the memo.
		r := memoRule(t, 13, OpContains, "a")
		assert.Empty(t, r.Keywords) // confirms catch-all
		tx := Transaction{Memo: "a payment"}
		candidates := FilterCandidates(tx, []*RuleGroup{r})
		assert.Len(t, candidates, 1) // catch-all always selected
		matched, _ := candidates[0].Evaluate(tx)
		assert.True(t, matched) // "a" is a full token in "a payment"
	})

	t.Run("inactive memo rule excluded regardless of memo match", func(t *testing.T) {
		g := &RuleGroup{
			ID: 14, Name: "Inactive", Active: false,
			Conditions: []*RuleCondition{
				{Field: FieldMemo, Operator: OpContains, Value: "refund"},
			},
		}
		g.Keywords = g.DeriveKeywords()
		tx := Transaction{Memo: "refund"}
		assert.Len(t, FilterCandidates(tx, []*RuleGroup{g}), 0)
	})

	// -------------------------------------------------------------------------
	// End-to-end pipeline: DeriveKeywords → FilterCandidates → Evaluate
	// -------------------------------------------------------------------------

	t.Run("OpContains memo: full pipeline pass", func(t *testing.T) {
		r := memoRule(t, 20, OpContains, "refund")
		tx := Transaction{Memo: "customer refund processed"}
		candidates := FilterCandidates(tx, []*RuleGroup{r})
		require.Len(t, candidates, 1)
		matched, exp := candidates[0].Evaluate(tx)
		assert.True(t, matched)
		assert.True(t, exp.FinalResult)
	})

	t.Run("OpContains memo: full pipeline miss (wrong word)", func(t *testing.T) {
		r := memoRule(t, 21, OpContains, "chargeback")
		tx := Transaction{Memo: "customer refund processed"}
		candidates := FilterCandidates(tx, []*RuleGroup{r})
		assert.Len(t, candidates, 0) // filtered before evaluate
	})

	t.Run("OpEquals memo: full pipeline pass", func(t *testing.T) {
		r := memoRule(t, 22, OpEquals, "refund")
		tx := Transaction{Memo: "refund"}
		candidates := FilterCandidates(tx, []*RuleGroup{r})
		require.Len(t, candidates, 1)
		matched, _ := candidates[0].Evaluate(tx)
		assert.True(t, matched)
	})

	t.Run("OpStartsWith memo: no keyword derived → catch-all; evaluate decides", func(t *testing.T) {
		// OpStartsWith does not derive keywords, so Keywords="", making it a catch-all.
		r := memoRule(t, 23, OpStartsWith, "invoice")
		tx := Transaction{Memo: "invoice #42"}
		candidates := FilterCandidates(tx, []*RuleGroup{r})
		require.Len(t, candidates, 1) // catch-all
		matched, _ := candidates[0].Evaluate(tx)
		assert.True(t, matched)

		txFail := Transaction{Memo: "payment #42"}
		matchedFail, _ := r.Evaluate(txFail)
		assert.False(t, matchedFail)
	})

	t.Run("OpIn memo: full pipeline with multiple allowed memos", func(t *testing.T) {
		g := &RuleGroup{
			ID: 24, Name: "Memo In", Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldMemo, Operator: OpIn, Value: `["refund", "reversal"]`},
			},
		}
		require.NoError(t, g.Compile())
		g.Keywords = g.DeriveKeywords()

		for _, memo := range []string{"refund", "reversal"} {
			tx := Transaction{Memo: memo}
			candidates := FilterCandidates(tx, []*RuleGroup{g})
			require.Len(t, candidates, 1, "memo=%s", memo)
			matched, _ := candidates[0].Evaluate(tx)
			assert.True(t, matched, "memo=%s", memo)
		}

		txFail := Transaction{Memo: "payment"}
		assert.Len(t, FilterCandidates(txFail, []*RuleGroup{g}), 0)
	})

	// -------------------------------------------------------------------------
	// AND logic mixing memo and other fields
	// -------------------------------------------------------------------------

	t.Run("AND rule: memo AND vendor — both present → match", func(t *testing.T) {
		g := &RuleGroup{
			ID: 30, Name: "AND Memo+Vendor", Logic: LogicAnd, Active: true,
			Conditions: []*RuleCondition{
				{ID: 301, Field: FieldVendor, Operator: OpContains, Value: "uber"},
				{ID: 302, Field: FieldMemo, Operator: OpContains, Value: "reimbursement"},
			},
		}
		require.NoError(t, g.Compile())
		g.Keywords = g.DeriveKeywords()
		tx := Transaction{Vendor: "UBER EATS", Memo: "reimbursement approved"}
		candidates := FilterCandidates(tx, []*RuleGroup{g})
		require.Len(t, candidates, 1)
		matched, _ := candidates[0].Evaluate(tx)
		assert.True(t, matched)
	})

	t.Run("AND rule: memo AND vendor — only vendor present → no match", func(t *testing.T) {
		g := &RuleGroup{
			ID: 31, Name: "AND Memo+Vendor Fail", Logic: LogicAnd, Active: true,
			Conditions: []*RuleCondition{
				{ID: 311, Field: FieldVendor, Operator: OpContains, Value: "uber"},
				{ID: 312, Field: FieldMemo, Operator: OpContains, Value: "reimbursement"},
			},
		}
		require.NoError(t, g.Compile())
		g.Keywords = g.DeriveKeywords()
		tx := Transaction{Vendor: "UBER EATS", Memo: "coffee run"}
		// "uber" is in tx vendor tokens → candidate selected; Evaluate rejects (memo condition fails).
		candidates := FilterCandidates(tx, []*RuleGroup{g})
		require.Len(t, candidates, 1)
		matched, _ := candidates[0].Evaluate(tx)
		assert.False(t, matched)
	})

	// -------------------------------------------------------------------------
	// Regression: memo token must NOT bleed into non-memo field evaluation
	// -------------------------------------------------------------------------

	t.Run("regression: filter is coarse; evaluate enforces field-level semantics", func(t *testing.T) {
		// The filter checks keyword presence across ALL tx fields (by design).
		// A vendor-keyword rule CAN become a candidate when the keyword appears in
		// the memo — but Evaluate correctly rejects it because the vendor field doesn't match.
		g := &RuleGroup{
			ID: 40, Name: "Vendor Only", Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldVendor, Operator: OpContains, Value: "refund"},
			},
		}
		require.NoError(t, g.Compile())
		g.Keywords = g.DeriveKeywords()
		tx := Transaction{Vendor: "AMAZON", Memo: "refund"}
		// "refund" is in tx token set (from memo) → filter passes.
		candidates := FilterCandidates(tx, []*RuleGroup{g})
		assert.Len(t, candidates, 1)
		// Evaluate checks vendor field only → correctly rejects.
		matched, _ := candidates[0].Evaluate(tx)
		assert.False(t, matched)
	})

	t.Run("regression: unrelated vendor-keyword rule not selected when vendor absent", func(t *testing.T) {
		// tx has no "starbucks" anywhere — vendor rule gets no candidate.
		vendorRule := &RuleGroup{
			ID: 50, Name: "Vendor Rule", Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldVendor, Operator: OpContains, Value: "starbucks"},
			},
		}
		require.NoError(t, vendorRule.Compile())
		vendorRule.Keywords = vendorRule.DeriveKeywords()

		memoR := memoRule(t, 51, OpContains, "refund")

		tx := Transaction{Vendor: "AMAZON", Memo: "refund applied"}
		candidates := FilterCandidates(tx, []*RuleGroup{vendorRule, memoR})
		require.Len(t, candidates, 1)
		assert.Equal(t, 51, candidates[0].ID)
	})
}

// =============================================================================
// Point 3 deep-dive: Time Zone Desync in Date Evaluations
// =============================================================================

// dateRule compiles a FieldDate condition with the given operator and value.
func dateRule(t *testing.T, op Operator, value string) *RuleCondition {
	t.Helper()
	c := &RuleCondition{Field: FieldDate, Operator: op, Value: value}
	require.NoError(t, c.compile())
	return c
}

// evalDate is a convenience wrapper for testing.
func evalDate(c *RuleCondition, d time.Time) bool {
	_, pass := c.evaluate(Transaction{Date: d})
	return pass
}

func TestDateTimezoneDesync(t *testing.T) {
	// Fixed-offset zones: no IANA tzdata dependency, deterministic in CI.
	est := time.FixedZone("EST", -5*3600)      // UTC-5
	ist := time.FixedZone("IST", 5*3600+30*60) // UTC+5:30
	aest := time.FixedZone("AEST", 10*3600)    // UTC+10
	lint := time.FixedZone("LINT", 14*3600)    // UTC+14 (Kiribati – furthest ahead)

	// -------------------------------------------------------------------------
	// compiledDate contract
	// -------------------------------------------------------------------------

	t.Run("compiledDate is UTC midnight", func(t *testing.T) {
		c := dateRule(t, OpEquals, "2023-01-01")
		want := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
		assert.True(t, c.compiledDate.Equal(want), "compiledDate %v != %v", c.compiledDate, want)
		assert.Equal(t, "UTC", c.compiledDate.Location().String())
	})

	// -------------------------------------------------------------------------
	// OpEquals — per-timezone boundary cases
	// -------------------------------------------------------------------------

	t.Run("OpEquals: UTC midnight matches", func(t *testing.T) {
		c := dateRule(t, OpEquals, "2023-01-01")
		assert.True(t, evalDate(c, time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)))
	})

	t.Run("OpEquals: EST midnight (05:00 UTC) still 2023-01-01 in UTC", func(t *testing.T) {
		// 2023-01-01 00:00 EST = 2023-01-01 05:00 UTC → calendar date 2023-01-01 ✓
		c := dateRule(t, OpEquals, "2023-01-01")
		assert.True(t, evalDate(c, time.Date(2023, 1, 1, 0, 0, 0, 0, est)))
	})

	t.Run("OpEquals: EST late evening flips to next UTC date, does not match local date", func(t *testing.T) {
		// 2023-01-01 23:00 EST = 2023-01-02 04:00 UTC → UTC date 2023-01-02
		c := dateRule(t, OpEquals, "2023-01-01")
		assert.False(t, evalDate(c, time.Date(2023, 1, 1, 23, 0, 0, 0, est)))

		cNext := dateRule(t, OpEquals, "2023-01-02")
		assert.True(t, evalDate(cNext, time.Date(2023, 1, 1, 23, 0, 0, 0, est)))
	})

	t.Run("OpEquals: IST early morning crosses to previous UTC date", func(t *testing.T) {
		// 2023-01-01 02:00 IST = 2022-12-31 20:30 UTC → UTC date 2022-12-31
		tx := time.Date(2023, 1, 1, 2, 0, 0, 0, ist)
		assert.True(t, evalDate(dateRule(t, OpEquals, "2022-12-31"), tx))
		assert.False(t, evalDate(dateRule(t, OpEquals, "2023-01-01"), tx))
	})

	t.Run("OpEquals: AEST morning maps to previous UTC day", func(t *testing.T) {
		// 2023-01-02 02:00 AEST = 2023-01-01 16:00 UTC → UTC date 2023-01-01 ✓
		tx := time.Date(2023, 1, 2, 2, 0, 0, 0, aest)
		assert.True(t, evalDate(dateRule(t, OpEquals, "2023-01-01"), tx))
		assert.False(t, evalDate(dateRule(t, OpEquals, "2023-01-02"), tx))
	})

	t.Run("OpEquals: LINT (UTC+14) spans ahead; UTC date still lags one day", func(t *testing.T) {
		// 2023-01-02 10:00 LINT = 2023-01-01 20:00 UTC → UTC date 2023-01-01 ✓
		tx := time.Date(2023, 1, 2, 10, 0, 0, 0, lint)
		assert.True(t, evalDate(dateRule(t, OpEquals, "2023-01-01"), tx))
		assert.False(t, evalDate(dateRule(t, OpEquals, "2023-01-02"), tx))
	})

	t.Run("OpEquals: IST noon stays on same UTC day", func(t *testing.T) {
		// 2023-06-15 12:00 IST = 2023-06-15 06:30 UTC → 2023-06-15 ✓
		tx := time.Date(2023, 6, 15, 12, 0, 0, 0, ist)
		assert.True(t, evalDate(dateRule(t, OpEquals, "2023-06-15"), tx))
	})

	// -------------------------------------------------------------------------
	// OpGt
	// -------------------------------------------------------------------------

	t.Run("OpGt: EST midnight on boundary date is not GT (equals same UTC date)", func(t *testing.T) {
		// 2023-01-01 00:00 EST = 2023-01-01 05:00 UTC → date 2023-01-01 → not GT 2023-01-01
		c := dateRule(t, OpGt, "2023-01-01")
		assert.False(t, evalDate(c, time.Date(2023, 1, 1, 0, 0, 0, 0, est)))
	})

	t.Run("OpGt: EST late night crosses to next UTC day, IS GT boundary date", func(t *testing.T) {
		// 2023-01-01 22:00 EST = 2023-01-02 03:00 UTC → date 2023-01-02 > 2023-01-01 ✓
		c := dateRule(t, OpGt, "2023-01-01")
		assert.True(t, evalDate(c, time.Date(2023, 1, 1, 22, 0, 0, 0, est)))
	})

	t.Run("OpGt: IST early morning UTC cross gives date before boundary", func(t *testing.T) {
		// 2023-01-01 03:00 IST = 2022-12-31 21:30 UTC → date 2022-12-31, not GT 2022-12-31
		c := dateRule(t, OpGt, "2022-12-31")
		assert.False(t, evalDate(c, time.Date(2023, 1, 1, 3, 0, 0, 0, ist)))
	})

	// -------------------------------------------------------------------------
	// OpGte
	// -------------------------------------------------------------------------

	t.Run("OpGte: EST midnight on boundary date matches (equal)", func(t *testing.T) {
		c := dateRule(t, OpGte, "2023-01-01")
		assert.True(t, evalDate(c, time.Date(2023, 1, 1, 0, 0, 0, 0, est)))
	})

	t.Run("OpGte: IST early morning maps to prior UTC day, matches gte prior day", func(t *testing.T) {
		// 2023-01-01 03:00 IST = 2022-12-31 21:30 UTC → date 2022-12-31 >= 2022-12-31 ✓
		c := dateRule(t, OpGte, "2022-12-31")
		assert.True(t, evalDate(c, time.Date(2023, 1, 1, 3, 0, 0, 0, ist)))
	})

	t.Run("OpGte: IST early morning does NOT satisfy gte 2023-01-01", func(t *testing.T) {
		// Same tx: UTC date is 2022-12-31, which is NOT >= 2023-01-01
		c := dateRule(t, OpGte, "2023-01-01")
		assert.False(t, evalDate(c, time.Date(2023, 1, 1, 3, 0, 0, 0, ist)))
	})

	// -------------------------------------------------------------------------
	// OpLt
	// -------------------------------------------------------------------------

	t.Run("OpLt: EST midnight is NOT Lt same day in UTC (they equal)", func(t *testing.T) {
		// 2023-01-01 00:00 EST = 2023-01-01 05:00 UTC → date 2023-01-01, not < 2023-01-01
		c := dateRule(t, OpLt, "2023-01-01")
		assert.False(t, evalDate(c, time.Date(2023, 1, 1, 0, 0, 0, 0, est)))
	})

	t.Run("OpLt: IST early morning (prev UTC day) is Lt target date", func(t *testing.T) {
		// 2023-01-01 03:00 IST = 2022-12-31 21:30 UTC → date 2022-12-31 < 2023-01-01 ✓
		c := dateRule(t, OpLt, "2023-01-01")
		assert.True(t, evalDate(c, time.Date(2023, 1, 1, 3, 0, 0, 0, ist)))
	})

	t.Run("OpLt: LINT ahead-zone still correctly uses UTC date", func(t *testing.T) {
		// 2023-01-01 22:00 LINT = 2023-01-01 08:00 UTC → date 2023-01-01, not < 2023-01-01
		c := dateRule(t, OpLt, "2023-01-01")
		assert.False(t, evalDate(c, time.Date(2023, 1, 1, 22, 0, 0, 0, lint)))
	})

	// -------------------------------------------------------------------------
	// OpLte
	// -------------------------------------------------------------------------

	t.Run("OpLte: EST late night crosses to next UTC day, NOT Lte today", func(t *testing.T) {
		// 2023-01-01 22:00 EST = 2023-01-02 03:00 UTC → date 2023-01-02, not <= 2023-01-01
		c := dateRule(t, OpLte, "2023-01-01")
		assert.False(t, evalDate(c, time.Date(2023, 1, 1, 22, 0, 0, 0, est)))
	})

	t.Run("OpLte: UTC end-of-day is still Lte that day", func(t *testing.T) {
		c := dateRule(t, OpLte, "2023-06-15")
		assert.True(t, evalDate(c, time.Date(2023, 6, 15, 23, 59, 59, 999999999, time.UTC)))
	})

	t.Run("OpLte: AEST morning (prev UTC day) satisfies lte prev day rule", func(t *testing.T) {
		// 2023-01-02 05:00 AEST = 2023-01-01 19:00 UTC → date 2023-01-01 <= 2023-01-01 ✓
		c := dateRule(t, OpLte, "2023-01-01")
		assert.True(t, evalDate(c, time.Date(2023, 1, 2, 5, 0, 0, 0, aest)))
	})

	// -------------------------------------------------------------------------
	// OpIn
	// -------------------------------------------------------------------------

	t.Run("OpIn: EST midnight resolves to correct UTC date, found in set", func(t *testing.T) {
		// 2023-01-01 00:00 EST = 2023-01-01 05:00 UTC → key "2023-01-01" ✓
		c := dateRule(t, OpIn, `["2023-01-01","2023-01-02"]`)
		assert.True(t, evalDate(c, time.Date(2023, 1, 1, 0, 0, 0, 0, est)))
	})

	t.Run("OpIn: EST late night flips to next UTC date; finds correct key in set", func(t *testing.T) {
		// 2023-01-01 22:00 EST = 2023-01-02 03:00 UTC → key "2023-01-02" ✓
		c := dateRule(t, OpIn, `["2023-01-01","2023-01-02"]`)
		assert.True(t, evalDate(c, time.Date(2023, 1, 1, 22, 0, 0, 0, est)))

		cWithout := dateRule(t, OpIn, `["2023-01-01"]`)
		assert.False(t, evalDate(cWithout, time.Date(2023, 1, 1, 22, 0, 0, 0, est)))
	})

	t.Run("OpIn: AEST morning (prev UTC day) found in set", func(t *testing.T) {
		// 2023-01-02 05:00 AEST = 2023-01-01 19:00 UTC → key "2023-01-01" ✓
		c := dateRule(t, OpIn, `["2023-01-01"]`)
		assert.True(t, evalDate(c, time.Date(2023, 1, 2, 5, 0, 0, 0, aest)))
	})

	// -------------------------------------------------------------------------
	// OpNotIn
	// -------------------------------------------------------------------------

	t.Run("OpNotIn: IST early morning resolves to prior UTC date, excluded from set", func(t *testing.T) {
		// 2023-01-01 03:00 IST = 2022-12-31 21:30 UTC → key "2022-12-31", not in ["2023-01-01"] ✓
		tx := time.Date(2023, 1, 1, 3, 0, 0, 0, ist)
		assert.True(t, evalDate(dateRule(t, OpNotIn, `["2023-01-01"]`), tx))
		assert.False(t, evalDate(dateRule(t, OpNotIn, `["2022-12-31"]`), tx))
	})

	t.Run("OpNotIn: EST late night (next UTC day) excluded from set of prior day", func(t *testing.T) {
		// 2023-01-01 22:00 EST = 2023-01-02 03:00 UTC → key "2023-01-02", not in ["2023-01-01"] ✓
		tx := time.Date(2023, 1, 1, 22, 0, 0, 0, est)
		assert.True(t, evalDate(dateRule(t, OpNotIn, `["2023-01-01"]`), tx))
	})

	// -------------------------------------------------------------------------
	// Same instant; different Location wrappers → same calendar date
	// -------------------------------------------------------------------------

	t.Run("same UTC instant in multiple zones yields identical UTC calendar date", func(t *testing.T) {
		// All represent the same point in time: 2023-06-15 10:00:00 UTC
		base := time.Date(2023, 6, 15, 10, 0, 0, 0, time.UTC)
		c := dateRule(t, OpEquals, "2023-06-15")
		for _, loc := range []*time.Location{time.UTC, est, ist, aest, lint} {
			assert.True(t, evalDate(c, base.In(loc)), "zone=%s", loc)
		}
	})

	t.Run("UTC instant near day-end: local zones differ in calendar date but UTC is consistent", func(t *testing.T) {
		// 2023-06-15 22:00:00 UTC
		// In AEST (UTC+10): local shows 2023-06-16 08:00, but UTC date = 2023-06-15
		// In EST  (UTC-5):  local shows 2023-06-15 17:00, UTC date = 2023-06-15
		instant := time.Date(2023, 6, 15, 22, 0, 0, 0, time.UTC)
		c := dateRule(t, OpEquals, "2023-06-15")
		assert.True(t, evalDate(c, instant.In(time.UTC)))
		assert.True(t, evalDate(c, instant.In(aest))) // local says Jun 16, UTC says Jun 15
		assert.True(t, evalDate(c, instant.In(est)))
	})

	// -------------------------------------------------------------------------
	// Sub-day precision stripped
	// -------------------------------------------------------------------------

	t.Run("nanosecond precision within a day does not affect date matching", func(t *testing.T) {
		c := dateRule(t, OpEquals, "2023-06-15")
		for _, ns := range []int{0, 1, 500000000, 999999999} {
			tx := time.Date(2023, 6, 15, 12, 30, 45, ns, time.UTC)
			assert.True(t, evalDate(c, tx), "ns=%d", ns)
		}
	})

	t.Run("seconds within a UTC day do not affect date matching", func(t *testing.T) {
		c := dateRule(t, OpEquals, "2023-06-15")
		for _, sec := range []int{0, 1, 43199, 43200, 86399} {
			tx := time.Unix(int64(time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC).Unix())+int64(sec), 0).UTC()
			assert.True(t, evalDate(c, tx), "sec=%d", sec)
		}
	})

	// -------------------------------------------------------------------------
	// Calendar edge cases
	// -------------------------------------------------------------------------

	t.Run("year boundary: EST Dec 31 late night flips to Jan 1 UTC (next year)", func(t *testing.T) {
		// 2022-12-31 22:00 EST = 2023-01-01 03:00 UTC → date 2023-01-01
		tx := time.Date(2022, 12, 31, 22, 0, 0, 0, est)
		assert.True(t, evalDate(dateRule(t, OpEquals, "2023-01-01"), tx))
		assert.False(t, evalDate(dateRule(t, OpEquals, "2022-12-31"), tx))
	})

	t.Run("leap day UTC: 2024-02-29 matches exactly", func(t *testing.T) {
		c := dateRule(t, OpEquals, "2024-02-29")
		assert.True(t, evalDate(c, time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)))
		assert.True(t, evalDate(c, time.Date(2024, 2, 29, 0, 0, 0, 0, est))) // 2024-02-29 05:00 UTC
	})

	t.Run("leap day EST midnight still within UTC leap day", func(t *testing.T) {
		// 2024-02-29 00:00 EST = 2024-02-29 05:00 UTC → still Feb 29 ✓
		c := dateRule(t, OpEquals, "2024-02-29")
		tx := time.Date(2024, 2, 29, 0, 0, 0, 0, est)
		assert.True(t, evalDate(c, tx))
	})

	// -------------------------------------------------------------------------
	// Full RuleGroup pipeline with timezone-bearing dates
	// -------------------------------------------------------------------------

	t.Run("AND rule date range: tax year 2022 with EST transactions", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Name: "Tax Year 2022", Logic: LogicAnd, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldDate, Operator: OpGte, Value: "2022-01-01"},
				{ID: 2, Field: FieldDate, Operator: OpLte, Value: "2022-12-31"},
			},
		}
		require.NoError(t, g.Compile())

		// Mid-year EST transaction — in range ✓
		txMid := Transaction{Date: time.Date(2022, 7, 4, 12, 0, 0, 0, est)}
		matched, exp := g.Evaluate(txMid)
		assert.True(t, matched)
		assert.True(t, exp.FinalResult)

		// 2022-12-31 22:00 EST = 2023-01-01 03:00 UTC → UTC date 2023-01-01, out of range ✗
		txLate := Transaction{Date: time.Date(2022, 12, 31, 22, 0, 0, 0, est)}
		matchedLate, _ := g.Evaluate(txLate)
		assert.False(t, matchedLate)

		// 2023-01-01 03:00 IST = 2022-12-31 21:30 UTC → UTC date 2022-12-31, still in range ✓
		txIST := Transaction{Date: time.Date(2023, 1, 1, 3, 0, 0, 0, ist)}
		matchedIST, _ := g.Evaluate(txIST)
		assert.True(t, matchedIST)
	})

	t.Run("OR rule: date in a set or amount over threshold; timezone date resolves correctly", func(t *testing.T) {
		g := &RuleGroup{
			ID: 2, Name: "Q1 Or Big Spend", Logic: LogicOr, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldDate, Operator: OpIn, Value: `["2023-01-01","2023-01-02","2023-01-03"]`},
				{ID: 2, Field: FieldAmount, Operator: OpGt, Value: "1000"},
			},
		}
		require.NoError(t, g.Compile())

		// 2023-01-01 22:00 EST = 2023-01-02 03:00 UTC → key "2023-01-02" in set ✓
		tx1 := Transaction{Date: time.Date(2023, 1, 1, 22, 0, 0, 0, est), Amount: 50}
		matched1, _ := g.Evaluate(tx1)
		assert.True(t, matched1)

		// 2023-01-04 00:00 UTC (outside set) but amount > 1000 ✓
		tx2 := Transaction{Date: time.Date(2023, 1, 4, 0, 0, 0, 0, time.UTC), Amount: 1500}
		matched2, _ := g.Evaluate(tx2)
		assert.True(t, matched2)

		// Outside set AND amount <= 1000 ✗
		tx3 := Transaction{Date: time.Date(2023, 1, 4, 0, 0, 0, 0, time.UTC), Amount: 50}
		matched3, _ := g.Evaluate(tx3)
		assert.False(t, matched3)
	})
}

// =============================================================================
// String field operators (all fields: vendor, description, category, memo)
// =============================================================================

func TestStringOperators(t *testing.T) {
	baseTx := Transaction{
		Vendor:      "AMAZON WEB SERVICES",
		Description: "Monthly AWS bill",
		Category:    "Software",
		Memo:        "Invoice #12345",
	}

	t.Run("OpEquals", func(t *testing.T) {
		c := &RuleCondition{Field: FieldVendor, Operator: OpEquals, Value: "amazon web services"}
		require.NoError(t, c.compile())
		_, ok := c.evaluate(baseTx)
		assert.True(t, ok)
		c.Value = "unknown"
		c.isCompiled = false
		require.NoError(t, c.compile())
		_, ok = c.evaluate(baseTx)
		assert.False(t, ok)
	})

	t.Run("OpEqualsCS", func(t *testing.T) {
		c := &RuleCondition{Field: FieldVendor, Operator: OpEqualsCS, Value: "AMAZON WEB SERVICES"}
		require.NoError(t, c.compile())
		_, ok := c.evaluate(baseTx)
		assert.True(t, ok)
		c.Value = "amazon web services"
		c.isCompiled = false
		require.NoError(t, c.compile())
		_, ok = c.evaluate(baseTx)
		assert.False(t, ok)
	})

	t.Run("OpContains whole-word", func(t *testing.T) {
		c := &RuleCondition{Field: FieldDescription, Operator: OpContains, Value: "aws"}
		require.NoError(t, c.compile())
		_, ok := c.evaluate(baseTx)
		assert.True(t, ok)
		c.Value = "bill" // token in "Monthly AWS bill"
		c.isCompiled = false
		require.NoError(t, c.compile())
		_, ok = c.evaluate(baseTx)
		assert.True(t, ok)
		c.Value = "mon" // not a full token
		c.isCompiled = false
		require.NoError(t, c.compile())
		_, ok = c.evaluate(baseTx)
		assert.False(t, ok)
	})

	t.Run("OpContainsCS", func(t *testing.T) {
		c := &RuleCondition{Field: FieldCategory, Operator: OpContainsCS, Value: "Software"}
		require.NoError(t, c.compile())
		_, ok := c.evaluate(baseTx)
		assert.True(t, ok)
		c.Value = "software"
		c.isCompiled = false
		require.NoError(t, c.compile())
		_, ok = c.evaluate(baseTx)
		assert.False(t, ok)
	})

	t.Run("OpStartsWith", func(t *testing.T) {
		c := &RuleCondition{Field: FieldMemo, Operator: OpStartsWith, Value: "invoice"}
		require.NoError(t, c.compile())
		_, ok := c.evaluate(baseTx)
		assert.True(t, ok)
		c.Value = "xyz"
		c.isCompiled = false
		require.NoError(t, c.compile())
		_, ok = c.evaluate(baseTx)
		assert.False(t, ok)
	})

	t.Run("OpEndsWith", func(t *testing.T) {
		c := &RuleCondition{Field: FieldMemo, Operator: OpEndsWith, Value: "12345"}
		require.NoError(t, c.compile())
		_, ok := c.evaluate(baseTx)
		assert.True(t, ok)
	})

	t.Run("OpIn", func(t *testing.T) {
		c := &RuleCondition{Field: FieldCategory, Operator: OpIn, Value: `["Software", "Hardware"]`}
		require.NoError(t, c.compile())
		_, ok := c.evaluate(baseTx)
		assert.True(t, ok)
		c.Value = `["Hardware", "Office"]`
		c.isCompiled = false
		require.NoError(t, c.compile())
		_, ok = c.evaluate(baseTx)
		assert.False(t, ok)
	})

	t.Run("OpNotIn", func(t *testing.T) {
		c := &RuleCondition{Field: FieldCategory, Operator: OpNotIn, Value: `["Hardware", "Office"]`}
		require.NoError(t, c.compile())
		_, ok := c.evaluate(baseTx)
		assert.True(t, ok)
		c.Value = `["Software"]`
		c.isCompiled = false
		require.NoError(t, c.compile())
		_, ok = c.evaluate(baseTx)
		assert.False(t, ok)
	})

	t.Run("OpIsNull", func(t *testing.T) {
		c := &RuleCondition{Field: FieldMemo, Operator: OpIsNull, Value: ""}
		require.NoError(t, c.compile())
		_, ok := c.evaluate(Transaction{})
		assert.True(t, ok)
		_, ok = c.evaluate(baseTx)
		assert.False(t, ok)
	})

	t.Run("OpIsNotNull", func(t *testing.T) {
		c := &RuleCondition{Field: FieldMemo, Operator: OpIsNotNull, Value: ""}
		require.NoError(t, c.compile())
		_, ok := c.evaluate(baseTx)
		assert.True(t, ok)
		_, ok = c.evaluate(Transaction{})
		assert.False(t, ok)
	})

	t.Run("OpRegex", func(t *testing.T) {
		c := &RuleCondition{Field: FieldMemo, Operator: OpRegex, Value: `#\d+`}
		require.NoError(t, c.compile())
		_, ok := c.evaluate(baseTx)
		assert.True(t, ok)
		c.Value = `^no-match`
		c.isCompiled = false
		require.NoError(t, c.compile())
		_, ok = c.evaluate(baseTx)
		assert.False(t, ok)
	})
}

func TestStringOperators_AllFields(t *testing.T) {
	// Ensure description and category are exercised (same code path as vendor/memo)
	tx := Transaction{Description: "NETFLIX.COM", Category: "Entertainment"}
	c := &RuleCondition{Field: FieldDescription, Operator: OpContains, Value: "netflix"}
	require.NoError(t, c.compile())
	_, ok := c.evaluate(tx)
	assert.True(t, ok)

	c2 := &RuleCondition{Field: FieldCategory, Operator: OpEquals, Value: "entertainment"}
	require.NoError(t, c2.compile())
	_, ok = c2.evaluate(tx)
	assert.True(t, ok)
}

// =============================================================================
// Numeric (amount) operators
// =============================================================================

func TestNumericOperators(t *testing.T) {
	t.Run("OpGt OpGte OpLt OpLte OpEquals", func(t *testing.T) {
		tx := Transaction{Amount: 100.0}
		for _, tc := range []struct {
			op    Operator
			value string
			want  bool
		}{
			{OpGt, "99", true},
			{OpGt, "100", false},
			{OpGte, "100", true},
			{OpGte, "101", false},
			{OpLt, "101", true},
			{OpLt, "100", false},
			{OpLte, "100", true},
			{OpLte, "99", false},
			{OpEquals, "100", true},
			{OpEquals, "100.00", true},
			{OpEquals, "99", false},
		} {
			c := &RuleCondition{Field: FieldAmount, Operator: tc.op, Value: tc.value}
			require.NoError(t, c.compile())
			_, got := c.evaluate(tx)
			assert.Equal(t, tc.want, got, "Amount 100 %s %s", tc.op, tc.value)
		}
	})

	t.Run("OpIn OpNotIn", func(t *testing.T) {
		cIn := &RuleCondition{Field: FieldAmount, Operator: OpIn, Value: "[10, 20, 30]"}
		require.NoError(t, cIn.compile())
		_, ok := cIn.evaluate(Transaction{Amount: 20})
		assert.True(t, ok)
		_, ok = cIn.evaluate(Transaction{Amount: 25})
		assert.False(t, ok)

		cNotIn := &RuleCondition{Field: FieldAmount, Operator: OpNotIn, Value: "[10, 20]"}
		require.NoError(t, cNotIn.compile())
		_, ok = cNotIn.evaluate(Transaction{Amount: 15})
		assert.True(t, ok)
		_, ok = cNotIn.evaluate(Transaction{Amount: 10})
		assert.False(t, ok)
	})
}

// =============================================================================
// Date operators
// =============================================================================

func TestDateOperators(t *testing.T) {
	utcDate := time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC)
	tx := Transaction{Date: utcDate}

	t.Run("OpGt OpGte OpLt OpLte OpEquals", func(t *testing.T) {
		for _, tc := range []struct {
			op    Operator
			value string
			want  bool
		}{
			{OpGt, "2023-06-14", true},
			{OpGt, "2023-06-15", false},
			{OpGte, "2023-06-15", true},
			{OpGte, "2023-06-16", false},
			{OpLt, "2023-06-16", true},
			{OpLt, "2023-06-15", false},
			{OpLte, "2023-06-15", true},
			{OpLte, "2023-06-14", false},
			{OpEquals, "2023-06-15", true},
			{OpEquals, "2023-06-16", false},
		} {
			c := &RuleCondition{Field: FieldDate, Operator: tc.op, Value: tc.value}
			require.NoError(t, c.compile())
			_, got := c.evaluate(tx)
			assert.Equal(t, tc.want, got, "Date 2023-06-15 %s %s", tc.op, tc.value)
		}
	})

	t.Run("OpIn OpNotIn", func(t *testing.T) {
		cIn := &RuleCondition{Field: FieldDate, Operator: OpIn, Value: `["2023-06-14", "2023-06-15", "2023-06-16"]`}
		require.NoError(t, cIn.compile())
		_, ok := cIn.evaluate(tx)
		assert.True(t, ok)

		cNotIn := &RuleCondition{Field: FieldDate, Operator: OpNotIn, Value: `["2023-06-14", "2023-06-16"]`}
		require.NoError(t, cNotIn.compile())
		_, ok = cNotIn.evaluate(tx)
		assert.True(t, ok)
		_, ok = cNotIn.evaluate(Transaction{Date: time.Date(2023, 6, 14, 0, 0, 0, 0, time.UTC)})
		assert.False(t, ok)
	})
}

// =============================================================================
// FilterCandidates (optimization layer)
// =============================================================================

func TestFilterCandidates(t *testing.T) {
	t.Run("inactive rule excluded", func(t *testing.T) {
		r := &RuleGroup{ID: 1, Active: false, Keywords: "foo"}
		tx := Transaction{Vendor: "foo"}
		candidates := FilterCandidates(tx, []*RuleGroup{r})
		assert.Len(t, candidates, 0)
	})

	t.Run("empty keywords is catch-all", func(t *testing.T) {
		r := &RuleGroup{ID: 1, Active: true, Keywords: ""}
		tx := Transaction{Vendor: "anything"}
		candidates := FilterCandidates(tx, []*RuleGroup{r})
		assert.Len(t, candidates, 1)
	})

	t.Run("keyword overlap includes rule", func(t *testing.T) {
		r := &RuleGroup{
			ID: 1, Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldVendor, Operator: OpContains, Value: "uber"},
			},
		}
		r.Keywords = r.DeriveKeywords()
		tx := Transaction{Vendor: "UBER EATS"}
		candidates := FilterCandidates(tx, []*RuleGroup{r})
		assert.Len(t, candidates, 1)
	})

	t.Run("no keyword overlap excludes rule", func(t *testing.T) {
		r := &RuleGroup{
			ID: 1, Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldVendor, Operator: OpContains, Value: "lyft"},
			},
		}
		r.Keywords = r.DeriveKeywords()
		tx := Transaction{Vendor: "UBER EATS"}
		candidates := FilterCandidates(tx, []*RuleGroup{r})
		assert.Len(t, candidates, 0)
	})

	t.Run("OpIn list derives keywords", func(t *testing.T) {
		r := &RuleGroup{
			ID: 1, Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldVendor, Operator: OpIn, Value: `["Uber", "Lyft"]`},
			},
		}
		r.Keywords = r.DeriveKeywords()
		assert.Contains(t, r.Keywords, "uber")
		assert.Contains(t, r.Keywords, "lyft")
		tx := Transaction{Vendor: "Lyft ride"}
		candidates := FilterCandidates(tx, []*RuleGroup{r})
		assert.Len(t, candidates, 1)
	})
}

// =============================================================================
// RuleGroup.Evaluate (AND/OR, nested, explanation)
// =============================================================================

func TestRuleGroupEvaluate(t *testing.T) {
	t.Run("AND all must pass", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Name: "AND Rule", Logic: LogicAnd, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldVendor, Operator: OpContains, Value: "AMAZON"},
				{ID: 2, Field: FieldAmount, Operator: OpGt, Value: "50"},
			},
		}
		require.NoError(t, g.Compile())
		tx := Transaction{Vendor: "AMAZON", Amount: 100}
		matched, exp := g.Evaluate(tx)
		assert.True(t, matched)
		assert.True(t, exp.FinalResult)
		assert.Len(t, exp.ConditionExp, 2)
		assert.True(t, exp.ConditionExp[0].Result)
		assert.True(t, exp.ConditionExp[1].Result)

		txFail := Transaction{Vendor: "AMAZON", Amount: 10}
		matched, _ = g.Evaluate(txFail)
		assert.False(t, matched)
	})

	t.Run("OR one must pass", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Name: "OR Rule", Logic: LogicOr, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldVendor, Operator: OpEquals, Value: "LYFT"},
				{ID: 2, Field: FieldVendor, Operator: OpEquals, Value: "UBER"},
			},
		}
		require.NoError(t, g.Compile())
		tx := Transaction{Vendor: "UBER"}
		matched, exp := g.Evaluate(tx)
		assert.True(t, matched)
		assert.True(t, exp.FinalResult)

		txFail := Transaction{Vendor: "OTHER"}
		matched, _ = g.Evaluate(txFail)
		assert.False(t, matched)
	})

	t.Run("inactive child skipped", func(t *testing.T) {
		child := &RuleGroup{ID: 2, Name: "Child", Active: false, Conditions: []*RuleCondition{
			{Field: FieldVendor, Operator: OpEquals, Value: "x"},
		}}
		parent := &RuleGroup{
			ID: 1, Name: "Parent", Logic: LogicOr, Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldVendor, Operator: OpEquals, Value: "ok"},
			},
			Children: []*RuleGroup{child},
		}
		child.Parent = parent
		require.NoError(t, parent.Compile())
		tx := Transaction{Vendor: "ok"}
		matched, exp := parent.Evaluate(tx)
		assert.True(t, matched)
		// Inactive children are not evaluated, so they do not appear in ChildrenExp.
		assert.Len(t, exp.ChildrenExp, 0)
	})

	t.Run("active child in ChildrenExp", func(t *testing.T) {
		child := &RuleGroup{ID: 2, Name: "Child", Active: true, Logic: LogicAnd,
			Conditions: []*RuleCondition{
				{Field: FieldVendor, Operator: OpEquals, Value: "child-match"},
			},
		}
		parent := &RuleGroup{
			ID: 1, Name: "Parent", Logic: LogicOr, Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldVendor, Operator: OpEquals, Value: "parent-match"},
			},
			Children: []*RuleGroup{child},
		}
		child.Parent = parent
		require.NoError(t, parent.Compile())
		tx := Transaction{Vendor: "child-match"}
		matched, exp := parent.Evaluate(tx)
		assert.True(t, matched)
		require.Len(t, exp.ChildrenExp, 1)
		assert.True(t, exp.ChildrenExp[0].FinalResult)
		assert.Equal(t, "Child", exp.ChildrenExp[0].GroupName)
	})

	t.Run("HumanReadableReason when matched", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Name: "Software", Logic: LogicAnd, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldDescription, Operator: OpContains, Value: "aws"},
				{ID: 2, Field: FieldAmount, Operator: OpGt, Value: "50"},
			},
		}
		require.NoError(t, g.Compile())
		tx := Transaction{Description: "AWS bill", Amount: 100}
		_, exp := g.Evaluate(tx)
		reason := exp.HumanReadableReason()
		assert.Contains(t, reason, "Software")
		assert.Contains(t, reason, "description")
		assert.Contains(t, reason, "amount")
	})
}

// =============================================================================
// Validate and Compile (errors)
// =============================================================================

func TestValidateAndCompile(t *testing.T) {
	t.Run("invalid regex fails compile", func(t *testing.T) {
		c := &RuleCondition{Field: FieldVendor, Operator: OpRegex, Value: "["}
		err := c.compile()
		require.Error(t, err)
	})

	t.Run("invalid OpIn JSON fails compile", func(t *testing.T) {
		c := &RuleCondition{Field: FieldVendor, Operator: OpIn, Value: "not json"}
		err := c.compile()
		require.Error(t, err)
	})

	t.Run("circular dependency fails Validate", func(t *testing.T) {
		g := &RuleGroup{ID: 1, Name: "Self"}
		g.Children = []*RuleGroup{g}
		g.Parent = g
		err := g.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "circular")
	})

	t.Run("valid rule compiles", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Name: "Valid", Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldVendor, Operator: OpEquals, Value: "x"},
				{Field: FieldAmount, Operator: OpGt, Value: "0"},
			},
		}
		require.NoError(t, g.Validate())
		require.NoError(t, g.Compile())
	})
}

// =============================================================================
// Point 4 deep-dive: OpIn / OpNotIn on Numeric and Date Fields
// =============================================================================

// numericInRule compiles a FieldAmount condition with the given operator and JSON list value.
func numericInRule(t *testing.T, op Operator, jsonValue string) *RuleCondition {
	t.Helper()
	c := &RuleCondition{Field: FieldAmount, Operator: op, Value: jsonValue}
	require.NoError(t, c.compile())
	return c
}

// evalAmount is a convenience wrapper for testing numeric evaluation.
func evalAmount(c *RuleCondition, amount float64) bool {
	_, pass := c.evaluate(Transaction{Amount: amount})
	return pass
}

func TestOpInNumericAmount(t *testing.T) {
	// -------------------------------------------------------------------------
	// OpIn — positive cases
	// -------------------------------------------------------------------------

	t.Run("integer in set matches", func(t *testing.T) {
		c := numericInRule(t, OpIn, "[10, 20, 30]")
		assert.True(t, evalAmount(c, 20))
	})

	t.Run("all members of set each match individually", func(t *testing.T) {
		c := numericInRule(t, OpIn, "[10, 20, 30]")
		for _, v := range []float64{10, 20, 30} {
			assert.True(t, evalAmount(c, v), "amount=%g", v)
		}
	})

	t.Run("float with trailing zero matches (100.50 == 100.5)", func(t *testing.T) {
		// JSON 100.5 and transaction 100.50 are the same float64; %g strips trailing zero.
		c := numericInRule(t, OpIn, "[100.50, 50.00]")
		assert.True(t, evalAmount(c, 100.50))
		assert.True(t, evalAmount(c, 50.00))
	})

	t.Run("large float / scientific-notation value matches", func(t *testing.T) {
		// 1e6 = 1000000; fmt.Sprint and %g both produce "1e+06".
		c := numericInRule(t, OpIn, "[1000000, 2000000]")
		assert.True(t, evalAmount(c, 1_000_000))
		assert.True(t, evalAmount(c, 2_000_000))
	})

	t.Run("negative amount in set matches", func(t *testing.T) {
		c := numericInRule(t, OpIn, "[-50.00, -100.00]")
		assert.True(t, evalAmount(c, -50))
		assert.True(t, evalAmount(c, -100))
	})

	t.Run("zero amount in set matches", func(t *testing.T) {
		c := numericInRule(t, OpIn, "[0, 10]")
		assert.True(t, evalAmount(c, 0))
	})

	t.Run("single-element set matches its sole member", func(t *testing.T) {
		c := numericInRule(t, OpIn, "[42.99]")
		assert.True(t, evalAmount(c, 42.99))
	})

	// -------------------------------------------------------------------------
	// OpIn — negative cases
	// -------------------------------------------------------------------------

	t.Run("value not in set returns false", func(t *testing.T) {
		c := numericInRule(t, OpIn, "[10, 20, 30]")
		assert.False(t, evalAmount(c, 25))
	})

	t.Run("close-but-not-equal float not in set returns false", func(t *testing.T) {
		// 100.501 formats as "100.501" under %g, distinct from "100.5".
		c := numericInRule(t, OpIn, "[100.5]")
		assert.False(t, evalAmount(c, 100.501))
	})

	t.Run("zero not in non-zero set", func(t *testing.T) {
		c := numericInRule(t, OpIn, "[10, 20]")
		assert.False(t, evalAmount(c, 0))
	})

	t.Run("negative not in positive set", func(t *testing.T) {
		c := numericInRule(t, OpIn, "[50, 100]")
		assert.False(t, evalAmount(c, -50))
	})

	// -------------------------------------------------------------------------
	// OpNotIn — positive / negative cases
	// -------------------------------------------------------------------------

	t.Run("OpNotIn: value absent from set → true", func(t *testing.T) {
		c := numericInRule(t, OpNotIn, "[10, 20]")
		assert.True(t, evalAmount(c, 15))
		assert.True(t, evalAmount(c, 0))
		assert.True(t, evalAmount(c, -5))
	})

	t.Run("OpNotIn: value present in set → false", func(t *testing.T) {
		c := numericInRule(t, OpNotIn, "[10, 20, 30]")
		for _, v := range []float64{10, 20, 30} {
			assert.False(t, evalAmount(c, v), "amount=%g should be in set", v)
		}
	})

	t.Run("OpNotIn: trailing-zero float present in set → false", func(t *testing.T) {
		c := numericInRule(t, OpNotIn, "[100.50]")
		assert.False(t, evalAmount(c, 100.5))
	})

	t.Run("OpNotIn: large set — spot checks", func(t *testing.T) {
		c := numericInRule(t, OpNotIn, "[1,2,3,4,5,6,7,8,9,10]")
		assert.False(t, evalAmount(c, 5))
		assert.True(t, evalAmount(c, 11))
	})

	// -------------------------------------------------------------------------
	// Compile validation
	// -------------------------------------------------------------------------

	t.Run("invalid JSON for OpIn amount fails compile", func(t *testing.T) {
		c := &RuleCondition{Field: FieldAmount, Operator: OpIn, Value: "not-json"}
		require.Error(t, c.compile())
	})

	t.Run("compiledNumericSet populated for numeric OpIn", func(t *testing.T) {
		c := &RuleCondition{Field: FieldAmount, Operator: OpIn, Value: "[10, 20.5]"}
		require.NoError(t, c.compile())
		require.NotNil(t, c.compiledNumericSet)
		require.Len(t, c.compiledNumericSet, 2)
		assert.InDelta(t, 10.0, c.compiledNumericSet[0], 1e-9)
		assert.InDelta(t, 20.5, c.compiledNumericSet[1], 1e-9)
	})

	// -------------------------------------------------------------------------
	// End-to-end: full RuleGroup pipeline
	// -------------------------------------------------------------------------

	t.Run("AND rule: amount in set AND vendor matches", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Name: "Amount In + Vendor", Logic: LogicAnd, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldVendor, Operator: OpContains, Value: "amazon"},
				{ID: 2, Field: FieldAmount, Operator: OpIn, Value: "[50, 100, 200]"},
			},
		}
		require.NoError(t, g.Compile())

		txPass := Transaction{Vendor: "AMAZON", Amount: 100}
		matched, exp := g.Evaluate(txPass)
		assert.True(t, matched)
		assert.True(t, exp.ConditionExp[1].Result)

		txFailAmt := Transaction{Vendor: "AMAZON", Amount: 75}
		matchedFail, _ := g.Evaluate(txFailAmt)
		assert.False(t, matchedFail)
	})

	t.Run("OR rule: amount in set OR vendor matches — amount branch fires", func(t *testing.T) {
		g := &RuleGroup{
			ID: 2, Name: "Amount In OR Vendor", Logic: LogicOr, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldVendor, Operator: OpEquals, Value: "lyft"},
				{ID: 2, Field: FieldAmount, Operator: OpIn, Value: "[99.99]"},
			},
		}
		require.NoError(t, g.Compile())
		tx := Transaction{Vendor: "UBER", Amount: 99.99}
		matched, _ := g.Evaluate(tx)
		assert.True(t, matched)
	})

	t.Run("Amount NOT IN list: transaction excluded when amount is in the deny-list", func(t *testing.T) {
		g := &RuleGroup{
			ID: 3, Name: "Not Internal", Logic: LogicAnd, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldAmount, Operator: OpNotIn, Value: "[0, 0.01]"},
			},
		}
		require.NoError(t, g.Compile())
		assert.False(t, func() bool { m, _ := g.Evaluate(Transaction{Amount: 0}); return m }())
		assert.True(t, func() bool { m, _ := g.Evaluate(Transaction{Amount: 50}); return m }())
	})
}

func TestOpInDate(t *testing.T) {
	est := time.FixedZone("EST", -5*3600)
	ist := time.FixedZone("IST", 5*3600+30*60)
	aest := time.FixedZone("AEST", 10*3600)

	// -------------------------------------------------------------------------
	// Compile validation
	// -------------------------------------------------------------------------

	t.Run("invalid JSON for OpIn date fails compile", func(t *testing.T) {
		c := &RuleCondition{Field: FieldDate, Operator: OpIn, Value: "not-json"}
		require.Error(t, c.compile())
	})

	t.Run("compiledSet populated with YYYY-MM-DD keys for date OpIn", func(t *testing.T) {
		c := &RuleCondition{Field: FieldDate, Operator: OpIn, Value: `["2023-01-01","2023-06-15"]`}
		require.NoError(t, c.compile())
		require.NotNil(t, c.compiledSet)
		_, has0101 := c.compiledSet["2023-01-01"]
		_, has0615 := c.compiledSet["2023-06-15"]
		assert.True(t, has0101)
		assert.True(t, has0615)
	})

	// -------------------------------------------------------------------------
	// OpIn — UTC positive cases
	// -------------------------------------------------------------------------

	t.Run("UTC date in set matches", func(t *testing.T) {
		c := dateRule(t, OpIn, `["2023-06-14","2023-06-15","2023-06-16"]`)
		assert.True(t, evalDate(c, time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC)))
	})

	t.Run("all members of date set each match individually", func(t *testing.T) {
		dates := []string{"2023-01-01", "2023-06-15", "2023-12-31"}
		jsonList := `["2023-01-01","2023-06-15","2023-12-31"]`
		c := dateRule(t, OpIn, jsonList)
		for _, ds := range dates {
			parsed, _ := time.Parse("2006-01-02", ds)
			assert.True(t, evalDate(c, parsed), "date=%s", ds)
		}
	})

	t.Run("single-element date set matches its sole member", func(t *testing.T) {
		c := dateRule(t, OpIn, `["2023-03-15"]`)
		assert.True(t, evalDate(c, time.Date(2023, 3, 15, 0, 0, 0, 0, time.UTC)))
	})

	// -------------------------------------------------------------------------
	// OpIn — UTC negative cases
	// -------------------------------------------------------------------------

	t.Run("date not in set returns false", func(t *testing.T) {
		c := dateRule(t, OpIn, `["2023-06-14","2023-06-16"]`)
		assert.False(t, evalDate(c, time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC)))
	})

	t.Run("adjacent dates not in set both return false", func(t *testing.T) {
		c := dateRule(t, OpIn, `["2023-06-15"]`)
		assert.False(t, evalDate(c, time.Date(2023, 6, 14, 0, 0, 0, 0, time.UTC)))
		assert.False(t, evalDate(c, time.Date(2023, 6, 16, 0, 0, 0, 0, time.UTC)))
	})

	// -------------------------------------------------------------------------
	// OpIn — timezone UTC-normalization
	// -------------------------------------------------------------------------

	t.Run("EST midnight resolves to correct UTC date and matches set", func(t *testing.T) {
		// 2023-01-01 00:00 EST = 2023-01-01 05:00 UTC → key "2023-01-01" ✓
		c := dateRule(t, OpIn, `["2023-01-01","2023-01-02"]`)
		assert.True(t, evalDate(c, time.Date(2023, 1, 1, 0, 0, 0, 0, est)))
	})

	t.Run("EST late night flips to next UTC date — matches correct key", func(t *testing.T) {
		// 2023-01-01 22:00 EST = 2023-01-02 03:00 UTC → key "2023-01-02" ✓
		c := dateRule(t, OpIn, `["2023-01-01","2023-01-02"]`)
		assert.True(t, evalDate(c, time.Date(2023, 1, 1, 22, 0, 0, 0, est)))
		// If set only contains the local date "2023-01-01", should NOT match.
		cStrict := dateRule(t, OpIn, `["2023-01-01"]`)
		assert.False(t, evalDate(cStrict, time.Date(2023, 1, 1, 22, 0, 0, 0, est)))
	})

	t.Run("IST early morning crosses to previous UTC day, matches prior date in set", func(t *testing.T) {
		// 2023-01-01 02:00 IST = 2022-12-31 20:30 UTC → key "2022-12-31"
		c := dateRule(t, OpIn, `["2022-12-31"]`)
		assert.True(t, evalDate(c, time.Date(2023, 1, 1, 2, 0, 0, 0, ist)))

		cWrong := dateRule(t, OpIn, `["2023-01-01"]`)
		assert.False(t, evalDate(cWrong, time.Date(2023, 1, 1, 2, 0, 0, 0, ist)))
	})

	t.Run("AEST morning maps to previous UTC day, matches prior date in set", func(t *testing.T) {
		// 2023-01-02 05:00 AEST = 2023-01-01 19:00 UTC → key "2023-01-01"
		c := dateRule(t, OpIn, `["2023-01-01"]`)
		assert.True(t, evalDate(c, time.Date(2023, 1, 2, 5, 0, 0, 0, aest)))
	})

	t.Run("same UTC instant in multiple Location wrappers matches same set key", func(t *testing.T) {
		// All represent 2023-06-15 10:00:00 UTC
		base := time.Date(2023, 6, 15, 10, 0, 0, 0, time.UTC)
		c := dateRule(t, OpIn, `["2023-06-15"]`)
		for _, loc := range []*time.Location{time.UTC, est, ist, aest} {
			assert.True(t, evalDate(c, base.In(loc)), "zone=%s", loc)
		}
	})

	// -------------------------------------------------------------------------
	// OpIn — sub-day precision stripped
	// -------------------------------------------------------------------------

	t.Run("nanosecond precision within UTC day does not affect date set lookup", func(t *testing.T) {
		c := dateRule(t, OpIn, `["2023-06-15"]`)
		for _, ns := range []int{0, 1, 500_000_000, 999_999_999} {
			tx := time.Date(2023, 6, 15, 14, 30, 0, ns, time.UTC)
			assert.True(t, evalDate(c, tx), "ns=%d", ns)
		}
	})

	// -------------------------------------------------------------------------
	// OpIn — calendar edge cases
	// -------------------------------------------------------------------------

	t.Run("leap day 2024-02-29 in set matches", func(t *testing.T) {
		c := dateRule(t, OpIn, `["2024-02-29"]`)
		assert.True(t, evalDate(c, time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)))
		assert.True(t, evalDate(c, time.Date(2024, 2, 29, 0, 0, 0, 0, est))) // 2024-02-29 05:00 UTC
	})

	t.Run("year boundary: EST Dec 31 late night flips to next-year key", func(t *testing.T) {
		// 2022-12-31 22:00 EST = 2023-01-01 03:00 UTC → key "2023-01-01"
		c := dateRule(t, OpIn, `["2023-01-01"]`)
		assert.True(t, evalDate(c, time.Date(2022, 12, 31, 22, 0, 0, 0, est)))

		cOld := dateRule(t, OpIn, `["2022-12-31"]`)
		assert.False(t, evalDate(cOld, time.Date(2022, 12, 31, 22, 0, 0, 0, est)))
	})

	// -------------------------------------------------------------------------
	// OpNotIn — positive / negative cases
	// -------------------------------------------------------------------------

	t.Run("OpNotIn: UTC date absent from set → true", func(t *testing.T) {
		c := dateRule(t, OpNotIn, `["2023-06-14","2023-06-16"]`)
		assert.True(t, evalDate(c, time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC)))
	})

	t.Run("OpNotIn: UTC date present in set → false", func(t *testing.T) {
		c := dateRule(t, OpNotIn, `["2023-06-15"]`)
		assert.False(t, evalDate(c, time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC)))
	})

	t.Run("OpNotIn: IST early morning resolves to prior UTC day, excluded from next-day set", func(t *testing.T) {
		// 2023-01-01 03:00 IST = 2022-12-31 21:30 UTC → key "2022-12-31", not in ["2023-01-01"] → true
		tx := time.Date(2023, 1, 1, 3, 0, 0, 0, ist)
		assert.True(t, evalDate(dateRule(t, OpNotIn, `["2023-01-01"]`), tx))
		// But it IS "2022-12-31" → not-in set containing that key → false
		assert.False(t, evalDate(dateRule(t, OpNotIn, `["2022-12-31"]`), tx))
	})

	t.Run("OpNotIn: EST late night (next UTC day) excluded from prior-day set", func(t *testing.T) {
		// 2023-01-01 22:00 EST = 2023-01-02 03:00 UTC → key "2023-01-02", not in ["2023-01-01"] → true
		tx := time.Date(2023, 1, 1, 22, 0, 0, 0, est)
		assert.True(t, evalDate(dateRule(t, OpNotIn, `["2023-01-01"]`), tx))
		assert.False(t, evalDate(dateRule(t, OpNotIn, `["2023-01-02"]`), tx))
	})

	t.Run("OpNotIn: all members present → each returns false", func(t *testing.T) {
		dates := []struct {
			key string
			ts  time.Time
		}{
			{"2023-01-01", time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)},
			{"2023-06-15", time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC)},
			{"2023-12-31", time.Date(2023, 12, 31, 0, 0, 0, 0, time.UTC)},
		}
		c := dateRule(t, OpNotIn, `["2023-01-01","2023-06-15","2023-12-31"]`)
		for _, d := range dates {
			assert.False(t, evalDate(c, d.ts), "date=%s should be in set", d.key)
		}
	})

	// -------------------------------------------------------------------------
	// End-to-end: full RuleGroup pipeline
	// -------------------------------------------------------------------------

	t.Run("AND rule date range + OpIn set: quarter-start audit", func(t *testing.T) {
		g := &RuleGroup{
			ID: 10, Name: "Q1 First Day Audit", Logic: LogicAnd, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldDate, Operator: OpIn, Value: `["2023-01-01","2023-04-01","2023-07-01","2023-10-01"]`},
				{ID: 2, Field: FieldAmount, Operator: OpGt, Value: "0"},
			},
		}
		require.NoError(t, g.Compile())

		// Q1 start + positive amount ✓
		txQ1 := Transaction{Date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), Amount: 500}
		matched, _ := g.Evaluate(txQ1)
		assert.True(t, matched)

		// Mid-quarter — date not in set ✗
		txMid := Transaction{Date: time.Date(2023, 2, 15, 0, 0, 0, 0, time.UTC), Amount: 500}
		matchedMid, _ := g.Evaluate(txMid)
		assert.False(t, matchedMid)
	})

	t.Run("OR rule: date OpIn OR amount OpIn", func(t *testing.T) {
		g := &RuleGroup{
			ID: 11, Name: "Special Days Or Amounts", Logic: LogicOr, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldDate, Operator: OpIn, Value: `["2023-12-25","2023-01-01"]`},
				{ID: 2, Field: FieldAmount, Operator: OpIn, Value: "[99.99, 49.99]"},
			},
		}
		require.NoError(t, g.Compile())

		// Matches date ✓
		txDate := Transaction{Date: time.Date(2023, 12, 25, 0, 0, 0, 0, time.UTC), Amount: 1}
		matched, _ := g.Evaluate(txDate)
		assert.True(t, matched)

		// Matches amount ✓
		txAmt := Transaction{Date: time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC), Amount: 99.99}
		matchedAmt, _ := g.Evaluate(txAmt)
		assert.True(t, matchedAmt)

		// Matches neither ✗
		txNone := Transaction{Date: time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC), Amount: 1}
		matchedNone, _ := g.Evaluate(txNone)
		assert.False(t, matchedNone)
	})

	t.Run("date OpNotIn used as exclusion guard in AND rule", func(t *testing.T) {
		// Rule: amount > 0 AND date NOT IN blackout dates
		g := &RuleGroup{
			ID: 12, Name: "Not Blackout", Logic: LogicAnd, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldAmount, Operator: OpGt, Value: "0"},
				{ID: 2, Field: FieldDate, Operator: OpNotIn, Value: `["2023-12-25","2023-01-01"]`},
			},
		}
		require.NoError(t, g.Compile())

		txNormal := Transaction{Date: time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC), Amount: 100}
		matched, _ := g.Evaluate(txNormal)
		assert.True(t, matched)

		txBlackout := Transaction{Date: time.Date(2023, 12, 25, 0, 0, 0, 0, time.UTC), Amount: 100}
		matchedBlackout, _ := g.Evaluate(txBlackout)
		assert.False(t, matchedBlackout)
	})
}

// =============================================================================
// Point 5 deep-dive: Float64 Precision & Audit Trail Mismatch
// =============================================================================

// numericRule compiles a FieldAmount condition.
func numericRule(t *testing.T, op Operator, value string) *RuleCondition {
	t.Helper()
	c := &RuleCondition{Field: FieldAmount, Operator: op, Value: value}
	require.NoError(t, c.compile())
	return c
}

// evalAmountFull returns (txValue, pass) for the given amount.
func evalAmountFull(c *RuleCondition, amount float64) (string, bool) {
	return c.evaluate(Transaction{Amount: amount})
}

func TestFloat64PrecisionAuditTrail(t *testing.T) {
	// -------------------------------------------------------------------------
	// Core audit finding: TxValue must not be truncated/rounded in audit log
	// -------------------------------------------------------------------------

	t.Run("sub-cent amount: TxValue preserves full precision, not rounded to 2dp", func(t *testing.T) {
		// 100.005 formatted with %.2f → "100.01" (rounds up half-even or up).
		// The auditor would see "100.01" but the rule evaluated against 100.005.
		// Fixed: use %g → "100.005".
		c := numericRule(t, OpGt, "100")
		txVal, _ := evalAmountFull(c, 100.005)
		assert.Equal(t, "100.005", txVal, "TxValue must reflect the exact amount used in evaluation")
	})

	t.Run("sub-cent amount: old %.2f discards sub-cent precision (regression guard)", func(t *testing.T) {
		// 100.005 in IEEE-754 float64 is actually 100.004999... so %.2f → "100.00",
		// silently dropping the 3rd decimal. %g preserves it as "100.005".
		old := func(amount float64) string { return fmt.Sprintf("%.2f", amount) }
		newFmt := func(amount float64) string { return fmt.Sprintf("%g", amount) }
		assert.Equal(t, "100", old(100.005)[:3])          // "100.00" starts with "100"
		assert.NotEqual(t, old(100.005), newFmt(100.005)) // key: they differ
		assert.Equal(t, "100.005", newFmt(100.005))
	})

	t.Run("ConditionResult TxValue equals %g of transaction amount", func(t *testing.T) {
		amounts := []float64{100.005, 0.1 + 0.2, 99.9999, 1234567.89, 0.001}
		for _, amt := range amounts {
			c := numericRule(t, OpGt, "0")
			txVal, _ := evalAmountFull(c, amt)
			assert.Equal(t, fmt.Sprintf("%g", amt), txVal, "amount=%v", amt)
		}
	})

	t.Run("integer amount: TxValue is clean integer representation", func(t *testing.T) {
		// %g strips trailing zeros: 100.00 → "100", keeping audit logs clean.
		c := numericRule(t, OpEquals, "100")
		txVal, pass := evalAmountFull(c, 100.00)
		assert.Equal(t, "100", txVal)
		assert.True(t, pass)
	})

	t.Run("audit trail consistency: TxValue and evaluation agree on 100.005 vs 100.00 threshold", func(t *testing.T) {
		// Evaluation: 100.005 > 100.00 → true.
		// Audit log must show 100.005, not 100.01 (which would imply a different rule fired).
		c := numericRule(t, OpGt, "100.00")
		txVal, pass := evalAmountFull(c, 100.005)
		assert.True(t, pass)
		// The logged TxValue must agree with what actually crossed the threshold.
		threshold, _ := strconv.ParseFloat("100.00", 64)
		loggedAmt, _ := strconv.ParseFloat(txVal, 64)
		assert.True(t, loggedAmt > threshold,
			"auditor must be able to re-derive the evaluation result from TxValue alone; got TxValue=%s", txVal)
	})

	t.Run("audit trail mismatch scenario: %.2f rounding crosses threshold boundary", func(t *testing.T) {
		// Amount 100.004 with rule OpGt 100.003:
		// - evaluation: 100.004 > 100.003 → TRUE
		// - %.2f audit: "100.00" → auditor sees 100.00 > 100.003 → FALSE (mismatch!)
		// - %g audit:   "100.004" → auditor sees 100.004 > 100.003 → TRUE (correct)
		c := numericRule(t, OpGt, "100.003")
		txVal, pass := evalAmountFull(c, 100.004)
		assert.True(t, pass)
		loggedAmt, _ := strconv.ParseFloat(txVal, 64)
		ruleThreshold, _ := strconv.ParseFloat("100.003", 64)
		assert.True(t, loggedAmt > ruleThreshold,
			"TxValue=%s must let auditor reproduce the pass outcome", txVal)
	})

	// -------------------------------------------------------------------------
	// OpEquals epsilon semantics
	// -------------------------------------------------------------------------

	t.Run("epsilon: 0.1+0.2 equals 0.3 within epsilon", func(t *testing.T) {
		c := numericRule(t, OpEquals, "0.3")
		assert.True(t, evalAmount(c, 0.1+0.2), "0.1+0.2 must match rule value 0.3 via epsilon")
	})

	t.Run("epsilon: value just within epsilon boundary matches", func(t *testing.T) {
		// epsilon = 0.00001; 0.3 + 0.000009 is within range
		c := numericRule(t, OpEquals, "0.3")
		assert.True(t, evalAmount(c, 0.3+0.000009))
	})

	t.Run("epsilon: value just outside epsilon boundary does not match", func(t *testing.T) {
		// 0.3 + 0.00002 exceeds epsilon on both sides
		c := numericRule(t, OpEquals, "0.3")
		assert.False(t, evalAmount(c, 0.3+0.00002))
	})

	t.Run("epsilon: negative value just within epsilon matches", func(t *testing.T) {
		c := numericRule(t, OpEquals, "0.3")
		assert.True(t, evalAmount(c, 0.3-0.000009))
	})

	t.Run("epsilon: negative value just outside epsilon does not match", func(t *testing.T) {
		c := numericRule(t, OpEquals, "0.3")
		assert.False(t, evalAmount(c, 0.3-0.00002))
	})

	t.Run("epsilon: zero amount equals zero rule", func(t *testing.T) {
		c := numericRule(t, OpEquals, "0")
		assert.True(t, evalAmount(c, 0.0))
	})

	t.Run("epsilon: large financial amount equals itself exactly", func(t *testing.T) {
		c := numericRule(t, OpEquals, "999999.99")
		assert.True(t, evalAmount(c, 999999.99))
	})

	// -------------------------------------------------------------------------
	// OpIn / OpNotIn precision: compiledSet uses %g, evalNumeric uses %g
	// -------------------------------------------------------------------------

	t.Run("OpIn: exact float amount found in set", func(t *testing.T) {
		c := numericRule(t, OpIn, "[100.5, 200.25, 50]")
		assert.True(t, evalAmount(c, 100.5))
		assert.True(t, evalAmount(c, 200.25))
		assert.True(t, evalAmount(c, 50))
	})

	t.Run("OpIn: amount not in set returns false", func(t *testing.T) {
		c := numericRule(t, OpIn, "[100.5, 200.25]")
		assert.False(t, evalAmount(c, 100.6))
	})

	t.Run("OpIn: integer-valued floats match (100.0 == 100 in %g representation)", func(t *testing.T) {
		// JSON parses 100 as float64(100); %g formats as "100".
		// tx.Amount = 100.0; %g formats as "100". They match.
		c := numericRule(t, OpIn, "[100, 200]")
		assert.True(t, evalAmount(c, 100.0))
	})

	t.Run("OpNotIn: amount absent from exclusion set passes", func(t *testing.T) {
		c := numericRule(t, OpNotIn, "[10, 20, 30]")
		assert.True(t, evalAmount(c, 15))
	})

	t.Run("OpNotIn: amount present in exclusion set fails", func(t *testing.T) {
		c := numericRule(t, OpNotIn, "[10, 20, 30]")
		assert.False(t, evalAmount(c, 20))
	})

	// -------------------------------------------------------------------------
	// ConditionResult audit struct fields
	// -------------------------------------------------------------------------

	t.Run("ConditionResult.TxValue matches %g format of tx.Amount", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Name: "Precision", Logic: LogicAnd, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldAmount, Operator: OpGt, Value: "0"},
			},
		}
		require.NoError(t, g.Compile())
		tx := Transaction{Amount: 100.005}
		_, exp := g.Evaluate(tx)
		require.Len(t, exp.ConditionExp, 1)
		cr := exp.ConditionExp[0]
		assert.Equal(t, fmt.Sprintf("%g", 100.005), cr.TxValue,
			"ConditionResult.TxValue must use %%g precision")
		assert.Equal(t, "0", cr.TargetValue)
		assert.True(t, cr.Result)
	})

	t.Run("ConditionResult.TxValue for 0.1+0.2 shows full float, evaluation passes vs 0.3", func(t *testing.T) {
		sum := 0.1 + 0.2 // 0.30000000000000004 in float64
		g := &RuleGroup{
			ID: 2, Name: "FloatSum", Logic: LogicAnd, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldAmount, Operator: OpEquals, Value: "0.3"},
			},
		}
		require.NoError(t, g.Compile())
		_, exp := g.Evaluate(Transaction{Amount: sum})
		require.Len(t, exp.ConditionExp, 1)
		cr := exp.ConditionExp[0]
		// TxValue shows the real float64 value (not rounded).
		assert.Equal(t, fmt.Sprintf("%g", sum), cr.TxValue)
		// Evaluation still passes via epsilon.
		assert.True(t, cr.Result)
	})

	t.Run("ConditionResult.TxValue does NOT equal the %.2f rounded form for sub-cent amounts", func(t *testing.T) {
		g := &RuleGroup{
			ID: 3, Name: "SubCent", Logic: LogicAnd, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldAmount, Operator: OpGt, Value: "0"},
			},
		}
		require.NoError(t, g.Compile())
		subCent := 100.005
		_, exp := g.Evaluate(Transaction{Amount: subCent})
		cr := exp.ConditionExp[0]
		// Must NOT equal the %.2f rounded form.
		roundedForm := fmt.Sprintf("%.2f", subCent)
		assert.NotEqual(t, roundedForm, cr.TxValue,
			"TxValue=%s must not equal %.2f=%s which would cause auditor mismatch", cr.TxValue, roundedForm)
	})

	// -------------------------------------------------------------------------
	// Interaction with OR/AND groups
	// -------------------------------------------------------------------------

	t.Run("AND rule amount+vendor: ConditionResult carries precise TxValue for both", func(t *testing.T) {
		g := &RuleGroup{
			ID: 10, Name: "AND Precise", Logic: LogicAnd, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldVendor, Operator: OpEquals, Value: "acme"},
				{ID: 2, Field: FieldAmount, Operator: OpGte, Value: "99.999"},
			},
		}
		require.NoError(t, g.Compile())
		tx := Transaction{Vendor: "ACME", Amount: 100.0005}
		matched, exp := g.Evaluate(tx)
		assert.True(t, matched)
		amtCR := exp.ConditionExp[1]
		assert.Equal(t, fmt.Sprintf("%g", 100.0005), amtCR.TxValue)
		assert.True(t, amtCR.Result)
	})

	t.Run("OR rule: failed amount condition still logs precise TxValue", func(t *testing.T) {
		g := &RuleGroup{
			ID: 11, Name: "OR Precise", Logic: LogicOr, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldAmount, Operator: OpGt, Value: "1000"},
				{ID: 2, Field: FieldVendor, Operator: OpEquals, Value: "target"},
			},
		}
		require.NoError(t, g.Compile())
		tx := Transaction{Vendor: "TARGET", Amount: 9.999}
		matched, exp := g.Evaluate(tx)
		assert.True(t, matched)
		amtCR := exp.ConditionExp[0]
		// Amount condition failed, but TxValue must still be precise.
		assert.False(t, amtCR.Result)
		assert.Equal(t, fmt.Sprintf("%g", 9.999), amtCR.TxValue)
	})
}

// =============================================================================
// Edge cases and DeriveKeywords
// =============================================================================

func TestEdgeCases(t *testing.T) {
	t.Run("unknown field returns false", func(t *testing.T) {
		c := &RuleCondition{Field: "unknown", Operator: OpEquals, Value: "x"}
		require.NoError(t, c.compile())
		txVal, pass := c.evaluate(Transaction{})
		assert.Empty(t, txVal)
		assert.False(t, pass)
	})

	t.Run("numeric epsilon Equals", func(t *testing.T) {
		c := &RuleCondition{Field: FieldAmount, Operator: OpEquals, Value: "0.3"}
		require.NoError(t, c.compile())
		// 0.1 + 0.2 can be 0.30000000000000004
		tx := Transaction{Amount: 0.1 + 0.2}
		_, pass := c.evaluate(tx)
		assert.True(t, pass)
	})

	t.Run("DeriveKeywords from OpStartsWith and OpEquals", func(t *testing.T) {
		g := &RuleGroup{
			Conditions: []*RuleCondition{
				{Field: FieldVendor, Operator: OpStartsWith, Value: "Starbucks"},
				{Field: FieldCategory, Operator: OpEquals, Value: "Coffee"},
			},
		}
		kw := g.DeriveKeywords()
		assert.Contains(t, kw, "starbucks")
		assert.Contains(t, kw, "coffee")
	})

	t.Run("keywords min length 2", func(t *testing.T) {
		g := &RuleGroup{
			Conditions: []*RuleCondition{
				{Field: FieldVendor, Operator: OpEquals, Value: "A"}, // single char
			},
		}
		kw := g.DeriveKeywords()
		assert.Empty(t, kw)
	})

	t.Run("DeriveKeywords includes OpEndsWith value", func(t *testing.T) {
		g := &RuleGroup{
			Conditions: []*RuleCondition{
				{Field: FieldVendor, Operator: OpEndsWith, Value: "services"},
			},
		}
		kw := g.DeriveKeywords()
		assert.Contains(t, kw, "services")
	})

	t.Run("DeriveKeywords OpEndsWith multi-token value", func(t *testing.T) {
		g := &RuleGroup{
			Conditions: []*RuleCondition{
				{Field: FieldDescription, Operator: OpEndsWith, Value: "web services"},
			},
		}
		kw := g.DeriveKeywords()
		assert.Contains(t, kw, "web")
		assert.Contains(t, kw, "services")
	})

	t.Run("DeriveKeywords covers all new string fields", func(t *testing.T) {
		g := &RuleGroup{
			Conditions: []*RuleCondition{
				{Field: FieldCustomer, Operator: OpContains, Value: "acme"},
				{Field: FieldMCC, Operator: OpEquals, Value: "5411"},
				{Field: FieldInvoiceText, Operator: OpContains, Value: "invoice"},
				{Field: FieldRole, Operator: OpEquals, Value: "admin"},
				{Field: FieldUUID, Operator: OpEquals, Value: "abc123"},
			},
		}
		kw := g.DeriveKeywords()
		assert.Contains(t, kw, "acme")
		assert.Contains(t, kw, "5411")
		assert.Contains(t, kw, "invoice")
		assert.Contains(t, kw, "admin")
		assert.Contains(t, kw, "abc123")
	})

	t.Run("DeriveKeywords OpNotContains does not derive keywords (catch-all intent)", func(t *testing.T) {
		// A not_contains rule should NOT contribute its value to keywords.
		// Adding "refund" as a keyword would exclude this rule from transactions
		// that DON'T have "refund" — the opposite of what a not_contains guard needs.
		g := &RuleGroup{
			Conditions: []*RuleCondition{
				{Field: FieldMemo, Operator: OpNotContains, Value: "refund"},
			},
		}
		kw := g.DeriveKeywords()
		assert.Empty(t, kw, "not_contains rules must be catch-all so they run on every transaction")
	})
}

// =============================================================================
// OpNotContains
// =============================================================================

func TestOpNotContains(t *testing.T) {
	// -------------------------------------------------------------------------
	// Core whole-word semantics
	// -------------------------------------------------------------------------

	t.Run("word absent from field → true", func(t *testing.T) {
		c := &RuleCondition{Field: FieldMemo, Operator: OpNotContains, Value: "refund"}
		require.NoError(t, c.compile())
		_, ok := c.evaluate(Transaction{Memo: "payment processed"})
		assert.True(t, ok)
	})

	t.Run("word present as full token → false", func(t *testing.T) {
		c := &RuleCondition{Field: FieldMemo, Operator: OpNotContains, Value: "refund"}
		require.NoError(t, c.compile())
		_, ok := c.evaluate(Transaction{Memo: "customer refund processed"})
		assert.False(t, ok)
	})

	t.Run("partial substring is NOT a token match → true (whole-word semantics)", func(t *testing.T) {
		// "ref" is not a full token of "refund"; whole-word semantics means not_contains passes.
		c := &RuleCondition{Field: FieldMemo, Operator: OpNotContains, Value: "ref"}
		require.NoError(t, c.compile())
		_, ok := c.evaluate(Transaction{Memo: "refund issued"})
		assert.True(t, ok)
	})

	t.Run("case-insensitive: uppercase value absent → true", func(t *testing.T) {
		c := &RuleCondition{Field: FieldVendor, Operator: OpNotContains, Value: "LYFT"}
		require.NoError(t, c.compile())
		_, ok := c.evaluate(Transaction{Vendor: "UBER EATS"})
		assert.True(t, ok)
	})

	t.Run("case-insensitive: lowercase value present as token → false", func(t *testing.T) {
		c := &RuleCondition{Field: FieldVendor, Operator: OpNotContains, Value: "uber"}
		require.NoError(t, c.compile())
		_, ok := c.evaluate(Transaction{Vendor: "UBER EATS"})
		assert.False(t, ok)
	})

	t.Run("empty field: any value → true (no tokens present)", func(t *testing.T) {
		c := &RuleCondition{Field: FieldMemo, Operator: OpNotContains, Value: "refund"}
		require.NoError(t, c.compile())
		_, ok := c.evaluate(Transaction{Memo: ""})
		assert.True(t, ok)
	})

	t.Run("token at start of field string", func(t *testing.T) {
		c := &RuleCondition{Field: FieldDescription, Operator: OpNotContains, Value: "aws"}
		require.NoError(t, c.compile())
		_, ok := c.evaluate(Transaction{Description: "AWS Monthly bill"})
		assert.False(t, ok) // "aws" is a token of "AWS Monthly bill"
	})

	t.Run("token at end of field string", func(t *testing.T) {
		c := &RuleCondition{Field: FieldDescription, Operator: OpNotContains, Value: "bill"}
		require.NoError(t, c.compile())
		_, ok := c.evaluate(Transaction{Description: "Monthly AWS bill"})
		assert.False(t, ok)
	})

	// -------------------------------------------------------------------------
	// Candidate filter: not_contains rules are catch-all
	// -------------------------------------------------------------------------

	t.Run("not_contains rule has empty keywords → catch-all", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldMemo, Operator: OpNotContains, Value: "refund"},
			},
		}
		require.NoError(t, g.Compile())
		g.Keywords = g.DeriveKeywords()
		assert.Empty(t, g.Keywords)

		// Passes even when tx has nothing in it — catch-all always enters evaluation.
		candidates := FilterCandidates(Transaction{Vendor: "AMAZON"}, []*RuleGroup{g})
		assert.Len(t, candidates, 1)
	})

	// -------------------------------------------------------------------------
	// Full pipeline: DeriveKeywords → FilterCandidates → Evaluate
	// -------------------------------------------------------------------------

	t.Run("AND rule: vendor contains + memo not_contains — both pass", func(t *testing.T) {
		g := &RuleGroup{
			ID: 10, Name: "Amazon non-refund", Logic: LogicAnd, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldVendor, Operator: OpContains, Value: "amazon"},
				{ID: 2, Field: FieldMemo, Operator: OpNotContains, Value: "refund"},
			},
		}
		require.NoError(t, g.Compile())
		g.Keywords = g.DeriveKeywords()
		// Only "amazon" contributes to keywords (from OpContains).
		assert.Contains(t, g.Keywords, "amazon")

		tx := Transaction{Vendor: "AMAZON", Memo: "purchase approved"}
		candidates := FilterCandidates(tx, []*RuleGroup{g})
		require.Len(t, candidates, 1)
		matched, _ := candidates[0].Evaluate(tx)
		assert.True(t, matched)
	})

	t.Run("AND rule: vendor contains + memo not_contains — not_contains blocks match", func(t *testing.T) {
		g := &RuleGroup{
			ID: 11, Name: "Amazon non-refund", Logic: LogicAnd, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldVendor, Operator: OpContains, Value: "amazon"},
				{ID: 2, Field: FieldMemo, Operator: OpNotContains, Value: "refund"},
			},
		}
		require.NoError(t, g.Compile())
		g.Keywords = g.DeriveKeywords()

		tx := Transaction{Vendor: "AMAZON", Memo: "refund applied"}
		candidates := FilterCandidates(tx, []*RuleGroup{g})
		require.Len(t, candidates, 1) // "amazon" keyword still selects it as candidate
		matched, _ := candidates[0].Evaluate(tx)
		assert.False(t, matched) // memo not_contains "refund" fails
	})

	t.Run("OR rule: not_contains or amount — fires via amount when memo contains word", func(t *testing.T) {
		g := &RuleGroup{
			ID: 12, Name: "OR guard", Logic: LogicOr, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldMemo, Operator: OpNotContains, Value: "refund"},
				{ID: 2, Field: FieldAmount, Operator: OpGt, Value: "1000"},
			},
		}
		require.NoError(t, g.Compile())
		// Memo has "refund" → not_contains fails, but amount > 1000 → OR passes.
		tx := Transaction{Memo: "refund", Amount: 1500}
		matched, _ := g.Evaluate(tx)
		assert.True(t, matched)

		// Memo has "refund" AND amount ≤ 1000 → both fail → OR fails.
		txFail := Transaction{Memo: "refund", Amount: 50}
		matchedFail, _ := g.Evaluate(txFail)
		assert.False(t, matchedFail)
	})
}

// =============================================================================
// New string fields: Customer, MCC, InvoiceText, Role, UUID
// =============================================================================

func TestNewStringFields(t *testing.T) {
	tx := Transaction{
		Customer:    "Acme Corp",
		MCC:         "5411",
		InvoiceText: "Invoice #7890 due on receipt",
		Role:        "expense-approver",
		UUID:        "f47ac10b-58cc-4372-a567-0e02b2c3d479",
	}

	fields := []struct {
		field Field
		token string // a whole token within the field value
		full  string // the full lowercase value for OpEquals
	}{
		{FieldCustomer, "acme", "acme corp"},
		{FieldMCC, "5411", "5411"},
		{FieldInvoiceText, "invoice", "invoice #7890 due on receipt"},
		{FieldRole, "expense", "expense-approver"},
		{FieldUUID, "f47ac10b", "f47ac10b-58cc-4372-a567-0e02b2c3d479"},
	}

	for _, f := range fields {
		f := f
		t.Run(string(f.field)+" OpContains token match", func(t *testing.T) {
			c := &RuleCondition{Field: f.field, Operator: OpContains, Value: f.token}
			require.NoError(t, c.compile())
			_, ok := c.evaluate(tx)
			assert.True(t, ok)
		})

		t.Run(string(f.field)+" OpContains non-token → false", func(t *testing.T) {
			c := &RuleCondition{Field: f.field, Operator: OpContains, Value: "zzznomatch"}
			require.NoError(t, c.compile())
			_, ok := c.evaluate(tx)
			assert.False(t, ok)
		})

		t.Run(string(f.field)+" OpNotContains absent token → true", func(t *testing.T) {
			c := &RuleCondition{Field: f.field, Operator: OpNotContains, Value: "zzznomatch"}
			require.NoError(t, c.compile())
			_, ok := c.evaluate(tx)
			assert.True(t, ok)
		})

		t.Run(string(f.field)+" OpIsNull on empty tx → true", func(t *testing.T) {
			c := &RuleCondition{Field: f.field, Operator: OpIsNull}
			require.NoError(t, c.compile())
			_, ok := c.evaluate(Transaction{}) // zero-value tx has all empty strings
			assert.True(t, ok)
		})

		t.Run(string(f.field)+" OpIsNotNull when populated → true", func(t *testing.T) {
			c := &RuleCondition{Field: f.field, Operator: OpIsNotNull}
			require.NoError(t, c.compile())
			_, ok := c.evaluate(tx)
			assert.True(t, ok)
		})
	}

	t.Run("Customer in candidate filter via extractTxTokens", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldCustomer, Operator: OpContains, Value: "acme"},
			},
		}
		require.NoError(t, g.Compile())
		g.Keywords = g.DeriveKeywords()
		assert.Contains(t, g.Keywords, "acme")

		candidates := FilterCandidates(tx, []*RuleGroup{g})
		assert.Len(t, candidates, 1)
		matched, _ := candidates[0].Evaluate(tx)
		assert.True(t, matched)
	})

	t.Run("MCC in candidate filter via extractTxTokens", func(t *testing.T) {
		g := &RuleGroup{
			ID: 2, Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldMCC, Operator: OpEquals, Value: "5411"},
			},
		}
		require.NoError(t, g.Compile())
		g.Keywords = g.DeriveKeywords()
		assert.Contains(t, g.Keywords, "5411")

		candidates := FilterCandidates(tx, []*RuleGroup{g})
		assert.Len(t, candidates, 1)
		matched, _ := candidates[0].Evaluate(tx)
		assert.True(t, matched)
	})

	t.Run("InvoiceText in candidate filter via extractTxTokens", func(t *testing.T) {
		g := &RuleGroup{
			ID: 3, Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldInvoiceText, Operator: OpContains, Value: "invoice"},
			},
		}
		require.NoError(t, g.Compile())
		g.Keywords = g.DeriveKeywords()
		assert.Contains(t, g.Keywords, "invoice")

		candidates := FilterCandidates(tx, []*RuleGroup{g})
		assert.Len(t, candidates, 1)
		matched, _ := candidates[0].Evaluate(tx)
		assert.True(t, matched)
	})

	t.Run("AND rule mixing Customer and MCC fields", func(t *testing.T) {
		g := &RuleGroup{
			ID: 4, Name: "Acme Grocery", Logic: LogicAnd, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldCustomer, Operator: OpContains, Value: "acme"},
				{ID: 2, Field: FieldMCC, Operator: OpEquals, Value: "5411"},
			},
		}
		require.NoError(t, g.Compile())
		matched, _ := g.Evaluate(tx)
		assert.True(t, matched)

		txWrongMCC := Transaction{Customer: "Acme Corp", MCC: "9999"}
		matchedFail, _ := g.Evaluate(txWrongMCC)
		assert.False(t, matchedFail)
	})
}

// =============================================================================
// FieldTime: time-of-day operators with UTC normalization
// =============================================================================

// timeCondition compiles a FieldTime condition with the given operator and value.
func timeCondition(t *testing.T, op Operator, value string) *RuleCondition {
	t.Helper()
	c := &RuleCondition{Field: FieldTime, Operator: op, Value: value}
	require.NoError(t, c.compile())
	return c
}

// evalTxTime is a convenience wrapper: evaluates the condition against a Transaction.Time.
func evalTxTime(c *RuleCondition, d time.Time) bool {
	_, pass := c.evaluate(Transaction{Time: d})
	return pass
}

func TestTimeOperators(t *testing.T) {
	est := time.FixedZone("EST", -5*3600)
	ist := time.FixedZone("IST", 5*3600+30*60)

	// -------------------------------------------------------------------------
	// Compile: HH:MM and HH:MM:SS both accepted
	// -------------------------------------------------------------------------

	t.Run("compile HH:MM format", func(t *testing.T) {
		c := &RuleCondition{Field: FieldTime, Operator: OpEquals, Value: "10:30"}
		require.NoError(t, c.compile())
		assert.Equal(t, 10, c.compiledTime.Hour())
		assert.Equal(t, 30, c.compiledTime.Minute())
		assert.Equal(t, 0, c.compiledTime.Second())
	})

	t.Run("compile HH:MM:SS format", func(t *testing.T) {
		c := &RuleCondition{Field: FieldTime, Operator: OpEquals, Value: "14:05:30"}
		require.NoError(t, c.compile())
		assert.Equal(t, 14, c.compiledTime.Hour())
		assert.Equal(t, 5, c.compiledTime.Minute())
		assert.Equal(t, 30, c.compiledTime.Second())
	})

	t.Run("compile invalid time format fails", func(t *testing.T) {
		c := &RuleCondition{Field: FieldTime, Operator: OpEquals, Value: "25:99"}
		require.Error(t, c.compile())
	})

	// -------------------------------------------------------------------------
	// OpEquals
	// -------------------------------------------------------------------------

	t.Run("OpEquals: exact UTC time matches", func(t *testing.T) {
		c := timeCondition(t, OpEquals, "10:00:00")
		assert.True(t, evalTxTime(c, time.Date(2023, 6, 15, 10, 0, 0, 0, time.UTC)))
	})

	t.Run("OpEquals: one second off → false", func(t *testing.T) {
		c := timeCondition(t, OpEquals, "10:00:00")
		assert.False(t, evalTxTime(c, time.Date(2023, 6, 15, 10, 0, 1, 0, time.UTC)))
	})

	t.Run("OpEquals: HH:MM rule ignores seconds (seconds compile to 0)", func(t *testing.T) {
		// Rule "10:30" compiles to 10:30:00; tx at 10:30:00 UTC matches.
		c := timeCondition(t, OpEquals, "10:30")
		assert.True(t, evalTxTime(c, time.Date(2023, 6, 15, 10, 30, 0, 0, time.UTC)))
		// 10:30:45 does NOT match because second (45) ≠ 0.
		assert.False(t, evalTxTime(c, time.Date(2023, 6, 15, 10, 30, 45, 0, time.UTC)))
	})

	// -------------------------------------------------------------------------
	// OpGt / OpGte / OpLt / OpLte
	// -------------------------------------------------------------------------

	t.Run("comparison operators on UTC times", func(t *testing.T) {
		base := time.Date(2023, 6, 15, 10, 0, 0, 0, time.UTC)
		for _, tc := range []struct {
			op    Operator
			value string
			want  bool
		}{
			{OpGt, "09:00", true},
			{OpGt, "10:00", false},
			{OpGt, "11:00", false},
			{OpGte, "10:00", true},
			{OpGte, "10:00:01", false},
			{OpGte, "09:59", true},
			{OpLt, "11:00", true},
			{OpLt, "10:00", false},
			{OpLt, "09:00", false},
			{OpLte, "10:00", true},
			{OpLte, "09:59", false},
			{OpLte, "10:00:01", true},
		} {
			c := timeCondition(t, tc.op, tc.value)
			got := evalTxTime(c, base)
			assert.Equal(t, tc.want, got, "10:00:00 UTC %s %s", tc.op, tc.value)
		}
	})

	// -------------------------------------------------------------------------
	// UTC normalization: only H/M/S matter; date and timezone are stripped
	// -------------------------------------------------------------------------

	t.Run("same H:M:S in different timezones normalizes to UTC", func(t *testing.T) {
		// 10:00 EST = 15:00 UTC. Rule equals "15:00:00" should match.
		c := timeCondition(t, OpEquals, "15:00:00")
		txEST := time.Date(2023, 6, 15, 10, 0, 0, 0, est) // 15:00 UTC
		assert.True(t, evalTxTime(c, txEST))

		// 10:00 IST = 04:30 UTC. Rule equals "04:30:00" should match.
		c2 := timeCondition(t, OpEquals, "04:30:00")
		txIST := time.Date(2023, 6, 15, 10, 0, 0, 0, ist) // 04:30 UTC
		assert.True(t, evalTxTime(c2, txIST))
	})

	t.Run("date portion is ignored; only H/M/S matter", func(t *testing.T) {
		c := timeCondition(t, OpEquals, "10:00:00")
		// Different calendar dates, same UTC time-of-day.
		for _, d := range []time.Time{
			time.Date(2020, 1, 1, 10, 0, 0, 0, time.UTC),
			time.Date(2023, 6, 15, 10, 0, 0, 0, time.UTC),
			time.Date(2025, 12, 31, 10, 0, 0, 0, time.UTC),
		} {
			assert.True(t, evalTxTime(c, d), "date=%s", d.Format("2006-01-02"))
		}
	})

	t.Run("midnight boundary: 00:00:00 UTC", func(t *testing.T) {
		c := timeCondition(t, OpEquals, "00:00:00")
		assert.True(t, evalTxTime(c, time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)))
		assert.False(t, evalTxTime(c, time.Date(2023, 1, 1, 0, 0, 1, 0, time.UTC)))
	})

	t.Run("end-of-day boundary: 23:59:59 UTC", func(t *testing.T) {
		c := timeCondition(t, OpEquals, "23:59:59")
		assert.True(t, evalTxTime(c, time.Date(2023, 1, 1, 23, 59, 59, 0, time.UTC)))
		assert.False(t, evalTxTime(c, time.Date(2023, 1, 1, 23, 59, 58, 0, time.UTC)))
	})

	// -------------------------------------------------------------------------
	// Full RuleGroup pipeline: business-hours rule
	// -------------------------------------------------------------------------

	t.Run("AND rule: business hours 09:00–17:00 UTC", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Name: "Business Hours", Logic: LogicAnd, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldTime, Operator: OpGte, Value: "09:00"},
				{ID: 2, Field: FieldTime, Operator: OpLt, Value: "17:00"},
			},
		}
		require.NoError(t, g.Compile())

		// 12:00 UTC — in range ✓
		txMid := Transaction{Time: time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)}
		matched, _ := g.Evaluate(txMid)
		assert.True(t, matched)

		// 08:59:59 UTC — before range ✗
		txBefore := Transaction{Time: time.Date(2023, 6, 15, 8, 59, 59, 0, time.UTC)}
		matchedBefore, _ := g.Evaluate(txBefore)
		assert.False(t, matchedBefore)

		// 17:00:00 UTC — at boundary (OpLt, not Lte) ✗
		txAtEnd := Transaction{Time: time.Date(2023, 6, 15, 17, 0, 0, 0, time.UTC)}
		matchedEnd, _ := g.Evaluate(txAtEnd)
		assert.False(t, matchedEnd)

		// 10:00 EST (= 15:00 UTC) — converted to UTC lands inside range ✓
		txEST := Transaction{Time: time.Date(2023, 6, 15, 10, 0, 0, 0, est)}
		matchedEST, _ := g.Evaluate(txEST)
		assert.True(t, matchedEST)
	})

	t.Run("OR rule: off-hours OR high-value amount", func(t *testing.T) {
		g := &RuleGroup{
			ID: 2, Name: "Off-Hours Or High Value", Logic: LogicOr, Active: true,
			Conditions: []*RuleCondition{
				{ID: 1, Field: FieldTime, Operator: OpGte, Value: "22:00"},
				{ID: 2, Field: FieldAmount, Operator: OpGt, Value: "5000"},
			},
		}
		require.NoError(t, g.Compile())

		// 23:00 UTC, small amount → time condition fires ✓
		txLate := Transaction{Time: time.Date(2023, 6, 15, 23, 0, 0, 0, time.UTC), Amount: 50}
		matched, _ := g.Evaluate(txLate)
		assert.True(t, matched)

		// Noon UTC, large amount → amount condition fires ✓
		txBig := Transaction{Time: time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC), Amount: 10000}
		matchedBig, _ := g.Evaluate(txBig)
		assert.True(t, matchedBig)

		// Noon UTC, small amount → neither fires ✗
		txFail := Transaction{Time: time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC), Amount: 50}
		matchedFail, _ := g.Evaluate(txFail)
		assert.False(t, matchedFail)
	})
}

// =============================================================================
// P0-B: DeriveKeywords recursion — subtree keyword union
// =============================================================================

func TestDeriveKeywordsRecursive(t *testing.T) {
	t.Run("child keywords merged into parent", func(t *testing.T) {
		child := &RuleGroup{
			ID: 2, Name: "Child", Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldVendor, Operator: OpContains, Value: "lyft"},
			},
		}
		parent := &RuleGroup{
			ID: 1, Name: "Parent", Logic: LogicOr, Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldVendor, Operator: OpContains, Value: "uber"},
			},
			Children: []*RuleGroup{child},
		}
		child.Parent = parent
		kw := parent.DeriveKeywords()
		assert.Contains(t, kw, "uber")
		assert.Contains(t, kw, "lyft")
	})

	t.Run("parent with no matching keywords but child matches — not filtered out", func(t *testing.T) {
		child := &RuleGroup{
			ID: 2, Name: "Child", Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldMemo, Operator: OpContains, Value: "refund"},
			},
		}
		parent := &RuleGroup{
			ID: 1, Name: "Parent", Logic: LogicOr, Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldAmount, Operator: OpGt, Value: "1000"},
			},
			Children: []*RuleGroup{child},
		}
		child.Parent = parent
		require.NoError(t, parent.Compile())
		parent.Keywords = parent.DeriveKeywords()
		assert.Contains(t, parent.Keywords, "refund")

		tx := Transaction{Memo: "refund applied", Amount: 50}
		candidates := FilterCandidates(tx, []*RuleGroup{parent})
		require.Len(t, candidates, 1)

		matched, _ := candidates[0].Evaluate(tx)
		assert.True(t, matched)
	})

	t.Run("deeply nested grandchild keywords bubble up", func(t *testing.T) {
		grandchild := &RuleGroup{
			ID: 3, Name: "Grandchild", Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldDescription, Operator: OpContains, Value: "groceries"},
			},
		}
		child := &RuleGroup{
			ID: 2, Name: "Child", Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldVendor, Operator: OpContains, Value: "walmart"},
			},
			Children: []*RuleGroup{grandchild},
		}
		parent := &RuleGroup{
			ID: 1, Name: "Parent", Logic: LogicOr, Active: true,
			Children: []*RuleGroup{child},
		}
		grandchild.Parent = child
		child.Parent = parent

		kw := parent.DeriveKeywords()
		assert.Contains(t, kw, "walmart")
		assert.Contains(t, kw, "groceries")
	})
}

// =============================================================================
// P0-C: extractTxTokens includes Role and UUID fields
// =============================================================================

func TestExtractTxTokensRoleAndUUID(t *testing.T) {
	t.Run("Role tokens extracted", func(t *testing.T) {
		tokens := extractTxTokens(Transaction{Role: "expense-approver"})
		_, hasExpense := tokens["expense"]
		_, hasApprover := tokens["approver"]
		assert.True(t, hasExpense, "expected 'expense' token from Role field")
		assert.True(t, hasApprover, "expected 'approver' token from Role field")
	})

	t.Run("UUID tokens extracted", func(t *testing.T) {
		tokens := extractTxTokens(Transaction{UUID: "f47ac10b-58cc-4372-a567-0e02b2c3d479"})
		_, hasSegment := tokens["f47ac10b"]
		assert.True(t, hasSegment, "expected UUID segment token from UUID field")
	})

	t.Run("Role rule passes FilterCandidates when Role is populated", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldRole, Operator: OpEquals, Value: "admin"},
			},
		}
		require.NoError(t, g.Compile())
		g.Keywords = g.DeriveKeywords()
		assert.Contains(t, g.Keywords, "admin")

		tx := Transaction{Role: "admin"}
		candidates := FilterCandidates(tx, []*RuleGroup{g})
		require.Len(t, candidates, 1)
		matched, _ := candidates[0].Evaluate(tx)
		assert.True(t, matched)
	})

	t.Run("UUID rule passes FilterCandidates when UUID is populated", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldUUID, Operator: OpContains, Value: "f47ac10b"},
			},
		}
		require.NoError(t, g.Compile())
		g.Keywords = g.DeriveKeywords()
		assert.Contains(t, g.Keywords, "f47ac10b")

		tx := Transaction{UUID: "f47ac10b-58cc-4372-a567-0e02b2c3d479"}
		candidates := FilterCandidates(tx, []*RuleGroup{g})
		require.Len(t, candidates, 1)
		matched, _ := candidates[0].Evaluate(tx)
		assert.True(t, matched)
	})
}

// =============================================================================
// P2-A: Operator/field compatibility validation
// =============================================================================

func TestFieldOperatorCompatibility(t *testing.T) {
	t.Run("regex on amount rejected", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Conditions: []*RuleCondition{
				{ID: 1, Field: FieldAmount, Operator: OpRegex, Value: ".*"},
			},
		}
		err := g.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), `operator "regex" is not valid for field "amount"`)
	})

	t.Run("contains on amount rejected", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Conditions: []*RuleCondition{
				{ID: 2, Field: FieldAmount, Operator: OpContains, Value: "100"},
			},
		}
		require.Error(t, g.Validate())
	})

	t.Run("gt on string field rejected", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Conditions: []*RuleCondition{
				{ID: 3, Field: FieldVendor, Operator: OpGt, Value: "abc"},
			},
		}
		require.Error(t, g.Validate())
	})

	t.Run("regex on date rejected", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Conditions: []*RuleCondition{
				{ID: 4, Field: FieldDate, Operator: OpRegex, Value: "2024.*"},
			},
		}
		require.Error(t, g.Validate())
	})

	t.Run("in/not_in on time rejected", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Conditions: []*RuleCondition{
				{ID: 5, Field: FieldTime, Operator: OpIn, Value: `["09:00"]`},
			},
		}
		require.Error(t, g.Validate())
	})

	t.Run("unknown field rejected", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Conditions: []*RuleCondition{
				{ID: 6, Field: Field("bogus"), Operator: OpEquals, Value: "x"},
			},
		}
		err := g.Validate()
		require.Error(t, err)
		assert.Contains(t, err.Error(), `unknown field "bogus"`)
	})

	t.Run("valid combinations pass", func(t *testing.T) {
		cases := []struct {
			field Field
			op    Operator
			val   string
		}{
			{FieldVendor, OpEquals, "acme"},
			{FieldVendor, OpRegex, "acme.*"},
			{FieldAmount, OpGt, "100"},
			{FieldAmount, OpIn, "[10,20]"},
			{FieldDate, OpLte, "2026-01-01"},
			{FieldTime, OpGte, "09:00"},
			{FieldDescription, OpIsNull, ""},
			{FieldAmount, OpIsNotNull, ""},
		}
		for _, tc := range cases {
			g := &RuleGroup{
				ID: 1, Conditions: []*RuleCondition{
					{ID: 1, Field: tc.field, Operator: tc.op, Value: tc.val},
				},
			}
			require.NoError(t, g.Validate(), "field=%s op=%s should be valid", tc.field, tc.op)
		}
	})

	t.Run("child with incompatible operator rejected", func(t *testing.T) {
		parent := &RuleGroup{
			ID: 1, Logic: LogicAnd, Active: true,
			Children: []*RuleGroup{
				{
					ID: 2, Active: true,
					Conditions: []*RuleCondition{
						{ID: 10, Field: FieldAmount, Operator: OpRegex, Value: ".*"},
					},
				},
			},
		}
		require.Error(t, parent.Validate())
	})
}

// =============================================================================
// P2-B: Numeric set membership with epsilon comparison
// =============================================================================

func TestNumericSetEpsilon(t *testing.T) {
	t.Run("epsilon match within tolerance", func(t *testing.T) {
		c := numericInRule(t, OpIn, "[100.005]")
		assert.True(t, evalAmount(c, 100.005))
		assert.True(t, evalAmount(c, 100.005+5e-6))
		assert.True(t, evalAmount(c, 100.005-5e-6))
	})

	t.Run("epsilon reject outside tolerance", func(t *testing.T) {
		c := numericInRule(t, OpIn, "[100.005]")
		assert.False(t, evalAmount(c, 100.005+2e-5))
		assert.False(t, evalAmount(c, 100.005-2e-5))
	})

	t.Run("not_in with epsilon", func(t *testing.T) {
		c := numericInRule(t, OpNotIn, "[50, 100]")
		assert.True(t, evalAmount(c, 75.0))
		assert.False(t, evalAmount(c, 50.0))
		assert.False(t, evalAmount(c, 100.0+1e-6))
	})

	t.Run("non-numeric value in amount set rejected", func(t *testing.T) {
		c := &RuleCondition{Field: FieldAmount, Operator: OpIn, Value: `[10, "abc"]`}
		require.Error(t, c.compile())
	})

	t.Run("string in set still uses string comparison", func(t *testing.T) {
		c := &RuleCondition{Field: FieldVendor, Operator: OpIn, Value: `["Uber", "Lyft"]`}
		require.NoError(t, c.compile())
		require.NotNil(t, c.compiledSet, "string fields should use compiledSet")
		require.Nil(t, c.compiledNumericSet, "string fields should NOT populate compiledNumericSet")
	})
}

// =============================================================================
// P2-C: Pre-split keyword set
// =============================================================================

func TestKeywordSetPreSplit(t *testing.T) {
	t.Run("Compile builds keywordSet from Keywords string", func(t *testing.T) {
		g := &RuleGroup{ID: 1, Active: true, Keywords: "uber eats delivery"}
		require.NoError(t, g.Compile())
		require.Len(t, g.keywordSet, 3)
		_, ok := g.keywordSet["uber"]
		assert.True(t, ok)
		_, ok = g.keywordSet["delivery"]
		assert.True(t, ok)
	})

	t.Run("DeriveKeywords also builds keywordSet", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldVendor, Operator: OpContains, Value: "acme"},
			},
		}
		g.Keywords = g.DeriveKeywords()
		require.Len(t, g.keywordSet, 1)
		_, ok := g.keywordSet["acme"]
		assert.True(t, ok)
	})

	t.Run("empty keywords yields nil set treated as catch-all", func(t *testing.T) {
		g := &RuleGroup{ID: 1, Active: true, Keywords: ""}
		require.NoError(t, g.Compile())
		require.Nil(t, g.keywordSet)

		tx := Transaction{Vendor: "anything"}
		candidates := FilterCandidates(tx, []*RuleGroup{g})
		assert.Len(t, candidates, 1)
	})

	t.Run("FilterCandidates uses prebuilt set", func(t *testing.T) {
		g := &RuleGroup{
			ID: 1, Active: true,
			Conditions: []*RuleCondition{
				{Field: FieldVendor, Operator: OpContains, Value: "uber"},
			},
		}
		require.NoError(t, g.Compile())
		g.Keywords = g.DeriveKeywords()

		hit := FilterCandidates(Transaction{Vendor: "UBER EATS"}, []*RuleGroup{g})
		assert.Len(t, hit, 1)

		miss := FilterCandidates(Transaction{Vendor: "LYFT"}, []*RuleGroup{g})
		assert.Len(t, miss, 0)
	})
}
