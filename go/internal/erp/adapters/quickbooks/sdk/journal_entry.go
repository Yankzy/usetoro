package quickbooks

import (
	"encoding/json"
	"errors"
	"strconv"
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

// CreateJournalEntry creates the given JournalEntry on the QuickBooks server, returning
// the resulting JournalEntry object.
func (c *Client) CreateJournalEntry(journalEntry *JournalEntry) (*JournalEntry, error) {
	var resp struct {
		JournalEntry JournalEntry
		Time         Date
	}

	if err := c.post("journalentry", journalEntry, &resp, nil); err != nil {
		return nil, err
	}

	return &resp.JournalEntry, nil
}

// DeleteJournalEntry deletes the journal entry
func (c *Client) DeleteJournalEntry(journalEntry *JournalEntry) error {
	if journalEntry.Id == "" || journalEntry.SyncToken == "" {
		return errors.New("missing id/sync token")
	}

	return c.post("journalentry", journalEntry, nil, map[string]string{"operation": "delete"})
}

// FindJournalEntries gets the full list of JournalEntries in the QuickBooks account.
func (c *Client) FindJournalEntries() ([]JournalEntry, error) {
	var resp struct {
		QueryResponse struct {
			JournalEntries []JournalEntry `json:"JournalEntry"`
			MaxResults     int
			StartPosition  int
			TotalCount     int
		}
	}

	if err := c.Query("SELECT COUNT(*) FROM JournalEntry", &resp); err != nil {
		return nil, err
	}

	if resp.QueryResponse.TotalCount == 0 {
		return nil, nil
	}

	journalEntries := make([]JournalEntry, 0, resp.QueryResponse.TotalCount)

	for i := 0; i < resp.QueryResponse.TotalCount; i += queryPageSize {
		query := "SELECT * FROM JournalEntry ORDERBY Id STARTPOSITION " + strconv.Itoa(i+1) + " MAXRESULTS " + strconv.Itoa(queryPageSize)

		if err := c.Query(query, &resp); err != nil {
			return nil, err
		}

		if resp.QueryResponse.JournalEntries == nil {
			return nil, errors.New("no journal entries could be found")
		}

		journalEntries = append(journalEntries, resp.QueryResponse.JournalEntries...)
	}

	return journalEntries, nil
}

// FindJournalEntryById finds the journal entry by the given id
func (c *Client) FindJournalEntryById(id string) (*JournalEntry, error) {
	var resp struct {
		JournalEntry JournalEntry
		Time         Date
	}

	if err := c.get("journalentry/"+id, &resp, nil); err != nil {
		return nil, err
	}

	return &resp.JournalEntry, nil
}

// QueryJournalEntries accepts an SQL query and returns all journal entries found using it
func (c *Client) QueryJournalEntries(query string) ([]JournalEntry, error) {
	var resp struct {
		QueryResponse struct {
			JournalEntries []JournalEntry `json:"JournalEntry"`
			StartPosition  int
			MaxResults     int
		}
	}

	if err := c.Query(query, &resp); err != nil {
		return nil, err
	}

	if resp.QueryResponse.JournalEntries == nil {
		return nil, errors.New("could not find any journal entries")
	}

	return resp.QueryResponse.JournalEntries, nil
}

// UpdateJournalEntry updates the journal entry
func (c *Client) UpdateJournalEntry(journalEntry *JournalEntry) (*JournalEntry, error) {
	if journalEntry.Id == "" {
		return nil, errors.New("missing journal entry id")
	}

	existingJournalEntry, err := c.FindJournalEntryById(journalEntry.Id)
	if err != nil {
		return nil, err
	}

	journalEntry.SyncToken = existingJournalEntry.SyncToken

	payload := struct {
		*JournalEntry
		Sparse bool `json:"sparse"`
	}{
		JournalEntry: journalEntry,
		Sparse:       true,
	}

	var journalEntryData struct {
		JournalEntry JournalEntry
		Time         Date
	}

	if err = c.post("journalentry", payload, &journalEntryData, nil); err != nil {
		return nil, err
	}

	return &journalEntryData.JournalEntry, err
}
