package quickbooks

import (
	"encoding/json"
)

// JournalEntry represents a Journal Entry in QuickBooks Online.
type JournalEntry struct {
	Id          string             `json:"Id,omitempty"`
	SyncToken   string             `json:"SyncToken,omitempty"`
	MetaData    MetaData           `json:"MetaData,omitempty"`
	DocNumber   string             `json:"DocNumber,omitempty"`
	TxnDate     Date               `json:"TxnDate,omitempty"`
	CurrencyRef ReferenceType      `json:"CurrencyRef,omitempty"`
	PrivateNote string             `json:"PrivateNote,omitempty"`
	Line        []JournalEntryLine `json:"Line"`
}

// JournalEntryLine represents a single debit or credit line in a Journal Entry.
type JournalEntryLine struct {
	Id                     string                 `json:"Id,omitempty"`
	Description            string                 `json:"Description,omitempty"`
	Amount                 json.Number            `json:"Amount,omitempty"`
	DetailType             string                 `json:"DetailType,omitempty"` // "JournalEntryLineDetail"
	JournalEntryLineDetail JournalEntryLineDetail `json:"JournalEntryLineDetail,omitempty"`
}

// JournalEntryLineDetail contains specific detail for a JournalEntryLine.
type JournalEntryLineDetail struct {
	PostingType string        `json:"PostingType,omitempty"` // "Debit" or "Credit"
	AccountRef  ReferenceType `json:"AccountRef,omitempty"`
}
