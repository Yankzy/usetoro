package quickbooks

import "context"

// NameValue represents a QuickBooks preference key-value pair.
type NameValue struct {
	Name  string `json:"Name"`
	Value string `json:"Value"`
}

// CompanyInfo describes a QBO company account with all API-available fields.
type CompanyInfo struct {
	Id          string    `json:"Id"`
	SyncToken   string    `json:"SyncToken"`
	CompanyName string    `json:"CompanyName"`
	LegalName   string    `json:"LegalName,omitempty"`
	Domain      string    `json:"domain,omitempty"`
	Country     string    `json:"Country,omitempty"`

	CompanyStartDate     Date   `json:"CompanyStartDate,omitempty"`
	FiscalYearStartMonth string `json:"FiscalYearStartMonth,omitempty"`
	SupportedLanguages   string `json:"SupportedLanguages,omitempty"`

	CompanyAddr               *PhysicalAddress  `json:"CompanyAddr,omitempty"`
	CustomerCommunicationAddr *PhysicalAddress  `json:"CustomerCommunicationAddr,omitempty"`
	LegalAddr                 *PhysicalAddress  `json:"LegalAddr,omitempty"`
	PrimaryPhone              *TelephoneNumber  `json:"PrimaryPhone,omitempty"`
	Email                     *EmailAddress     `json:"Email,omitempty"`
	WebAddr                   *WebSiteAddress   `json:"WebAddr,omitempty"`

	// NameValue holds company preference settings (e.g. TrackClasses, FiscalYear overrides).
	NameValue []NameValue `json:"NameValue,omitempty"`

	Metadata *MetaData `json:"MetaData,omitempty"`
}

// FindCompanyInfo returns the QBO CompanyInfo for the configured realm.
// It is a good connectivity check; a successful response confirms a valid OAuth token.
func (c *Client) FindCompanyInfo() (*CompanyInfo, error) {
	return c.FindCompanyInfoContext(context.Background())
}

// FindCompanyInfoContext is the context-aware variant of FindCompanyInfo.
func (c *Client) FindCompanyInfoContext(ctx context.Context) (*CompanyInfo, error) {
	var resp struct {
		CompanyInfo CompanyInfo `json:"CompanyInfo"`
		Time        Date        `json:"time"`
	}
	if err := c.getContext(ctx, "companyinfo/"+c.realmId, &resp, nil); err != nil {
		return nil, err
	}
	return &resp.CompanyInfo, nil
}

// UpdateCompanyInfo performs a sparse update of the company info in QBO.
// The SyncToken and Id are resolved automatically via a preflight read.
func (c *Client) UpdateCompanyInfo(info *CompanyInfo) (*CompanyInfo, error) {
	return c.UpdateCompanyInfoContext(context.Background(), info)
}

// UpdateCompanyInfoContext is the context-aware variant of UpdateCompanyInfo.
func (c *Client) UpdateCompanyInfoContext(ctx context.Context, info *CompanyInfo) (*CompanyInfo, error) {
	existing, err := c.FindCompanyInfoContext(ctx)
	if err != nil {
		return nil, err
	}

	info.Id = existing.Id
	info.SyncToken = existing.SyncToken

	payload := struct {
		*CompanyInfo
		Sparse bool `json:"sparse"`
	}{CompanyInfo: info, Sparse: true}

	var resp struct {
		CompanyInfo CompanyInfo `json:"CompanyInfo"`
		Time        Date        `json:"time"`
	}
	if err := c.postContext(ctx, "companyInfo", payload, &resp, nil); err != nil {
		return nil, err
	}
	return &resp.CompanyInfo, nil
}
