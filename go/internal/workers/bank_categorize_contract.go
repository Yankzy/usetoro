package workers

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	BankCategorizeSchemaVersion = "bookkeeping.ase.bank_categorize.v1"
	BankCategorizeDagID         = "bookkeeping_bank_categorization_v1"
	BankCategorizeSubject       = "worker.inbox.bookkeeping_ase_bank_categorizer"
	BankCategorizeQueueGroup    = "bookkeeping_ase_bank_categorizer_group"
)

// BankCategorizeItem represents one residual bank movement in the wire contract.
type BankCategorizeItem struct {
	BankItemID          string   `json:"bank_item_id"`
	BankAccountID       string   `json:"bank_account_id"`
	ResidualAmountUnits int64    `json:"residual_amount_units"`
	OriginalAmountUnits int64    `json:"original_amount_units"`
	Direction           string   `json:"direction"`
	Date                string   `json:"date"`
	Currency            string   `json:"currency"`
	Description         string   `json:"description"`
	Reference           *string  `json:"reference,omitempty"`
	ProvenanceRefs      []string `json:"provenance_refs,omitempty"`
	BankAccountName     *string  `json:"bank_account_name,omitempty"`
	InstitutionName     *string  `json:"institution_name,omitempty"`
	CounterpartyName    *string  `json:"counterparty_name,omitempty"`
}

// Validate verifies constraints for a single BankCategorizeItem.
func (it *BankCategorizeItem) Validate() error {
	if !strings.HasPrefix(it.BankItemID, "staged:") {
		return fmt.Errorf("bank_item_id must start with 'staged:', got %q", it.BankItemID)
	}
	if it.BankAccountID == "" {
		return errors.New("bank_account_id is required")
	}
	if it.ResidualAmountUnits <= 0 {
		return fmt.Errorf("residual_amount_units must be positive, got %d", it.ResidualAmountUnits)
	}
	if it.OriginalAmountUnits <= 0 {
		return fmt.Errorf("original_amount_units must be positive, got %d", it.OriginalAmountUnits)
	}
	if it.ResidualAmountUnits > it.OriginalAmountUnits {
		return fmt.Errorf("residual_amount_units (%d) cannot exceed original_amount_units (%d)", it.ResidualAmountUnits, it.OriginalAmountUnits)
	}
	if it.Direction != "INFLOW" && it.Direction != "OUTFLOW" {
		return fmt.Errorf("direction must be 'INFLOW' or 'OUTFLOW', got %q", it.Direction)
	}
	if len(it.Currency) != 3 {
		return fmt.Errorf("currency must be 3 characters, got %q", it.Currency)
	}
	if it.Description == "" {
		return errors.New("description is required")
	}
	return nil
}

// BankCategorizeRequest represents the versioned bank categorization request envelope.
type BankCategorizeRequest struct {
	SchemaVersion       string               `json:"schema_version"`
	RequestID           string               `json:"request_id"`
	IdempotencyKey      string               `json:"idempotency_key"`
	CompanyID           string               `json:"company_id"`
	SessionID           string               `json:"session_id"`
	StateRevision       int                  `json:"state_revision"`
	PersistenceRevision int                  `json:"persistence_revision"`
	DagID               string               `json:"dag_id"`
	RequestedAt         string               `json:"requested_at"`
	BankItems           []BankCategorizeItem `json:"bank_items"`
}

// Validate verifies constraints for BankCategorizeRequest.
func (req *BankCategorizeRequest) Validate() error {
	if req.SchemaVersion != BankCategorizeSchemaVersion {
		return fmt.Errorf("invalid schema_version: expected %q, got %q", BankCategorizeSchemaVersion, req.SchemaVersion)
	}
	seen := make(map[string]struct{})
	for _, it := range req.BankItems {
		if _, exists := seen[it.BankItemID]; exists {
			return fmt.Errorf("duplicate bank_item_id in request: %q", it.BankItemID)
		}
		seen[it.BankItemID] = struct{}{}
		if err := it.Validate(); err != nil {
			return fmt.Errorf("invalid item %q: %w", it.BankItemID, err)
		}
	}
	return nil
}

