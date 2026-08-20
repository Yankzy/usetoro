package pcm_cash

import (
	"encoding/json"
	"strings"

	"github.com/Yankzy/usetoro/internal/services/enrichment"
)

type ComplianceStatus string

const (
	StatusCompliant      ComplianceStatus = "COMPLIANT"
	StatusActionRequired ComplianceStatus = "ACTION_REQUIRED"
	StatusWarning        ComplianceStatus = "WARNING"
)

// TransactionAnnotation holds Moroccan statutory compliance metadata for a transaction.
// These fields are populated from the node-level AnnotationTemplate injected by the
// DAG engine when a transaction enters a holding gate.
type TransactionAnnotation struct {
	ComplianceStatus ComplianceStatus `json:"compliance_status"`
	DocumentRequired string           `json:"document_required"`
	TaxRuleCode      string           `json:"tax_rule_code"`
	Instruction      string           `json:"instruction"`
	TaxImpact        string           `json:"tax_impact"`
	HasValidICE      bool             `json:"has_valid_ice"`
	ExtractedICE     string           `json:"extracted_ice,omitempty"`
}

// AnnotationFromPayload builds a TransactionAnnotation from ExportRecord fields
// that were injected by the DAG engine at the node level. If the record doesn't
// carry node annotations, it returns a minimal compliant annotation with ICE
// extracted from the enrichment envelope.
func AnnotationFromPayload(rec ExportRecord) TransactionAnnotation {
	ice, hasValidICE := ExtractICEFromEnrichment(rec.MoroccanEnrichment)

	ann := TransactionAnnotation{
		ComplianceStatus: StatusCompliant,
		HasValidICE:      hasValidICE,
		ExtractedICE:     ice,
	}

	// Node-injected fields take precedence
	if rec.TaxRuleCode != "" {
		ann.TaxRuleCode = rec.TaxRuleCode
	}
	if rec.DocumentRequired != "" {
		ann.DocumentRequired = rec.DocumentRequired
	}
	if rec.Instruction != "" {
		ann.Instruction = rec.Instruction
	}
	if rec.HoldReason != "" && ann.Instruction == "" {
		ann.Instruction = rec.HoldReason
	}

	// Derive compliance status from node-injected severity or hold state
	switch strings.ToUpper(rec.ComplianceStatus) {
	case "BLOCKING", "ACTION_REQUIRED":
		ann.ComplianceStatus = StatusActionRequired
	case "WARNING":
		ann.ComplianceStatus = StatusWarning
	default:
		if rec.HoldReason != "" {
			ann.ComplianceStatus = StatusActionRequired
		}
	}

	return ann
}

// ExtractICEFromEnrichment parses the Moroccan enrichment envelope and returns
// the counterparty ICE and whether it is valid.
func ExtractICEFromEnrichment(enrichmentJSON []byte) (string, bool) {
	if len(enrichmentJSON) == 0 || string(enrichmentJSON) == "{}" {
		return "", false
	}
	var env enrichment.AnnotatedMoroccanTransactionEnvelope
	if err := json.Unmarshal(enrichmentJSON, &env); err != nil {
		return "", false
	}
	if env.Counterparty.Identifiers.ICE == nil {
		return "", false
	}
	ice := *env.Counterparty.Identifiers.ICE
	return ice, ValidateICE(ice)
}
