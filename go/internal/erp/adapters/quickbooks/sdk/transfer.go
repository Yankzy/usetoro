package quickbooks

import (
	"encoding/json"
	"errors"
	"strconv"
)

// Transfer represents an inter-account transfer in QuickBooks Online
type Transfer struct {
	Id             string        `json:"Id,omitempty"`
	SyncToken      string        `json:",omitempty"`
	Amount         json.Number   `json:",omitempty"`
	FromAccountRef ReferenceType `json:",omitempty"`
	ToAccountRef   ReferenceType `json:",omitempty"`
	TxnDate        Date          `json:",omitempty"`
	PrivateNote    string        `json:",omitempty"`
	MetaData       MetaData      `json:",omitempty"`
}

// CreateTransfer creates the given Transfer on the QuickBooks server,
// returning the resulting Transfer object.
func (c *Client) CreateTransfer(transfer *Transfer) (*Transfer, error) {
	var resp struct {
		Transfer Transfer
		Time     Date
	}

	if err := c.post("transfer", transfer, &resp, nil); err != nil {
		return nil, err
	}

	return &resp.Transfer, nil
}

// DeleteTransfer deletes the given transfer.
func (c *Client) DeleteTransfer(transfer *Transfer) error {
	if transfer.Id == "" || transfer.SyncToken == "" {
		return errors.New("missing id/sync token")
	}

	return c.post("transfer", transfer, nil, map[string]string{"operation": "delete"})
}

// FindTransfers retrieves the full list of transfers from QuickBooks.
func (c *Client) FindTransfers() ([]Transfer, error) {
	var resp struct {
		QueryResponse struct {
			Transfers     []Transfer `json:"Transfer"`
			MaxResults    int
			StartPosition int
			TotalCount    int
		}
	}

	if err := c.Query("SELECT COUNT(*) FROM Transfer", &resp); err != nil {
		return nil, err
	}

	if resp.QueryResponse.TotalCount == 0 {
		return nil, nil
	}

	transfers := make([]Transfer, 0, resp.QueryResponse.TotalCount)

	for i := 0; i < resp.QueryResponse.TotalCount; i += queryPageSize {
		query := "SELECT * FROM Transfer ORDERBY Id STARTPOSITION " + strconv.Itoa(i+1) + " MAXRESULTS " + strconv.Itoa(queryPageSize)

		if err := c.Query(query, &resp); err != nil {
			return nil, err
		}

		if resp.QueryResponse.Transfers == nil {
			return nil, errors.New("could not find any transfers")
		}

		transfers = append(transfers, resp.QueryResponse.Transfers...)
	}

	return transfers, nil
}

// FindTransferById retrieves the transfer by the given id.
func (c *Client) FindTransferById(id string) (*Transfer, error) {
	var resp struct {
		Transfer Transfer
		Time     Date
	}

	if err := c.get("transfer/"+id, &resp, nil); err != nil {
		return nil, err
	}

	return &resp.Transfer, nil
}

// QueryTransfers accepts an SQL query and returns all transfers found using it.
func (c *Client) QueryTransfers(query string) ([]Transfer, error) {
	var resp struct {
		QueryResponse struct {
			Transfers     []Transfer `json:"Transfer"`
			StartPosition int
			MaxResults    int
		}
	}

	if err := c.Query(query, &resp); err != nil {
		return nil, err
	}

	if resp.QueryResponse.Transfers == nil {
		return nil, errors.New("could not find any transfers")
	}

	return resp.QueryResponse.Transfers, nil
}

// UpdateTransfer updates the transfer.
func (c *Client) UpdateTransfer(transfer *Transfer) (*Transfer, error) {
	if transfer.Id == "" {
		return nil, errors.New("missing transfer id")
	}

	existingTransfer, err := c.FindTransferById(transfer.Id)
	if err != nil {
		return nil, err
	}

	transfer.SyncToken = existingTransfer.SyncToken

	payload := struct {
		*Transfer
		Sparse bool `json:"sparse"`
	}{
		Transfer: transfer,
		Sparse:   true,
	}

	var transferData struct {
		Transfer Transfer
		Time     Date
	}

	if err = c.post("transfer", payload, &transferData, nil); err != nil {
		return nil, err
	}

	return &transferData.Transfer, err
}
