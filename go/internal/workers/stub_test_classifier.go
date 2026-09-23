package workers

import (
	"context"
	"fmt"
	"strings"

	"github.com/Yankzy/usetoro/internal/erp/ase"
)

// StubTestClassifier is an explicit test double implementing ase.Classifier.
// It is strictly permitted in unit tests and local simulations; production execution
// MUST resolve the real DomainTool and model runtime from domain_tools registry.
type StubTestClassifier struct{}

func NewStubTestClassifier() *StubTestClassifier {
	return &StubTestClassifier{}
}

func (s *StubTestClassifier) BuildPayloadRouterThinkFunc(payloadKey string) ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		results := make(map[string]ase.NodeClassification, len(batch))
		for _, node := range batch {
			desc, _ := node.Payload["description"].(string)
			descUpper := strings.ToUpper(strings.TrimSpace(desc))
			hasExistingCode := false
			if code, ok := node.Payload["existing_account_code"].(*string); ok && code != nil && *code != "" {
				hasExistingCode = true
			} else if codeStr, ok := node.Payload["existing_account_code"].(string); ok && codeStr != "" {
				hasExistingCode = true
			}

			if (descUpper == "" && !hasExistingCode) ||
				strings.Contains(descUpper, "AMBIGUOUS") ||
				strings.Contains(descUpper, "AMBIGU") ||
				strings.Contains(descUpper, "UNKNOWN") ||
				strings.Contains(descUpper, "SANS OBJET") ||
				strings.Contains(descUpper, "NON DOCUMENTE") ||
				strings.Contains(descUpper, "HOLD") {
				results[node.NodeID] = ase.NodeClassification{
					Property: "direction",
					Candidates: []ase.ProbabilityCandidate{
						{Value: "HOLD_AMBIGUOUS", Confidence: 1.0, Reasoning: "Description is ambiguous or missing source context"},
					},
				}
				continue
			}

			direction, _ := node.Payload["direction"].(string)
			routeDir := "OUTFLOW"
			if direction == "INFLOW" || direction == "BOOK_BANK_DEBIT" {
				routeDir = "INFLOW"
			}

			results[node.NodeID] = ase.NodeClassification{
				Property: "direction",
				Candidates: []ase.ProbabilityCandidate{
					{Value: routeDir, Confidence: 0.99, Reasoning: "Stub deterministic routing based on direction: " + routeDir},
				},
			}
		}
		return results, nil
	}
}

func (s *StubTestClassifier) BuildGenericThinkFunc(promptKey string) ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		results := make(map[string]ase.NodeClassification, len(batch))
		for _, node := range batch {
			macroClass := "EXPENSE"
			if strings.Contains(promptKey, "inflow") {
				macroClass = "REVENUE"
			}
			results[node.NodeID] = ase.NodeClassification{
				Property: "macro_class",
				Candidates: []ase.ProbabilityCandidate{
					{Value: macroClass, Confidence: 0.99, Reasoning: "Stub macro classification for " + promptKey},
				},
			}
		}
		return results, nil
	}
}

func (s *StubTestClassifier) BuildDynamicThinkFunc(provider string) ase.ThinkFunc {
	return func(ctx context.Context, batch []*ase.AutonomousSemanticEngineNode) (map[string]ase.NodeClassification, error) {
		results := make(map[string]ase.NodeClassification, len(batch))
		for _, node := range batch {
			var accountCode string
			if codePtr, ok := node.Payload["existing_account_code"].(*string); ok && codePtr != nil && *codePtr != "" {
				accountCode = *codePtr
			} else if codeStr, ok := node.Payload["existing_account_code"].(string); ok && codeStr != "" {
				accountCode = codeStr
			} else {
				desc, _ := node.Payload["description"].(string)
				upper := strings.ToUpper(desc)
				direction, _ := node.Payload["direction"].(string)

				switch {
				case strings.Contains(upper, "AMBIGUOUS") || strings.Contains(upper, "AMBIGU") || strings.Contains(upper, "UNKNOWN") || strings.Contains(upper, "SANS OBJET") || strings.Contains(upper, "NON DOCUMENTE") || strings.Contains(upper, "HOLD"):
					accountCode = "HOLD_AMBIGUOUS"
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
				case strings.Contains(upper, "TVA") || strings.Contains(upper, "DGI") || strings.Contains(upper, "TAX"):
					accountCode = "4456"
				case strings.Contains(upper, "ACCESSOIRES"):
					accountCode = "7127"
				case strings.Contains(upper, "VENTE") || strings.Contains(upper, "SALE") || strings.Contains(upper, "CLIENT") || direction == "INFLOW" || direction == "BOOK_BANK_DEBIT":
					accountCode = "7111"
				default:
					accountCode = "6111"
				}
			}

			node.Mu.Lock()
			if node.Payload == nil {
				node.Payload = make(map[string]any)
			}
			node.Payload["account_code"] = accountCode
			node.Mu.Unlock()

			results[node.NodeID] = ase.NodeClassification{
				Property: "account_code",
				Candidates: []ase.ProbabilityCandidate{
					{
						Value:      accountCode,
						Confidence: 0.99,
						Reasoning:  fmt.Sprintf("Categorized to %s by stub pcge_account_resolver", accountCode),
					},
				},
			}
		}
		return results, nil
	}
}
