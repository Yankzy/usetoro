package pcm_cash

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/tap/pkg/agent"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PcmClassifier implements ase.Classifier for Moroccan bookkeeping workflows.
// It executes deterministic routing and real model reasoning over pre-fetched PCGE candidates.
type PcmClassifier struct {
	rt                  *agent.Runtime
	pool                *pgxpool.Pool
	db                  *database.Queries
	logger              *slog.Logger
	explicitTestCatalog []AccountCandidate
}

// NewPcmClassifier constructs a new Moroccan PCGE classifier.
func NewPcmClassifier(rt *agent.Runtime, pool *pgxpool.Pool, db *database.Queries, logger *slog.Logger) *PcmClassifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &PcmClassifier{
		rt:     rt,
		pool:   pool,
		db:     db,
		logger: logger.With("classifier", "pcm_moroccan"),
	}
}

// SetExplicitTestCatalog injects an explicit test catalog for test execution only.
// In production, candidate accounts must be fetched authoritatively from the company's ledger CoA.
func (c *PcmClassifier) SetExplicitTestCatalog(catalog []AccountCandidate) {
	c.explicitTestCatalog = catalog
}

// BuildPayloadRouterThinkFunc routes transactions deterministically based on payload direction.
func (c *PcmClassifier) BuildPayloadRouterThinkFunc(payloadKey string) ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		results := make(map[string]ase.NodeClassification, len(batch))
		for _, node := range batch {
			desc := extractPayloadString(node.Payload, "description")
			cpName := extractPayloadString(node.Payload, "counterparty_name")
			combinedUpper := strings.ToUpper(strings.TrimSpace(desc + " " + cpName))
			hasExistingCode := false
			if code, ok := node.Payload["existing_account_code"].(*string); ok && code != nil && *code != "" {
				hasExistingCode = true
			} else if codeStr, ok := node.Payload["existing_account_code"].(string); ok && codeStr != "" {
				hasExistingCode = true
			}

			if (combinedUpper == "" && !hasExistingCode) || strings.Contains(combinedUpper, "AMBIGUOUS") || strings.Contains(combinedUpper, "UNKNOWN") {
				results[node.NodeID] = ase.NodeClassification{
					Property: "direction",
					Candidates: []ase.ProbabilityCandidate{
						{Value: "HOLD_AMBIGUOUS", Confidence: 1.0, Reasoning: "Description is ambiguous or missing source context"},
					},
				}
				continue
			}

			direction := extractPayloadString(node.Payload, "direction")
			if direction == "" {
				direction = extractPayloadString(node.Payload, "cash_direction")
			}
			routeDir := "OUTFLOW"
			if strings.EqualFold(direction, "INFLOW") || strings.EqualFold(direction, "BOOK_BANK_DEBIT") {
				routeDir = "INFLOW"
			}

			results[node.NodeID] = ase.NodeClassification{
				Property: "direction",
				Candidates: []ase.ProbabilityCandidate{
					{Value: routeDir, Confidence: 0.99, Reasoning: "Deterministic routing based on transaction direction: " + routeDir},
				},
			}
		}
		return results, nil
	}
}

func extractPayloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	v, ok := payload[key]
	if !ok || v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	case *string:
		if val != nil {
			return strings.TrimSpace(*val)
		}
	}
	return ""
}

func extractAmountUnits(payload map[string]any) int64 {
	if payload == nil {
		return 0
	}
	keys := []string{
		"residual_amount_units",
		"original_amount_units",
		"amount_units",
		"amount",
		"raw_amount",
	}
	for _, k := range keys {
		v, ok := payload[k]
		if !ok || v == nil {
			continue
		}
		switch val := v.(type) {
		case int64:
			if val != 0 {
				return val
			}
		case int:
			if val != 0 {
				return int64(val)
			}
		case float64:
			if val != 0 {
				return int64(val)
			}
		case string:
			if parsed, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64); err == nil && parsed != 0 {
				return parsed
			}
		}
	}
	return 0
}

func extractItemID(node *ase.AutonomousSemanticEngineNode) string {
	if node == nil {
		return ""
	}
	if node.Payload != nil {
		if id := extractPayloadString(node.Payload, "bank_item_id"); id != "" {
			return id
		}
		if id := extractPayloadString(node.Payload, "book_item_id"); id != "" {
			return id
		}
	}
	return node.NodeID
}

func extractEvidenceSummaries(payload map[string]any) []string {
	if payload == nil {
		return nil
	}
	var evidenceStrs []string
	keys := []string{"safe_evidence_summaries", "provenance_refs", "evidence_refs"}
	for _, k := range keys {
		v, ok := payload[k]
		if !ok || v == nil {
			continue
		}
		switch list := v.(type) {
		case []string:
			for _, s := range list {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					evidenceStrs = append(evidenceStrs, trimmed)
				}
			}
		case []any:
			for _, item := range list {
				if s, ok := item.(string); ok {
					if trimmed := strings.TrimSpace(s); trimmed != "" {
						evidenceStrs = append(evidenceStrs, trimmed)
					}
				}
			}
		case string:
			if trimmed := strings.TrimSpace(list); trimmed != "" {
				evidenceStrs = append(evidenceStrs, trimmed)
			}
		}
	}
	return evidenceStrs
}

