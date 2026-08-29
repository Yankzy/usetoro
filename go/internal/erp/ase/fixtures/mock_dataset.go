package fixtures

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Yankzy/usetoro/internal/erp/ase"
	"github.com/Yankzy/usetoro/internal/services/enrichment"
	"github.com/google/uuid"
)

//go:embed pcm_mock_transactions.json
var mockTransactionsJSON []byte

// MockTransaction defines a test scenario matching a specific DAG child edge.
type MockTransaction struct {
	EdgeKey           string `json:"edge_key"`
	ParentNode        string `json:"parent_node"`
	CashDirection     string `json:"cash_direction"`
	ExpectedChildNode string `json:"expected_child_node"`
	RawDescription    string `json:"raw_description"`
	RawAmount         string `json:"raw_amount"`
	Currency          string `json:"currency"`
	CounterpartyName  string `json:"counterparty_name"`
	ICENumber         string `json:"ice_number"`
	Notes             string `json:"notes"`
}

// LoadMockTransactions returns all 101 curated mock transactions.
func LoadMockTransactions() ([]MockTransaction, error) {
	var list []MockTransaction
	if err := json.Unmarshal(mockTransactionsJSON, &list); err != nil {
		return nil, fmt.Errorf("failed to unmarshal mock transactions: %w", err)
	}
	return list, nil
}

// GetMockTransactionByEdge retrieves a specific mock transaction by its allowed edge key.
func GetMockTransactionByEdge(edgeKey string) (*MockTransaction, error) {
	all, err := LoadMockTransactions()
	if err != nil {
		return nil, err
	}
	for _, tx := range all {
		if strings.EqualFold(tx.EdgeKey, edgeKey) {
			return &tx, nil
		}
	}
	return nil, fmt.Errorf("no mock transaction found for edge key: %s", edgeKey)
}

// FilterByDirection returns mock transactions for INFLOW or OUTFLOW.
func FilterByDirection(direction string) ([]MockTransaction, error) {
	all, err := LoadMockTransactions()
	if err != nil {
		return nil, err
	}
	var filtered []MockTransaction
	for _, tx := range all {
		if strings.EqualFold(tx.CashDirection, direction) {
			filtered = append(filtered, tx)
		}
	}
	return filtered, nil
}

// ToASENode converts a MockTransaction into an *ase.AutonomousSemanticEngineNode in-memory.
func (m *MockTransaction) ToASENode(dagName, sessionID string) *ase.AutonomousSemanticEngineNode {
	if dagName == "" {
		dagName = "pcm_bank_cash_accounting_dag"
	}
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	nodeID := uuid.New().String()
	currency := m.Currency
	if currency == "" {
		currency = "MAD"
	}

	var icePtr *string
	if m.ICENumber != "" {
		val := m.ICENumber
		icePtr = &val
	}

	enrichmentEnvelope := enrichment.AnnotatedMoroccanTransactionEnvelope{
		Counterparty: enrichment.CounterpartyInfo{
			NormalizedName: m.CounterpartyName,
			Identifiers: enrichment.TaxIdentifiers{
				ICE: icePtr,
			},
		},
		PCGMAccounting: enrichment.PCGMInfo{
			AccountLabel: m.Notes,
		},
	}

	payload := map[string]any{
		"raw_description":        m.RawDescription,
		"description":            m.RawDescription,
		"cash_direction":         m.CashDirection,
		"raw_amount":             m.RawAmount,
		"currency":               currency,
		"operation_date":         time.Now().Format("2006-01-02"),
		"domain_tool":            "pcm_cash_accounting",
		"session_id":             sessionID,
		"staging_transaction_id": nodeID,
		"statement_type":         "BANK_STATEMENT",
		"account_code":           "514100",
		"enrichment_envelope":    enrichmentEnvelope,
		"normalized_merchant":    m.CounterpartyName,
		"ice_number":             m.ICENumber,
		"target_edge_key":        m.EdgeKey,
	}

	node := ase.NewASENode("default_user", dagName, payload)
	node.NodeID = nodeID
	node.TenantID = "default_user"
	node.UserID = "default_user"
	return node
}
