package plaid

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/plaid/plaid-go/v41/plaid"
)

// Configuration variables (Environment-based)
var (
	plaidClientID     = os.Getenv("PLAID_CLIENT_ID")
	plaidSecret       = os.Getenv("PLAID_SECRET")
	plaidEnv          = os.Getenv("PLAID_ENV") // 'sandbox' or 'production'
	plaidProducts     = strings.Split(os.Getenv("PLAID_PRODUCTS"), ",")
	plaidCountryCodes = strings.Split(os.Getenv("PLAID_COUNTRY_CODES"), ",")
	plaidRedirectURI  = os.Getenv("PLAID_REDIRECT_URI")
)

// PlaidServiceError mimics your custom Python exception
type PlaidServiceError struct {
	Message string
	Err     error
}

func (e *PlaidServiceError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

// PlaidService is the singleton struct
type PlaidService struct {
	Client *plaid.APIClient
}

var (
	instance *PlaidService
	once     sync.Once
)

// NewPlaidService initializes the client (Singleton)
func NewPlaidService() *PlaidService {
	once.Do(func() {
		environment := plaid.Sandbox
		if plaidEnv == "production" {
			environment = plaid.Production
		}

		configuration := plaid.NewConfiguration()
		configuration.AddDefaultHeader("PLAID-CLIENT-ID", plaidClientID)
		configuration.AddDefaultHeader("PLAID-SECRET", plaidSecret)
		configuration.UseEnvironment(environment)

		client := plaid.NewAPIClient(configuration)
		instance = &PlaidService{Client: client}
	})
	return instance
}

// --- Helper Methods ---

// getAccessTokenForItem handles the logic of fetching the token from your DB
// In Go, you'd typically pass a repository or DB object here.
func (s *PlaidService) getAccessTokenForItem(itemID string) (string, error) {
	// Logic to query your DB (e.g., GORM: db.First(&item, "item_id = ?", itemID))
	// Returning dummy for structure
	return "access-sandbox-xxxx", nil
}

// --- Core API Methods ---

// GetAccounts fetches account info
func (s *PlaidService) GetAccounts(ctx context.Context, itemID string) (map[string]interface{}, error) {
	accessToken, err := s.getAccessTokenForItem(itemID)
	if err != nil {
		return nil, &PlaidServiceError{"Item not found", err}
	}

	request := plaid.NewAccountsGetRequest(accessToken)
	resp, _, err := s.Client.PlaidApi.AccountsGet(ctx).AccountsGetRequest(*request).Execute()
	if err != nil {
		return nil, &PlaidServiceError{"Failed to get accounts", err}
	}

	// Converting to map to mimic Python's .to_dict()
	data, _ := json.Marshal(resp)
	var result map[string]interface{}
	json.Unmarshal(data, &result)
	return result, nil
}

// CreateLinkToken generates a token for Plaid Link
func (s *PlaidService) CreateLinkToken(ctx context.Context, userID string) (map[string]interface{}, error) {
	products := []plaid.Products{}
	for _, p := range plaidProducts {
		products = append(products, plaid.Products(p))
	}

	countries := []plaid.CountryCode{}
	for _, c := range plaidCountryCodes {
		countries = append(countries, plaid.CountryCode(c))
	}

	user := plaid.LinkTokenCreateRequestUser{ClientUserId: userID}
	request := plaid.NewLinkTokenCreateRequest("usetoro.io", "en", countries)
	request.SetUser(user)
	request.SetProducts(products)
	request.SetRedirectUri(plaidRedirectURI)
	request.SetWebhook("https://prime-legible-turkey.ngrok-free.app/api/plaid/webhook/")

	resp, _, err := s.Client.PlaidApi.LinkTokenCreate(ctx).LinkTokenCreateRequest(*request).Execute()
	if err != nil {
		return nil, &PlaidServiceError{"Could not create link token", err}
	}

	return map[string]interface{}{"link_token": resp.GetLinkToken()}, nil
}

// SyncTransactions handles the incremental update logic (get_transactions in your python)
func (s *PlaidService) SyncTransactions(ctx context.Context, itemID string, cursor string) (map[string]interface{}, error) {
	accessToken, _ := s.getAccessTokenForItem(itemID)

	var added []plaid.Transaction
	var modified []plaid.Transaction
	var removed []plaid.RemovedTransaction
	hasMore := true
	nextCursor := cursor

	for hasMore {
		request := plaid.NewTransactionsSyncRequest(accessToken)
		if nextCursor != "" {
			request.SetCursor(nextCursor)
		}

		resp, _, err := s.Client.PlaidApi.TransactionsSync(ctx).TransactionsSyncRequest(*request).Execute()
		if err != nil {
			return nil, &PlaidServiceError{"Transaction sync failed", err}
		}

		added = append(added, resp.GetAdded()...)
		modified = append(modified, resp.GetModified()...)
		removed = append(removed, resp.GetRemoved()...)
		hasMore = resp.GetHasMore()
		nextCursor = resp.GetNextCursor()
	}

	return map[string]interface{}{
		"added":       added,
		"modified":    modified,
		"removed":     removed,
		"next_cursor": nextCursor,
	}, nil
}

// CreateSandboxPublicToken creates a test token without the UI flow
func (s *PlaidService) CreateSandboxPublicToken(ctx context.Context, institutionID string) (string, error) {
	if institutionID == "" {
		institutionID = "ins_109508"
	}

	products := []plaid.Products{}
	for _, p := range plaidProducts {
		products = append(products, plaid.Products(p))
	}

	options := plaid.NewSandboxPublicTokenCreateRequestOptions()
	options.SetOverrideUsername("user_transactions_dynamic")
	options.SetOverridePassword("Any-non-blank-password-will-work")

	request := plaid.NewSandboxPublicTokenCreateRequest(institutionID, products)
	request.SetOptions(*options)

	resp, _, err := s.Client.PlaidApi.SandboxPublicTokenCreate(ctx).SandboxPublicTokenCreateRequest(*request).Execute()
	if err != nil {
		return "", &PlaidServiceError{"Failed to create sandbox token", err}
	}

	return resp.GetPublicToken(), nil
}

// RefreshTransactions triggers an immediate update
func (s *PlaidService) RefreshTransactions(ctx context.Context, accessToken string) error {
	request := plaid.NewTransactionsRefreshRequest(accessToken)
	_, _, err := s.Client.PlaidApi.TransactionsRefresh(ctx).TransactionsRefreshRequest(*request).Execute()
	return err
}
