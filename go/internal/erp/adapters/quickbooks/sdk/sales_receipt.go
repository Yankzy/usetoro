package quickbooks

import (
	"encoding/json"
	"errors"
	"strconv"
)

// SalesReceipt represents a QBO SalesReceipt (income transaction)
type SalesReceipt struct {
	Id                  string         `json:"Id,omitempty"`
	SyncToken           string         `json:",omitempty"`
	Domain              string         `json:"domain,omitempty"`
	DocNumber           string         `json:",omitempty"`
	TxnDate             Date           `json:",omitempty"`
	TotalAmt            json.Number    `json:",omitempty"`
	CustomerRef         ReferenceType  `json:",omitempty"`
	DepositToAccountRef *ReferenceType `json:"DepositToAccountRef,omitempty"`
	Line                []Line         `json:",omitempty"`
	PaymentMethodRef    *ReferenceType `json:"PaymentMethodRef,omitempty"`
	PrivateNote         string         `json:",omitempty"`
	MetaData            MetaData       `json:",omitempty"`
}

// CreateSalesReceipt creates the given SalesReceipt on the QuickBooks server,
// returning the resulting SalesReceipt object.
func (c *Client) CreateSalesReceipt(sr *SalesReceipt) (*SalesReceipt, error) {
	var resp struct {
		SalesReceipt SalesReceipt
		Time         Date
	}

	if err := c.post("salesreceipt", sr, &resp, nil); err != nil {
		return nil, err
	}

	return &resp.SalesReceipt, nil
}

// DeleteSalesReceipt deletes the sales receipt
func (c *Client) DeleteSalesReceipt(sr *SalesReceipt) error {
	if sr.Id == "" || sr.SyncToken == "" {
		return errors.New("missing id/sync token")
	}

	return c.post("salesreceipt", sr, nil, map[string]string{"operation": "delete"})
}

// FindSalesReceipts gets the full list of SalesReceipts in the QuickBooks account.
func (c *Client) FindSalesReceipts() ([]SalesReceipt, error) {
	var resp struct {
		QueryResponse struct {
			SalesReceipts []SalesReceipt `json:"SalesReceipt"`
			MaxResults    int
			StartPosition int
			TotalCount    int
		}
	}

	if err := c.Query("SELECT COUNT(*) FROM SalesReceipt", &resp); err != nil {
		return nil, err
	}

	if resp.QueryResponse.TotalCount == 0 {
		return nil, nil
	}

	srs := make([]SalesReceipt, 0, resp.QueryResponse.TotalCount)

	for i := 0; i < resp.QueryResponse.TotalCount; i += queryPageSize {
		query := "SELECT * FROM SalesReceipt ORDERBY TxnDate STARTPOSITION " + strconv.Itoa(i+1) + " MAXRESULTS " + strconv.Itoa(queryPageSize)

		if err := c.Query(query, &resp); err != nil {
			return nil, err
		}

		if resp.QueryResponse.SalesReceipts == nil {
			return nil, errors.New("no sales receipts could be found")
		}

		srs = append(srs, resp.QueryResponse.SalesReceipts...)
	}

	return srs, nil
}

// FindSalesReceiptById finds the sales receipt by the given id
func (c *Client) FindSalesReceiptById(id string) (*SalesReceipt, error) {
	var resp struct {
		SalesReceipt SalesReceipt
		Time         Date
	}

	if err := c.get("salesreceipt/"+id, &resp, nil); err != nil {
		return nil, err
	}

	return &resp.SalesReceipt, nil
}

// QuerySalesReceipts accepts an SQL query and returns all sales receipts found using it
func (c *Client) QuerySalesReceipts(query string) ([]SalesReceipt, error) {
	var resp struct {
		QueryResponse struct {
			SalesReceipts []SalesReceipt `json:"SalesReceipt"`
			StartPosition int
			MaxResults    int
		}
	}

	if err := c.Query(query, &resp); err != nil {
		return nil, err
	}

	if resp.QueryResponse.SalesReceipts == nil {
		return nil, errors.New("could not find any sales receipts")
	}

	return resp.QueryResponse.SalesReceipts, nil
}

// UpdateSalesReceipt updates the sales receipt
func (c *Client) UpdateSalesReceipt(sr *SalesReceipt) (*SalesReceipt, error) {
	if sr.Id == "" {
		return nil, errors.New("missing sales receipt id")
	}

	existingSR, err := c.FindSalesReceiptById(sr.Id)
	if err != nil {
		return nil, err
	}

	sr.SyncToken = existingSR.SyncToken

	payload := struct {
		*SalesReceipt
		Sparse bool `json:"sparse"`
	}{
		SalesReceipt: sr,
		Sparse:       true,
	}

	var srData struct {
		SalesReceipt SalesReceipt
		Time         Date
	}

	if err = c.post("salesreceipt", payload, &srData, nil); err != nil {
		return nil, err
	}

	return &srData.SalesReceipt, err
}