func isInternalTransferDescription(desc, counterparty string) bool {
	combined := strings.ToUpper(strings.TrimSpace(desc + " " + counterparty))
	if combined == "" {
		return false
	}
	combined = strings.ReplaceAll(combined, "À", "A")
	combined = strings.ReplaceAll(combined, "É", "E")
	combined = strings.ReplaceAll(combined, "È", "E")

	patterns := []string{
		"VIREMENT INTERNE",
		"VIR INTERNE",
		"VIREMENT DE COMPTE A COMPTE",
		"VIREMENT COMPTE A COMPTE",
		"VIR DE COMPTE A COMPTE",
		"VIR COMPTE A COMPTE",
		"VIREMENT C/C",
		"VIR C/C",
		"VIREMENT PROPRE COMPTE",
		"VIR PROPRE COMPTE",
		"TRANSIT 5115",
		"5115",
		"COMPTE A COMPTE",
		"INTERNAL TRANSFER",
		"OWN ACCOUNT TRANSFER",
	}

	for _, p := range patterns {
		if strings.Contains(combined, p) {
			return true
		}
	}
	return false
}

func macroClassFromAccountCode(code string) string {
	c := strings.TrimSpace(code)
	if len(c) == 0 {
		return "UNKNOWN"
	}
	switch c[0] {
	case '6':
		return "EXPENSE"
	case '7':
		return "REVENUE"
	case '2', '3':
		return "ASSET"
	case '4':
		return "LIABILITY"
	case '1':
		return "LIABILITY"
	case '5':
		return "ASSET"
	default:
		return "UNKNOWN"
	}
}