// ComputeBankRequestPayloadCanonicalBytes computes deterministically ordered UTF-8 canonical JSON bytes.
func ComputeBankRequestPayloadCanonicalBytes(req BankCategorizeRequest) ([]byte, error) {
	items := make([]BankCategorizeItem, len(req.BankItems))
	copy(items, req.BankItems)
	sort.Slice(items, func(i, j int) bool {
		return items[i].BankItemID < items[j].BankItemID
	})

	canonicalItems := make([]any, len(items))
	for idx, it := range items {
		// Normalize optionals
		ref := ""
		if it.Reference != nil {
			ref = *it.Reference
		}
		bankAccName := ""
		if it.BankAccountName != nil {
			bankAccName = *it.BankAccountName
		}
		instName := ""
		if it.InstitutionName != nil {
			instName = *it.InstitutionName
		}
		cpName := ""
		if it.CounterpartyName != nil {
			cpName = *it.CounterpartyName
		}

		// Sort provenance refs
		refSet := make(map[string]struct{})
		for _, r := range it.ProvenanceRefs {
			refSet[r] = struct{}{}
		}
		uniqueRefs := make([]string, 0, len(refSet))
		for r := range refSet {
			uniqueRefs = append(uniqueRefs, r)
		}
		sort.Strings(uniqueRefs)

		canonicalItems[idx] = map[string]any{
			"bank_account_id":       it.BankAccountID,
			"bank_account_name":     bankAccName,
			"bank_item_id":          it.BankItemID,
			"counterparty_name":     cpName,
			"currency":              it.Currency,
			"date":                  it.Date,
			"description":           it.Description,
			"direction":             it.Direction,
			"institution_name":      instName,
			"original_amount_units": it.OriginalAmountUnits,
			"provenance_refs":       uniqueRefs,
			"reference":             ref,
			"residual_amount_units": it.ResidualAmountUnits,
		}
	}

	canonicalData := map[string]any{
		"company_id":           req.CompanyID,
		"dag_id":               req.DagID,
		"items":                canonicalItems,
		"persistence_revision": req.PersistenceRevision,
		"schema_version":       req.SchemaVersion,
		"session_id":           req.SessionID,
		"state_revision":       req.StateRevision,
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(canonicalData); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// ComputeBankRequestPayloadDigest computes 64-hex SHA-256 digest of the canonical semantic bank request.
func ComputeBankRequestPayloadDigest(req BankCategorizeRequest) (string, error) {
	b, err := ComputeBankRequestPayloadCanonicalBytes(req)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return fmt.Sprintf("%x", h), nil
}

// BankCategorizeOutcome represents a terminal CLASSIFIED or HOLD outcome.
type BankCategorizeOutcome struct {
	BankItemID       string   `json:"bank_item_id"`
	Status           string   `json:"status"` // "CLASSIFIED" | "HOLD"
	AccountCode      *string  `json:"account_code,omitempty"`
	Confidence       *float64 `json:"confidence,omitempty"`
	Rationale        *string  `json:"rationale,omitempty"`
	EvidenceRefs     []string `json:"evidence_refs,omitempty"`
	AseNodeID        *string  `json:"ase_node_id,omitempty"`
	TerminalProperty *string  `json:"terminal_property,omitempty"`
	HoldReason       *string  `json:"hold_reason,omitempty"`
	RequiredEvidence []string `json:"required_evidence,omitempty"`
	CandidateSource  *string  `json:"candidate_source,omitempty"`
	ConstrainedMacro *string  `json:"constrained_macro,omitempty"`
	CandidateCodes   []string `json:"candidate_codes,omitempty"`
}

// Validate verifies constraints for BankCategorizeOutcome.
func (out *BankCategorizeOutcome) Validate() error {
	if !strings.HasPrefix(out.BankItemID, "staged:") {
		return fmt.Errorf("outcome bank_item_id must start with 'staged:', got %q", out.BankItemID)
	}
	if out.Status == "CLASSIFIED" {
		if out.AccountCode == nil || strings.TrimSpace(*out.AccountCode) == "" {
			return errors.New("CLASSIFIED outcome requires account_code")
		}
		if out.HoldReason != nil && strings.TrimSpace(*out.HoldReason) != "" {
			return errors.New("CLASSIFIED outcome must not specify hold_reason")
		}
	} else if out.Status == "HOLD" {
		if out.AccountCode != nil && strings.TrimSpace(*out.AccountCode) != "" {
			return errors.New("HOLD outcome must not specify account_code")
		}
		if out.HoldReason == nil || strings.TrimSpace(*out.HoldReason) == "" {
			return errors.New("HOLD outcome requires hold_reason")
		}
	} else {
		return fmt.Errorf("invalid outcome status: %q (expected CLASSIFIED or HOLD)", out.Status)
	}
	return nil
}

// BankCategorizeResponse represents the versioned bank categorization response envelope.
type BankCategorizeResponse struct {
	SchemaVersion  string                  `json:"schema_version"`
	RequestID      string                  `json:"request_id"`
	IdempotencyKey string                  `json:"idempotency_key"`
	SessionID      string                  `json:"session_id"`
	StateRevision  int                     `json:"state_revision"`
	DagID          string                  `json:"dag_id"`
	DagRunID       string                  `json:"dag_run_id,omitempty"`
	Status         string                  `json:"status"`
	Outcomes       []BankCategorizeOutcome `json:"outcomes"`
	ProviderIssues []ProviderIssue         `json:"provider_issues,omitempty"`
}

// Validate verifies constraints for BankCategorizeResponse.
func (resp *BankCategorizeResponse) Validate() error {
	if resp.SchemaVersion != BankCategorizeSchemaVersion {
		return fmt.Errorf("invalid schema_version in response: expected %q, got %q", BankCategorizeSchemaVersion, resp.SchemaVersion)
	}
	seen := make(map[string]struct{})
	for _, o := range resp.Outcomes {
		if _, exists := seen[o.BankItemID]; exists {
			return fmt.Errorf("duplicate bank_item_id in response outcomes: %q", o.BankItemID)
		}
		seen[o.BankItemID] = struct{}{}
		if err := o.Validate(); err != nil {
			return fmt.Errorf("invalid outcome for item %q: %w", o.BankItemID, err)
		}
	}
	return nil
}
