package pcm_cash

import (
	"context"
	"fmt"
	"strings"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AccountCandidate represents an entity-allowed Moroccan PCGE account candidate.
type AccountCandidate struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	BalanceType string `json:"balance_type"`
	Active      bool   `json:"active"`
}

// BaselineMoroccanPCGECatalog provides standard PCGE accounts strictly for test fixtures.
// It must NEVER be used as an implicit production fallback.
var BaselineMoroccanPCGECatalog = []AccountCandidate{
	{Code: "2355", Name: "Matériel informatique", Role: "asset_ppe_equip", BalanceType: "debit", Active: true},
	{Code: "3421", Name: "Clients", Role: "asset_ca_recv", BalanceType: "debit", Active: true},
	{Code: "3455", Name: "État, TVA récupérable", Role: "asset_ca_other", BalanceType: "debit", Active: true},
	{Code: "4411", Name: "Fournisseurs", Role: "lia_cl_acc_payable", BalanceType: "credit", Active: true},
	{Code: "4432", Name: "Rémunérations dues au personnel", Role: "lia_cl_wages_payable", BalanceType: "credit", Active: true},
	{Code: "4455", Name: "État, TVA facturée", Role: "lia_cl_taxes_payable", BalanceType: "credit", Active: true},
	{Code: "5115", Name: "Virements de fonds", Role: "asset_ca_cash", BalanceType: "debit", Active: true},
	{Code: "5141", Name: "Banques", Role: "asset_ca_cash", BalanceType: "debit", Active: true},
	{Code: "6111", Name: "Achats de marchandises", Role: "cogs_regular", BalanceType: "debit", Active: true},
	{Code: "6125", Name: "Achats de fournitures de bureau", Role: "ex_regular", BalanceType: "debit", Active: true},
	{Code: "6131", Name: "Locations et charges locatives", Role: "ex_regular", BalanceType: "debit", Active: true},
	{Code: "6134", Name: "Services informatiques et télécommunications", Role: "ex_regular", BalanceType: "debit", Active: true},
	{Code: "6136", Name: "Rémunérations d'intermédiaires et honoraires", Role: "ex_regular", BalanceType: "debit", Active: true},
	{Code: "6147", Name: "Services bancaires et commissions", Role: "ex_regular", BalanceType: "debit", Active: true},
	{Code: "7111", Name: "Ventes de marchandises au Maroc", Role: "in_operational", BalanceType: "credit", Active: true},
}

// BaselineMoroccanCatalog returns standard PCGE accounts for explicit test injection only.
// It must NEVER be used as an implicit production fallback.
func BaselineMoroccanCatalog() []AccountCandidate {
	return append([]AccountCandidate(nil), BaselineMoroccanPCGECatalog...)
}

type testCatalogContextKey struct{}

// ContextWithExplicitTestCatalog injects an explicit test catalog into the context for test execution.
// In production, this context key is never set; GetCandidateAccounts fails closed if authoritative CoA is unavailable.
func ContextWithExplicitTestCatalog(ctx context.Context, catalog []AccountCandidate) context.Context {
	return context.WithValue(ctx, testCatalogContextKey{}, catalog)
}

// Candidate source telemetry constants
const (
	CandidateSourceDjangoLedgerDefaultCoA = "DJANGO_LEDGER_DEFAULT_COA"
	CandidateSourceExplicitTestInjection  = "EXPLICIT_TEST_INJECTION"
)

// AuthoritativeDjangoCoaQuery is the exact SQL read path against Django ledger persistence.
// Architectural Invariants:
// 1. Authoritative persistence: ledger_accountmodel JOIN ledger_chartofaccountmodel JOIN ledger_entitymodel
// 2. Strict EntityModel.default_coa enforcement: e.default_coa_id = coa.uuid
// 3. Company/entity scoped with zero cross-company leakage: coa.entity_id = e.uuid AND (e.uuid::text = $1 OR e.slug = $1)
// 4. Read-only (SELECT query only, performs no writes)
// 5. Zero Shadow ERP authority (no tables from shadow_erp are referenced)
// 6. Active accounts only (a.active = true)
const AuthoritativeDjangoCoaQuery = `
	SELECT a.code, a.name, a.role, a.balance_type, a.active
	FROM ledger_accountmodel a
	JOIN ledger_chartofaccountmodel coa ON coa.uuid = a.coa_model_id
	JOIN ledger_entitymodel e ON (e.default_coa_id = coa.uuid AND coa.entity_id = e.uuid)
	WHERE (REPLACE(CAST(e.uuid AS text), '-', '') = REPLACE($1, '-', '') OR e.slug = $1)
	  AND a.active = true
	ORDER BY a.code ASC;
`

// GetCandidateAccountsWithSource retrieves candidate Moroccan accounts for a company from the Django ledger schema,
// strictly respecting EntityModel.default_coa, and returns the accounts alongside the candidate source telemetry tag.
// In production, it fails closed if the database pool is unavailable, query fails, or company has no authoritative CoA.
// Silent fallback to BaselineMoroccanPCGECatalog is prohibited.
func GetCandidateAccountsWithSource(
	ctx context.Context,
	pool *pgxpool.Pool,
	db *database.Queries,
	companyID string,
	macroClass string,
	direction string,
) ([]AccountCandidate, string, error) {
	return GetCandidateAccountsWithSourceAndLifecycle(ctx, pool, db, companyID, macroClass, direction, "", "")
}