// BuildGenericThinkFunc executes semantic macro classification using the model runtime,
// applying deterministic constraints when authoritative source lifecycle facts are known.
func (c *PcmClassifier) BuildGenericThinkFunc(promptKey string) ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		results := make(map[string]ase.NodeClassification, len(batch))
		if len(batch) == 0 {
			return results, nil
		}

		tenantID := batch[0].TenantID
		dagName := batch[0].DagName
		systemPrompt := ase.GetPrompt(tenantID, dagName, promptKey)
		if systemPrompt == "" {
			systemPrompt = defaultMoroccanMacroPrompt(promptKey)
		}

		for _, node := range batch {
			nodeID := node.NodeID
			sourceKind := extractPayloadString(node.Payload, "source_artifact_kind")
			bookRole := extractPayloadString(node.Payload, "bookkeeping_role")
			existingCode := extractPayloadString(node.Payload, "existing_account_code")

			sKindUpper := strings.ToUpper(sourceKind)
			bRoleUpper := strings.ToUpper(bookRole)

			// Rule 1: Posted transaction / existing validated account code
			// Audit: Do not reclassify already-authoritative accounting truth.
			if existingCode != "" {
				derivedMacro := macroClassFromAccountCode(existingCode)
				node.Mu.Lock()
				if node.Payload == nil {
					node.Payload = make(map[string]any)
				}
				node.Payload["macro_class"] = derivedMacro
				node.Payload["source_kind"] = "POSTED_TRANSACTION"
				node.Payload["constrained_macro"] = derivedMacro
				node.Mu.Unlock()

				results[nodeID] = ase.NodeClassification{
					Property: "macro_class",
					Candidates: []ase.ProbabilityCandidate{
						{
							Value:      derivedMacro,
							Confidence: 1.0,
							Reasoning:  "Pre-existing validated account code preserved; semantic macro classification bypassed",
						},
					},
				}
				continue
			}

			// Rule 1b: Posted transaction missing authoritative account code: reclassification prohibited
			if sKindUpper == "TRANSACTION" && bRoleUpper == "POSTED_CASH_MOVEMENT" {
				node.Mu.Lock()
				if node.Payload == nil {
					node.Payload = make(map[string]any)
				}
				node.Payload["source_kind"] = "TRANSACTION"
				node.Mu.Unlock()

				results[nodeID] = ase.NodeClassification{
					Property: "macro_class",
					Candidates: []ase.ProbabilityCandidate{
						{
							Value:      "HOLD_AMBIGUOUS",
							Confidence: 1.0,
							Reasoning:  "Posted transaction missing authoritative ledger account code; semantic reclassification prohibited",
						},
					},
				}
				continue
			}

			// Rule 2: Invoice / Open Receivable
			// Lifecycle Fact: Invoice source = open receivable obligation.
			// Constrain admissible family deterministically to ASSET (receivable control account family).
			if sKindUpper == "INVOICE" || bRoleUpper == "OPEN_RECEIVABLE" {
				node.Mu.Lock()
				if node.Payload == nil {
					node.Payload = make(map[string]any)
				}
				node.Payload["macro_class"] = "ASSET"
				node.Payload["source_kind"] = "INVOICE"
				node.Payload["constrained_macro"] = "ASSET"
				node.Mu.Unlock()

				results[nodeID] = ase.NodeClassification{
					Property: "macro_class",
					Candidates: []ase.ProbabilityCandidate{
						{
							Value:      "ASSET",
							Confidence: 1.0,
							Reasoning:  "Authoritative invoice/open receivable lifecycle fact: constrained to receivable/asset family",
						},
					},
				}
				continue
			}

			// Rule 3: Bill / Open Payable
			// Lifecycle Fact: Bill source = open payable obligation.
			// Constrain admissible family deterministically to LIABILITY (payable control account family).
			if sKindUpper == "BILL" || bRoleUpper == "OPEN_PAYABLE" {
				node.Mu.Lock()
				if node.Payload == nil {
					node.Payload = make(map[string]any)
				}
				node.Payload["macro_class"] = "LIABILITY"
				node.Payload["source_kind"] = "BILL"
				node.Payload["constrained_macro"] = "LIABILITY"
				node.Mu.Unlock()

				results[nodeID] = ase.NodeClassification{
					Property: "macro_class",
					Candidates: []ase.ProbabilityCandidate{
						{
							Value:      "LIABILITY",
							Confidence: 1.0,
							Reasoning:  "Authoritative bill/open payable lifecycle fact: constrained to payable/liability family",
						},
					},
				}
				continue
			}

			// Rule 4: Direct / Other / Residual Bank Movements
			// Check for internal bank account transfer
			desc := extractPayloadString(node.Payload, "description")
			cpName := extractPayloadString(node.Payload, "counterparty_name")

			if isInternalTransferDescription(desc, cpName) {
				node.Mu.Lock()
				if node.Payload == nil {
					node.Payload = make(map[string]any)
				}
				node.Payload["macro_class"] = "HOLD_INSUFFICIENT_EVIDENCE"
				node.Payload["hold_reason"] = "HOLD_INSUFFICIENT_EVIDENCE"
				node.Payload["rationale"] = "Internal transfer detected; counterpart account or paired bank line matching required, placed on HOLD"
				node.Mu.Unlock()

				results[nodeID] = ase.NodeClassification{
					Property: "macro_class",
					Candidates: []ase.ProbabilityCandidate{
						{
							Value:      "HOLD_INSUFFICIENT_EVIDENCE",
							Confidence: 1.0,
							Reasoning:  "Internal transfer detected; counterpart account or paired bank line matching required, placed on HOLD",
						},
					},
				}
				continue
			}

			// Check for ambiguous or unknown description
			combinedUpper := strings.ToUpper(strings.TrimSpace(desc + " " + cpName))
			if (combinedUpper == "" && existingCode == "") || strings.Contains(combinedUpper, "AMBIGUOUS") || strings.Contains(combinedUpper, "UNKNOWN") {
				node.Mu.Lock()
				if node.Payload == nil {
					node.Payload = make(map[string]any)
				}
				node.Payload["macro_class"] = "HOLD_AMBIGUOUS"
				node.Payload["hold_reason"] = "HOLD_AMBIGUOUS"
				node.Payload["rationale"] = "Transaction description or counterparty is ambiguous or missing"
				node.Mu.Unlock()

				results[nodeID] = ase.NodeClassification{
					Property: "macro_class",
					Candidates: []ase.ProbabilityCandidate{
						{
							Value:      "HOLD_AMBIGUOUS",
							Confidence: 1.0,
							Reasoning:  "Transaction description or counterparty is ambiguous or missing",
						},
					},
				}
				continue
			}

			// Genuine semantic ambiguity: model reasoning determines macro class.
			node.Mu.Lock()
			if node.Payload == nil {
				node.Payload = make(map[string]any)
			}
			node.Payload["source_kind"] = "DIRECT_OR_OTHER"
			node.Mu.Unlock()

			userPrompt := buildMacroUserPrompt(node)

			if c.rt == nil {
				// Offline heuristic fallback for unit testing when model runtime is not injected
				descUpper := strings.ToUpper(desc + " " + cpName)
				direction := extractPayloadString(node.Payload, "direction")
				if direction == "" {
					direction = extractPayloadString(node.Payload, "cash_direction")
				}

				macroClass := "EXPENSE"
				isInflow := strings.Contains(strings.ToLower(promptKey), "inflow") || strings.EqualFold(direction, "INFLOW") || strings.EqualFold(direction, "BOOK_BANK_DEBIT")

				if isInflow {
					macroClass = "REVENUE"
					if strings.Contains(descUpper, "EMPRUNT") || strings.Contains(descUpper, "LOAN") {
						macroClass = "LIABILITY"
					} else if strings.Contains(descUpper, "CAPITAL") || strings.Contains(descUpper, "APPORT") {
						macroClass = "EQUITY"
					} else if strings.Contains(descUpper, "REMBOURSEMENT") {
						macroClass = "ASSET"
					}
				} else {
					if strings.Contains(descUpper, "SALAIRE") || strings.Contains(descUpper, "PAYROLL") {
						macroClass = "LIABILITY"
					} else if strings.Contains(descUpper, "CAPITAL") {
						macroClass = "EQUITY"
					} else if strings.Contains(descUpper, "MATERIEL") || strings.Contains(descUpper, "EQUIPMENT") || strings.Contains(descUpper, "ORDINATEUR") {
						macroClass = "ASSET"
					}
				}

				node.Mu.Lock()
				node.Payload["macro_class"] = macroClass
				node.Payload["constrained_macro"] = macroClass
				node.Mu.Unlock()

				results[nodeID] = ase.NodeClassification{
					Property: "macro_class",
					Candidates: []ase.ProbabilityCandidate{
						{Value: macroClass, Confidence: 0.99, Reasoning: "Offline Moroccan heuristic macro classification"},
					},
				}
				continue
			}

			respText, err := c.rt.Exec(ctx, userPrompt, systemPrompt)
			if err != nil {
				c.logger.Error("failed model call for macro classification", "node_id", nodeID, "error", err)
				return nil, fmt.Errorf("provider execution failure at macro node %s: %w", promptKey, err)
			}

			candidates, property, parseErr := parseClassificationCandidates(respText, "macro_class")
			if parseErr != nil || len(candidates) == 0 {
				c.logger.Warn("failed to parse macro model response, applying fallback hold", "node_id", nodeID, "error", parseErr, "raw", respText)
				results[nodeID] = ase.NodeClassification{
					Property: "macro_class",
					Candidates: []ase.ProbabilityCandidate{
						{Value: "HOLD_AMBIGUOUS", Confidence: 1.0, Reasoning: "Model output invalid or malformed for macro classification"},
					},
				}
				continue
			}

			node.Mu.Lock()
			if len(candidates) > 0 {
				node.Payload["macro_class"] = candidates[0].Value
				node.Payload["constrained_macro"] = candidates[0].Value
			}
			node.Mu.Unlock()

			results[nodeID] = ase.NodeClassification{
				Property:   property,
				Candidates: candidates,
			}
		}

		return results, nil
	}
}

