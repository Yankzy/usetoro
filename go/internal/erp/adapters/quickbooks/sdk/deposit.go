package quickbooks

import (
	"encoding/json"
	"errors"
	"strconv"
)

type Deposit struct {
	Id                  string        `json:"Id,omitempty"`
	SyncToken           string        `json:"SyncToken,omitempty"`
	Domain              string        `json:"domain,omitempty"`
	MetaData            MetaData      `json:",omitempty"`
	DocNumber           string        `json:",omitempty"`
	DepositToAccountRef ReferenceType `json:",omitempty"`
	TxnDate             Date          `json:",omitempty"`
	TotalAmt            json.Number   `json:",omitempty"`
	PrivateNote         string        `json:",omitempty"`
	Line                []DepositLine `json:",omitempty"`
	CashBack            *CashBack     `json:",omitempty"`
}

// DepositLine represents a single line in a QBO Deposit payload.
type DepositLine struct {
	Id                string            `json:"Id,omitempty"`
	Description       string            `json:",omitempty"`
	Amount            json.Number       `json:",omitempty"`
	DetailType        string            `json:",omitempty"` // typically "DepositLineDetail"
	DepositLineDetail DepositLineDetail `json:",omitempty"`
	LinkedTxn         []LinkedTxn       `json:",omitempty"`
}

// DepositLineDetail is the QBO "DepositLineDetail" object.
type DepositLineDetail struct {
	AccountRef       ReferenceType `json:",omitempty"`
	EntityRef        ReferenceType `json:",omitempty"`
	PaymentMethodRef ReferenceType `json:",omitempty"`
	CheckNum         string        `json:",omitempty"`
}

// CashBack represents a QBO CashBack object.
type CashBack struct {
	AccountRef ReferenceType `json:",omitempty"`
	Amount     json.Number   `json:",omitempty"`
	Memo       string        `json:",omitempty"`
}

// CreateDeposit creates the given deposit within QuickBooks
func (c *Client) CreateDeposit(deposit *Deposit) (*Deposit, error) {
	var resp struct {
		Deposit Deposit
		Time    Date
	}

	if err := c.post("deposit", deposit, &resp, nil); err != nil {
		return nil, err
	}

	return &resp.Deposit, nil
}

func (c *Client) DeleteDeposit(deposit *Deposit) error {
	if deposit.Id == "" || deposit.SyncToken == "" {
		return errors.New("missing id/sync token")
	}

	return c.post("deposit", deposit, nil, map[string]string{"operation": "delete"})
}

// FindDeposits gets the full list of Deposits in the QuickBooks account.
func (c *Client) FindDeposits() ([]Deposit, error) {
	var resp struct {
		QueryResponse struct {
			Deposits      []Deposit `json:"Deposit"`
			MaxResults    int
			StartPosition int
			TotalCount    int
		}
	}

	if err := c.Query("SELECT COUNT(*) FROM Deposit", &resp); err != nil {
		return nil, err
	}

	if resp.QueryResponse.TotalCount == 0 {
		return nil, nil
	}

	deposits := make([]Deposit, 0, resp.QueryResponse.TotalCount)

	for i := 0; i < resp.QueryResponse.TotalCount; i += queryPageSize {
		query := "SELECT * FROM Deposit ORDERBY TxnDate STARTPOSITION " + strconv.Itoa(i+1) + " MAXRESULTS " + strconv.Itoa(queryPageSize)

		if err := c.Query(query, &resp); err != nil {
			return nil, err
		}

		if resp.QueryResponse.Deposits == nil {
			return nil, errors.New("no deposits could be found")
		}

		deposits = append(deposits, resp.QueryResponse.Deposits...)
	}

	return deposits, nil
}

// FindDepositById returns an deposit with a given Id.
func (c *Client) FindDepositById(id string) (*Deposit, error) {
	var resp struct {
		Deposit Deposit
		Time    Date
	}

	if err := c.get("deposit/"+id, &resp, nil); err != nil {
		return nil, err
	}

	return &resp.Deposit, nil
}

// QueryDeposits accepts an SQL query and returns all deposits found using it
func (c *Client) QueryDeposits(query string) ([]Deposit, error) {
	var resp struct {
		QueryResponse struct {
			Deposits      []Deposit `json:"Deposit"`
			StartPosition int
			MaxResults    int
		}
	}

	if err := c.Query(query, &resp); err != nil {
		return nil, err
	}

	if resp.QueryResponse.Deposits == nil {
		return nil, errors.New("could not find any deposits")
	}

	return resp.QueryResponse.Deposits, nil
}

// UpdateDeposit updates the deposit
func (c *Client) UpdateDeposit(deposit *Deposit) (*Deposit, error) {
	if deposit.Id == "" {
		return nil, errors.New("missing deposit id")
	}

	existingDeposit, err := c.FindDepositById(deposit.Id)
	if err != nil {
		return nil, err
	}

	deposit.SyncToken = existingDeposit.SyncToken

	payload := struct {
		*Deposit
		Sparse bool `json:"sparse"`
	}{
		Deposit: deposit,
		Sparse:  true,
	}

	var depositData struct {
		Deposit Deposit
		Time    Date
	}

	if err = c.post("deposit", payload, &depositData, nil); err != nil {
		return nil, err
	}

	return &depositData.Deposit, err
}