// GetCandidateAccountsWithSourceAndLifecycle retrieves candidate Moroccan accounts for a company from the Django ledger schema,
// strictly respecting EntityModel.default_coa and lifecycle family constraints, and returns the accounts alongside candidate source telemetry.
func GetCandidateAccountsWithSourceAndLifecycle(
	ctx context.Context,
	pool *pgxpool.Pool,
	db *database.Queries,
	companyID string,
	macroClass string,
	direction string,
	sourceKind string,
	bookRole string,
) ([]AccountCandidate, string, error) {
	if strings.TrimSpace(companyID) == "" {
		return nil, "", fmt.Errorf("companyID is required for candidate account lookup")
	}

	// 1. Check for explicitly injected test catalog
	if testCat, ok := ctx.Value(testCatalogContextKey{}).([]AccountCandidate); ok {
		return FilterAccountsBySourceFamily(testCat, macroClass, sourceKind, bookRole), CandidateSourceExplicitTestInjection, nil
	}

	// 2. Production invariant: fail closed if database pool is unavailable
	if pool == nil {
		return nil, "", fmt.Errorf("authoritative CoA pool is unavailable (pool is nil)")
	}

	var allAccounts []AccountCandidate
	rows, err := pool.Query(ctx, AuthoritativeDjangoCoaQuery, companyID)
	if err != nil {
		return nil, "", fmt.Errorf("authoritative CoA query failed for company %s: %w", companyID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var cand AccountCandidate
		if scanErr := rows.Scan(&cand.Code, &cand.Name, &cand.Role, &cand.BalanceType, &cand.Active); scanErr == nil {
			allAccounts = append(allAccounts, cand)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("authoritative CoA row iteration failed for company %s: %w", companyID, err)
	}

	// If no authoritative accounts found for company, return empty slice (fail closed)
	if len(allAccounts) == 0 {
		return nil, CandidateSourceDjangoLedgerDefaultCoA, nil
	}

	// Deterministic pre-filtering based on source lifecycle and macro family
	return FilterAccountsBySourceFamily(allAccounts, macroClass, sourceKind, bookRole), CandidateSourceDjangoLedgerDefaultCoA, nil
}

// FilterAccountsBySourceFamily filters accounts strictly by authoritative source lifecycle constraints and macro class.
// For INVOICE / OPEN_RECEIVABLE: constrains to receivable/asset-compatible accounts (role: asset_ca_recv or code prefix 342).
// For BILL / OPEN_PAYABLE: constrains to payable/liability-compatible accounts (role: lia_cl_acc_payable or code prefix 441).
// For Direct / Other: constrains by standard macro class (EXPENSE, REVENUE, ASSET, LIABILITY, EQUITY).
func FilterAccountsBySourceFamily(
	accounts []AccountCandidate,
	macroClass string,
	sourceKind string,
	bookRole string,
) []AccountCandidate {
	sKind := strings.ToUpper(strings.TrimSpace(sourceKind))
	bRole := strings.ToUpper(strings.TrimSpace(bookRole))

	if sKind == "INVOICE" || bRole == "OPEN_RECEIVABLE" {
		var filtered []AccountCandidate
		for _, acc := range accounts {
			role := strings.ToLower(acc.Role)
			if role == "asset_ca_recv" || strings.HasPrefix(acc.Code, "342") {
				filtered = append(filtered, acc)
			}
		}
		return filtered
	}

	if sKind == "BILL" || bRole == "OPEN_PAYABLE" {
		var filtered []AccountCandidate
		for _, acc := range accounts {
			role := strings.ToLower(acc.Role)
			if role == "lia_cl_acc_payable" || strings.HasPrefix(acc.Code, "441") {
				filtered = append(filtered, acc)
			}
		}
		return filtered
	}

	return filterAccountsByMacroClass(accounts, macroClass)
}

// GetCandidateAccounts retrieves candidate Moroccan accounts for a company from the Django ledger schema,
// maintaining backwards compatibility with callers that only expect ([]AccountCandidate, error).
func GetCandidateAccounts(
	ctx context.Context,
	pool *pgxpool.Pool,
	db *database.Queries,
	companyID string,
	macroClass string,
	direction string,
) ([]AccountCandidate, error) {
	cands, _, err := GetCandidateAccountsWithSource(ctx, pool, db, companyID, macroClass, direction)
	return cands, err
}

func filterAccountsByMacroClass(accounts []AccountCandidate, macroClass string) []AccountCandidate {
	mUpper := strings.ToUpper(strings.TrimSpace(macroClass))
	if mUpper == "" || mUpper == "UNKNOWN" {
		return accounts
	}

	var filtered []AccountCandidate
	for _, acc := range accounts {
		code := acc.Code
		role := strings.ToLower(acc.Role)

		switch mUpper {
		case "EXPENSE":
			if strings.HasPrefix(code, "6") || role == "cogs_regular" || strings.HasPrefix(role, "ex_") {
				filtered = append(filtered, acc)
			}
		case "REVENUE":
			if strings.HasPrefix(code, "7") || strings.HasPrefix(role, "in_") {
				filtered = append(filtered, acc)
			}
		case "ASSET":
			if strings.HasPrefix(code, "2") || strings.HasPrefix(code, "3") || strings.HasPrefix(code, "5") || strings.HasPrefix(role, "asset_") {
				filtered = append(filtered, acc)
			}
		case "LIABILITY":
			if strings.HasPrefix(code, "1") || strings.HasPrefix(code, "4") || strings.HasPrefix(code, "5") || strings.HasPrefix(role, "lia_") {
				filtered = append(filtered, acc)
			}
		case "EQUITY":
			if strings.HasPrefix(code, "1") || strings.HasPrefix(role, "eq_") {
				filtered = append(filtered, acc)
			}
		default:
			filtered = append(filtered, acc)
		}
	}

	if len(filtered) == 0 {
		return accounts
	}
	return filtered
}