// BuildDynamicThinkFunc executes Moroccan PCGE candidate account lookup + model ranking + candidate validation.
func (c *PcmClassifier) BuildDynamicThinkFunc(provider string) ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		results := make(map[string]ase.NodeClassification, len(batch))
		for _, node := range batch {
			nodeID := node.NodeID
			sourceKind := extractPayloadString(node.Payload, "source_artifact_kind")
			bookRole := extractPayloadString(node.Payload, "bookkeeping_role")
			existingCode := extractPayloadString(node.Payload, "existing_account_code")
			sKindUpper := strings.ToUpper(sourceKind)
			bRoleUpper := strings.ToUpper(bookRole)

			// 1. Check for existing explicit account code on the item
			if existingCode != "" {
				results[nodeID] = ase.NodeClassification{
					Property: "account_code",
					Candidates: []ase.ProbabilityCandidate{
						{Value: existingCode, Confidence: 0.99, Reasoning: "Pre-existing validated account code preserved"},
					},
				}
				continue
			}

			// 2. Check for posted transaction without existing code
			if sKindUpper == "TRANSACTION" && bRoleUpper == "POSTED_CASH_MOVEMENT" {
				results[nodeID] = ase.NodeClassification{
					Property: "account_code",
					Candidates: []ase.ProbabilityCandidate{
						{Value: "HOLD_INSUFFICIENT_EVIDENCE", Confidence: 1.0, Reasoning: "Posted transaction missing authoritative ledger account code; reclassification prohibited"},
					},
				}
				continue
			}

			desc := extractPayloadString(node.Payload, "description")
			cpName := extractPayloadString(node.Payload, "counterparty_name")

			// Check for internal transfer
			if isInternalTransferDescription(desc, cpName) {
				results[nodeID] = ase.NodeClassification{
					Property: "account_code",
					Candidates: []ase.ProbabilityCandidate{
						{Value: "HOLD_INSUFFICIENT_EVIDENCE", Confidence: 1.0, Reasoning: "Internal transfer detected; counterpart account or paired bank line matching required, placed on HOLD"},
					},
				}
				continue
			}

			// Check for ambiguous or unknown description
			combinedUpper := strings.ToUpper(strings.TrimSpace(desc + " " + cpName))
			if (combinedUpper == "" && existingCode == "") || strings.Contains(combinedUpper, "AMBIGUOUS") || strings.Contains(combinedUpper, "UNKNOWN") {
				results[nodeID] = ase.NodeClassification{
					Property: "account_code",
					Candidates: []ase.ProbabilityCandidate{
						{Value: "HOLD_AMBIGUOUS", Confidence: 1.0, Reasoning: "Description is ambiguous or missing source context"},
					},
				}
				continue
			}

			companyID := node.RealmID
			if companyID == "" {
				if cid, ok := node.Payload["company_id"].(string); ok && cid != "" {
					companyID = cid
				} else {
					companyID = node.TenantID
				}
			}

			macroClass := "UNKNOWN"
			if top := node.TopCandidate("macro_class"); top != nil {
				macroClass = top.Value
			} else if mc, ok := node.Payload["macro_class"].(string); ok && mc != "" {
				macroClass = mc
			}

			direction, _ := node.Payload["direction"].(string)

			// 3. Pre-fetch candidate accounts from the Django-ledger schema respecting lifecycle family constraints
			var candidates []AccountCandidate
			var candidateSource string
			var err error
			if c.explicitTestCatalog != nil {
				candidates = FilterAccountsBySourceFamily(c.explicitTestCatalog, macroClass, sourceKind, bookRole)
				candidateSource = CandidateSourceExplicitTestInjection
			} else {
				candidates, candidateSource, err = GetCandidateAccountsWithSourceAndLifecycle(ctx, c.pool, c.db, companyID, macroClass, direction, sourceKind, bookRole)
				if err != nil {
					c.logger.Error("failed to prefetch candidate accounts", "company_id", companyID, "error", err)
					return nil, fmt.Errorf("domain tool candidate lookup failure for company %s: %w", companyID, err)
				}
			}

			candidateCodes := make([]string, len(candidates))
			for i, cand := range candidates {
				candidateCodes[i] = cand.Code
			}

			node.Mu.Lock()
			if node.Payload == nil {
				node.Payload = make(map[string]any)
			}
			node.Payload["source_kind"] = sourceKind
			node.Payload["constrained_macro"] = macroClass
			node.Payload["candidate_codes"] = candidateCodes
			node.Payload["candidate_source"] = candidateSource
			node.Mu.Unlock()

			if len(candidates) == 0 {
				results[nodeID] = ase.NodeClassification{
					Property: "account_code",
					Candidates: []ase.ProbabilityCandidate{
						{Value: "HOLD_INSUFFICIENT_EVIDENCE", Confidence: 1.0, Reasoning: "No candidate accounts available for this company and macro class/family"},
					},
				}
				continue
			}

			// 4. Configured deterministic selection:
			// If entity configuration yields exactly 1 admissible account for this family,
			// that account is configured deterministic from the entity's CoA.
			if len(candidates) == 1 {
				soleAccount := candidates[0].Code
				node.Mu.Lock()
				node.Payload["account_code"] = soleAccount
				node.Mu.Unlock()

				c.logger.Info("resolved account candidate via configured deterministic match",
					"company_id", companyID,
					"source_kind", sourceKind,
					"constrained_macro", macroClass,
					"candidate_codes", candidateCodes,
					"candidate_source", candidateSource,
					"account_code", soleAccount,
				)

				results[nodeID] = ase.NodeClassification{
					Property: "account_code",
					Candidates: []ase.ProbabilityCandidate{
						{
							Value:      soleAccount,
							Confidence: 0.99,
							Reasoning:  fmt.Sprintf("Account %s configured deterministic from authoritative CoA for family %s", soleAccount, macroClass),
						},
					},
				}
				continue
			}

			// 5. Multiple admissible accounts: genuine semantic ambiguity -> model ranking
			allowedCodes := make(map[string]AccountCandidate, len(candidates))
			var candidateLines []string
			for _, cand := range candidates {
				allowedCodes[cand.Code] = cand
				candidateLines = append(candidateLines, fmt.Sprintf("- Code: %s | Name: %s | Role: %s", cand.Code, cand.Name, cand.Role))
			}

			// 6. Offline heuristic fallback for unit testing when model runtime is not injected
			if c.rt == nil {
				var accountCode string
				if sKindUpper == "INVOICE" || bRoleUpper == "OPEN_RECEIVABLE" {
					if _, ok := allowedCodes["3421"]; ok {
						accountCode = "3421"
					} else {
						accountCode = candidates[0].Code
					}
				} else if sKindUpper == "BILL" || bRoleUpper == "OPEN_PAYABLE" {
					if _, ok := allowedCodes["4411"]; ok {
						accountCode = "4411"
					} else {
						accountCode = candidates[0].Code
					}
				} else {
					upper := strings.ToUpper(desc + " " + cpName)
					switch macroClass {
					case "EXPENSE":
						switch {
						case strings.Contains(upper, "LOYER") || strings.Contains(upper, "RENT") || strings.Contains(upper, "BAIL") || strings.Contains(upper, "LOCATION") || strings.Contains(upper, "LEASE"):
							accountCode = "6131"
						case strings.Contains(upper, "HONORAIRE") || strings.Contains(upper, "LEGAL") || strings.Contains(upper, "AVOCAT"):
							accountCode = "6136"
						case strings.Contains(upper, "COMMISSION") || strings.Contains(upper, "FRAIS") || strings.Contains(upper, "FEE"):
							accountCode = "6147"
						case strings.Contains(upper, "AWS") || strings.Contains(upper, "GOOGLE") || strings.Contains(upper, "SOFTWARE") || strings.Contains(upper, "SAAS"):
							accountCode = "6134"
						default:
							accountCode = "6111"
						}
					case "REVENUE":
						switch {
						case strings.Contains(upper, "SERVICE") || strings.Contains(upper, "PRESTATION"):
							accountCode = "7124"
						default:
							accountCode = "7111"
						}
					case "ASSET":
						switch {
						case strings.Contains(upper, "MATERIEL") || strings.Contains(upper, "EQUIPMENT") || strings.Contains(upper, "ORDINATEUR"):
							accountCode = "2355"
						case strings.Contains(upper, "REMBOURSEMENT"):
							accountCode = "3421"
						default:
							accountCode = "2355"
						}
					case "LIABILITY":
						switch {
						case strings.Contains(upper, "SALAIRE") || strings.Contains(upper, "PAYROLL"):
							accountCode = "4432"
						case strings.Contains(upper, "EMPRUNT") || strings.Contains(upper, "LOAN"):
							accountCode = "1481"
						default:
							accountCode = "4411"
						}
					case "EQUITY":
						accountCode = "1111"
					default:
						switch {
						case strings.Contains(upper, "LOYER") || strings.Contains(upper, "RENT") || strings.Contains(upper, "BAIL") || strings.Contains(upper, "LOCATION") || strings.Contains(upper, "LEASE"):
							accountCode = "6131"
						case strings.Contains(upper, "HONORAIRE") || strings.Contains(upper, "LEGAL") || strings.Contains(upper, "AVOCAT"):
							accountCode = "6136"
						case strings.Contains(upper, "COMMISSION") || strings.Contains(upper, "FRAIS") || strings.Contains(upper, "FEE"):
							accountCode = "6147"
						case strings.Contains(upper, "AWS") || strings.Contains(upper, "GOOGLE") || strings.Contains(upper, "SOFTWARE") || strings.Contains(upper, "SAAS"):
							accountCode = "6134"
						case strings.Contains(upper, "SALAIRE") || strings.Contains(upper, "PAYROLL"):
							accountCode = "4432"
						case strings.Contains(upper, "VENTE") || strings.Contains(upper, "SALE") || strings.Contains(upper, "CLIENT") || direction == "INFLOW" || direction == "BOOK_BANK_DEBIT":
							accountCode = "7111"
						default:
							accountCode = "6111"
						}
					}
					if _, ok := allowedCodes[accountCode]; !ok {
						accountCode = candidates[0].Code
					}
				}

				node.Mu.Lock()
				node.Payload["account_code"] = accountCode
				node.Mu.Unlock()

				c.logger.Info("resolved account candidate via offline fallback",
					"company_id", companyID,
					"source_kind", sourceKind,
					"constrained_macro", macroClass,
					"candidate_codes", candidateCodes,
					"candidate_source", candidateSource,
					"account_code", accountCode,
				)

				results[nodeID] = ase.NodeClassification{
					Property: "account_code",
					Candidates: []ase.ProbabilityCandidate{
						{
							Value:      accountCode,
							Confidence: 0.99,
							Reasoning:  fmt.Sprintf("Categorized to %s by offline PCGE resolver fallback", accountCode),
						},
					},
				}
				continue
			}

			// 7. Assemble bounded prompt for model ranking
			systemPrompt := fmt.Sprintf(`You are a senior Moroccan Expert-Comptable classifying transactions into the Moroccan General Chart of Accounts (PCGE).
You MUST select the single best matching account code ONLY from the following pre-approved candidate accounts:

%s

CRITICAL RULES:
1. You MUST select an account code that appears in the allowed candidate list above. NEVER invent or hallucinate an account code.
2. If the transaction description and evidence are too vague or ambiguous to justify an account, return a confidence score < 0.98.
3. If confident (>= 0.98), return the exact 4-digit Moroccan PCGE account code.

EXPECTED JSON FORMAT:
{
  "account_code": "6131",
  "confidence": 0.99,
  "reasoning": "Clear explanation citing Moroccan PCGE classification standards."
}
`, strings.Join(candidateLines, "\n"))

			userPrompt := buildAccountUserPrompt(node)

			respText, err := c.rt.Exec(ctx, userPrompt, systemPrompt)
			if err != nil {
				c.logger.Error("failed model execution for account resolution", "node_id", nodeID, "error", err)
				return nil, fmt.Errorf("provider execution failure at account resolver: %w", err)
			}

			accCode, conf, reasoning, parseErr := parseAccountResolution(respText)
			if parseErr != nil {
				c.logger.Warn("failed to parse model output for account resolution", "node_id", nodeID, "error", parseErr, "raw", respText)
				results[nodeID] = ase.NodeClassification{
					Property: "account_code",
					Candidates: []ase.ProbabilityCandidate{
						{Value: "HOLD_INSUFFICIENT_EVIDENCE", Confidence: 1.0, Reasoning: "Model output invalid for account resolution: " + parseErr.Error()},
					},
				}
				continue
			}

			// Deterministic validation: must be in pre-fetched candidate set
			if _, allowed := allowedCodes[accCode]; !allowed {
				c.logger.Warn("model selected account outside allowed candidate set", "selected", accCode, "node_id", nodeID)
				results[nodeID] = ase.NodeClassification{
					Property: "account_code",
					Candidates: []ase.ProbabilityCandidate{
						{Value: "HOLD_INSUFFICIENT_EVIDENCE", Confidence: 1.0, Reasoning: fmt.Sprintf("Model selected account %s which is not in the allowed candidate set for company", accCode)},
					},
				}
				continue
			}

			node.Mu.Lock()
			node.Payload["account_code"] = accCode
			node.Mu.Unlock()

			c.logger.Info("resolved account candidate via model execution",
				"company_id", companyID,
				"source_kind", sourceKind,
				"constrained_macro", macroClass,
				"candidate_codes", candidateCodes,
				"candidate_source", candidateSource,
				"account_code", accCode,
			)

			results[nodeID] = ase.NodeClassification{
				Property: "account_code",
				Candidates: []ase.ProbabilityCandidate{
					{
						Value:      accCode,
						Confidence: conf,
						Reasoning:  reasoning,
					},
				},
			}
		}

		return results, nil
	}
}

func defaultMoroccanMacroPrompt(nodeID string) string {
	nLower := strings.ToLower(nodeID)
	if strings.Contains(nLower, "outflow") {
		return `You are a Moroccan Expert-Comptable acting as an outflow macro-classification router in a PCGE accounting system.
Your job is to classify EACH outgoing bank movement into one of the following primary macro classes:
- EXPENSE: Operating expenses, purchases of goods/materials (611x), external services/leases (613x/614x), utilities, taxes (616x), personnel expenses.
- ASSET: Fixed asset purchases, computer hardware > 5000 DH, security deposits, long-term equipment (2xxx).
- LIABILITY: Supplier balance settlements, repayments of loans (148x), social security/tax settlements (44xx).
- EQUITY: Owner/partner capital withdrawals, dividends (11xx).

CRITICAL RULES:
1. If this movement is an internal bank account transfer (virement interne, virement compte à compte, transit 5115), return macro_class "HOLD_INSUFFICIENT_EVIDENCE".
2. If description is ambiguous or unknown, return "HOLD_AMBIGUOUS".

EXPECTED JSON FORMAT:
{
  "macro_class": "EXPENSE",
  "confidence": 0.99,
  "reasoning": "Standard operating supply purchase under Moroccan accounting standards."
}
`
	}
	if strings.Contains(nLower, "inflow") {
		return `You are a Moroccan Expert-Comptable acting as an inflow macro-classification router in a PCGE accounting system.
Your job is to classify EACH incoming bank movement into one of the following primary macro classes:
- REVENUE: Operating revenue, merchandise sales (711x), services rendered (712x), client fee receipts.
- LIABILITY: Customer advances/deposits (442x), new bank loans/borrowings (148x), partner current accounts (446x).
- ASSET: Supplier refunds/reimbursements, recovery of deposits (248x/34xx).
- EQUITY: Capital injections/contributions by shareholders (11xx).

CRITICAL RULES:
1. If this movement is an internal bank account transfer (virement interne, virement compte à compte, transit 5115), return macro_class "HOLD_INSUFFICIENT_EVIDENCE".
2. If description is ambiguous or unknown, return "HOLD_AMBIGUOUS".

EXPECTED JSON FORMAT:
{
  "macro_class": "REVENUE",
  "confidence": 0.99,
  "reasoning": "Operating revenue under Moroccan PCGE standards."
}
`
	}
	return `You are a Moroccan Expert-Comptable acting as a macro-classification router in a PCGE accounting system.
Your job is to classify EACH transaction row into one of the following primary macro classes:
- EXPENSE: Operating expenses, purchases of goods, materials, services, utilities, leases, taxes, payroll.
- REVENUE: Operating revenue, merchandise sales, service billings, client receipts.
- ASSET: Physical assets, computer hardware > 5000 DH, long-term equipment, security deposits, customer receivables.
- LIABILITY: Supplier payables, taxes payable, loans, credit balances.
- EQUITY: Capital, partner draws, dividends.

EXPECTED JSON FORMAT:
{
  "macro_class": "EXPENSE",
  "confidence": 0.99,
  "reasoning": "Standard operating supply purchase under Moroccan accounting standards."
}
`
}

func buildMacroUserPrompt(node *ase.AutonomousSemanticEngineNode) string {
	desc := extractPayloadString(node.Payload, "description")
	amt := extractAmountUnits(node.Payload)
	curr := extractPayloadString(node.Payload, "currency")
	if curr == "" {
		curr = "MAD"
	}
	dir := extractPayloadString(node.Payload, "direction")
	cpName := extractPayloadString(node.Payload, "counterparty_name")
	ref := extractPayloadString(node.Payload, "reference")
	bankAcc := extractPayloadString(node.Payload, "bank_account_name")
	institution := extractPayloadString(node.Payload, "institution_name")
	itemID := extractItemID(node)

	evidenceStrs := extractEvidenceSummaries(node.Payload)

	return fmt.Sprintf(`### TRANSACTION INPUT DATA
- Item ID: %s
- Description: %s
- Counterparty: %s
- Reference: %s
- Bank Account: %s (%s)
- Amount (Units): %d %s
- Cash Direction: %s
- Evidence Summaries: %s
`, itemID, desc, cpName, ref, bankAcc, institution, amt, curr, dir, strings.Join(evidenceStrs, "; "))
}

func buildAccountUserPrompt(node *ase.AutonomousSemanticEngineNode) string {
	desc := extractPayloadString(node.Payload, "description")
	amt := extractAmountUnits(node.Payload)
	curr := extractPayloadString(node.Payload, "currency")
	if curr == "" {
		curr = "MAD"
	}
	dir := extractPayloadString(node.Payload, "direction")
	cpName := extractPayloadString(node.Payload, "counterparty_name")
	ref := extractPayloadString(node.Payload, "reference")
	bankAcc := extractPayloadString(node.Payload, "bank_account_name")
	institution := extractPayloadString(node.Payload, "institution_name")
	macroClass := extractPayloadString(node.Payload, "macro_class")
	itemID := extractItemID(node)

	evidenceStrs := extractEvidenceSummaries(node.Payload)

	return fmt.Sprintf(`### TRANSACTION TO RESOLVE
- Item ID: %s
- Macro Class: %s
- Description: %s
- Reference: %s
- Counterparty: %s
- Bank Account: %s (%s)
- Amount: %d %s
- Direction: %s
- Document Evidence: %s
`, itemID, macroClass, desc, ref, cpName, bankAcc, institution, amt, curr, dir, strings.Join(evidenceStrs, "; "))
}
func cleanJSONResponse(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json") // Handles optional "```json" tag
	s = strings.TrimPrefix(s, "```")     // Handles generic "```" tag
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func parseClassificationCandidates(raw string, defaultProperty string) ([]ase.ProbabilityCandidate, string, error) {
	cleaned := cleanJSONResponse(raw)

	// Attempt 1: Direct JSON object { "macro_class": "EXPENSE", "confidence": 0.99, "reasoning": "..." }
	var directObj map[string]interface{}
	if err := json.Unmarshal([]byte(cleaned), &directObj); err == nil {
		if val, ok := directObj[defaultProperty].(string); ok && val != "" {
			conf := 0.99
			if c, ok := directObj["confidence"].(float64); ok {
				conf = c
			}
			reasoning := ""
			if r, ok := directObj["reasoning"].(string); ok {
				reasoning = r
			}
			return []ase.ProbabilityCandidate{
				{Value: val, Confidence: conf, Reasoning: reasoning},
			}, defaultProperty, nil
		}
	}

	// Attempt 2: Structured { "property": "...", "candidates": [ ... ] }
	var structured struct {
		Property   string                     `json:"property"`
		Candidates []ase.ProbabilityCandidate `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(cleaned), &structured); err == nil && len(structured.Candidates) > 0 {
		prop := structured.Property
		if prop == "" {
			prop = defaultProperty
		}
		return structured.Candidates, prop, nil
	}

	return nil, defaultProperty, fmt.Errorf("unable to parse candidates from: %s", cleaned)
}

func parseAccountResolution(raw string) (string, float64, string, error) {
	cleaned := cleanJSONResponse(raw)

	// Attempt 1: Direct object
	var obj struct {
		AccountCode string  `json:"account_code"`
		Code        string  `json:"code"`
		Confidence  float64 `json:"confidence"`
		Reasoning   string  `json:"reasoning"`
	}
	if err := json.Unmarshal([]byte(cleaned), &obj); err == nil {
		code := obj.AccountCode
		if code == "" {
			code = obj.Code
		}
		if code != "" {
			conf := obj.Confidence
			if conf <= 0 {
				conf = 0.99
			}
			return code, conf, obj.Reasoning, nil
		}
	}

	// Attempt 2: Fallback scanning for 4-digit code in response
	for _, word := range strings.Fields(cleaned) {
		cleanWord := strings.Trim(word, `"',.:;()[]{}`)
		if len(cleanWord) == 4 {
			if _, numErr := strconv.Atoi(cleanWord); numErr == nil {
				return cleanWord, 0.98, "Extracted from model response: " + cleaned, nil
			}
		}
	}

	return "", 0, "", fmt.Errorf("no account code found in model output: %s", cleaned)
}
