package quickbooks

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/sony/gobreaker"
	"golang.org/x/oauth2"
	"golang.org/x/time/rate"
)

// Client is your handle to the QuickBooks API.
type Client struct {
	// Get this from oauth2.NewClient().
	Client *http.Client
	// Set to ProductionEndpoint or SandboxEndpoint.
	endpoint *url.URL
	// The set of quickbooks APIs
	discoveryAPI *DiscoveryAPI
	// The client Id
	clientId string
	// The client Secret
	clientSecret string
	// The minor version of the QB API
	minorVersion string
	// The account Id you're connecting to.
	realmId string
	// Flag set if the limit of 500req/s has been hit (source: https://developer.intuit.com/app/developer/qbo/docs/learn/rest-api-features#limits-and-throttles)
	throttled bool
	// Rate Limiter for general API calls (~500/min)
	limiter *rate.Limiter
	// Rate limiter for the batch endpoint specifically (40 req/min per realmID per QBO docs)
	batchLimiter *rate.Limiter
	// Concurrency limiter
	concurrencySem chan struct{}
	// Circuit breaker
	breaker *gobreaker.CircuitBreaker
}

// TokenUpdatedFunc is a callback that is triggered when the token is refreshed.
type TokenUpdatedFunc func(token *BearerToken) error

// NewClient initializes a new QuickBooks client for interacting with their Online API
func NewClient(clientId string, clientSecret string, realmId string, isProduction bool, minorVersion string, token *BearerToken, onTokenUpdated TokenUpdatedFunc) (c *Client, err error) {
	minorVersion = cmp.Or(minorVersion, "75")
	client := Client{
		clientId:       clientId,
		clientSecret:   clientSecret,
		minorVersion:   minorVersion,
		realmId:        realmId,
		throttled:      false,
		limiter:        rate.NewLimiter(rate.Limit(8.0), 10),      // ~500/min ≈ 8.3/s
		batchLimiter:   rate.NewLimiter(rate.Limit(40.0/60.0), 1), // 40/min (QBO batch endpoint limit)
		concurrencySem: make(chan struct{}, 10),                   // 10 concurrent requests max
		breaker: gobreaker.NewCircuitBreaker(gobreaker.Settings{
			Name:        fmt.Sprintf("QBO-API-%s", realmId),
			MaxRequests: 5,
			Interval:    1 * time.Minute,
			Timeout:     30 * time.Second,
			IsSuccessful: func(err error) bool {
				if err == nil {
					return true
				}
				// Application errors shouldn't trip the breaker
				return IsQBOApplicationError(err)
			},
			ReadyToTrip: func(counts gobreaker.Counts) bool {
				failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
				return counts.Requests >= 5 && failureRatio >= 0.6 // 60% failure rate over 5+ reqs
			},
		}),
	}

	var endpoint string
	var discoveryEndpoint EndpointUrl

	if isProduction {
		endpoint = ProductionEndpoint.String()
		discoveryEndpoint = DiscoveryProductionEndpoint
	} else {
		endpoint = SandboxEndpoint.String()
		discoveryEndpoint = DiscoverySandboxEndpoint
	}

	client.endpoint, err = url.Parse(endpoint + "/v3/company/" + realmId + "/")
	if err != nil {
		return nil, fmt.Errorf("failed to parse API endpoint: %v", err)
	}

	client.discoveryAPI, err = CallDiscoveryAPI(discoveryEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain discovery endpoint: %v", err)
	}

	if token != nil {
		// Create oauth2 config
		conf := &oauth2.Config{
			ClientID:     clientId,
			ClientSecret: clientSecret,
			Endpoint: oauth2.Endpoint{
				TokenURL: client.discoveryAPI.TokenEndpoint,
			},
		}

		// Convert BearerToken to oauth2.Token
		oauthToken := &oauth2.Token{
			AccessToken:  token.AccessToken,
			RefreshToken: token.RefreshToken,
			TokenType:    "Bearer",
			Expiry:       token.Expiry,
		}

		// Create a token source that automatically refreshes
		tokenSource := conf.TokenSource(context.Background(), oauthToken)

		// Wrap with notification if callback provided
		if onTokenUpdated != nil {
			tokenSource = &notifyTokenSource{
				src:            tokenSource,
				lastKnownToken: oauthToken, // Init with current token so we don't fire on first read
				onTokenUpdated: onTokenUpdated,
			}
		}

		client.Client = oauth2.NewClient(context.Background(), tokenSource)
	}

	return &client, nil
}

// FindAuthorizationUrl compiles the authorization url from the discovery api's auth endpoint.
//
// Example: qbClient.FindAuthorizationUrl("com.intuit.quickbooks.accounting", "security_token", "https://developer.intuit.com/v2/OAuth2Playground/RedirectUrl")
//
// You can find live examples from https://developer.intuit.com/app/developer/playground
func (c *Client) FindAuthorizationUrl(scope string, state string, redirectUri string) (string, error) {
	return GetAuthURL(c.clientId, scope, state, redirectUri, c.discoveryAPI.AuthorizationEndpoint)
}

// GetAuthURL builds a QBO Authorization URL.
func GetAuthURL(clientId, scope, state, redirectUri, authEndpoint string) (string, error) {
	authorizationUrl, err := url.Parse(authEndpoint)
	if err != nil {
		return "", fmt.Errorf("failed to parse auth endpoint: %v", err)
	}

	urlValues := url.Values{}
	urlValues.Add("client_id", clientId)
	urlValues.Add("response_type", "code")
	urlValues.Add("scope", scope)
	urlValues.Add("redirect_uri", redirectUri)
	urlValues.Add("state", state)
	authorizationUrl.RawQuery = urlValues.Encode()

	return authorizationUrl.String(), nil
}

func (c *Client) req(method string, endpoint string, payloadData interface{}, responseObject interface{}, queryParameters map[string]string) error {
	return c.reqContext(context.Background(), method, endpoint, payloadData, responseObject, queryParameters)
}

func (c *Client) reqContext(ctx context.Context, method string, endpoint string, payloadData interface{}, responseObject interface{}, queryParameters map[string]string) error {
	if c.throttled {
		return errors.New("waiting for rate limit")
	}

	endpointUrl := *c.endpoint
	endpointUrl.Path += endpoint
	urlValues := url.Values{}

	for param, value := range queryParameters {
		urlValues.Add(param, value)
	}
	urlValues.Set("minorversion", c.minorVersion)
	endpointUrl.RawQuery = urlValues.Encode()

	var marshalledJson []byte
	if payloadData != nil {
		var err error
		marshalledJson, err = json.Marshal(payloadData)
		if err != nil {
			return fmt.Errorf("failed to marshal payload: %v", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, endpointUrl.String(), bytes.NewBuffer(marshalledJson))
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}
	req.Header.Add("Accept", "application/json")
	req.Header.Add("Content-Type", "application/json")

	if err := c.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limiter wait failed: %v", err)
	}

	c.concurrencySem <- struct{}{}
	defer func() { <-c.concurrencySem }()

	executeRes, breakerErr := c.breaker.Execute(func() (interface{}, error) {
		resp, err := c.Client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to make request: %v", err)
		}
		defer resp.Body.Close()

		switch resp.StatusCode {
		case http.StatusOK:
			bodyBytes, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, err
			}
			return bodyBytes, nil
		case http.StatusTooManyRequests:
			c.throttled = true
			go func(c *Client) {
				time.Sleep(1 * time.Minute)
				c.throttled = false
			}(c)
			return nil, errors.New("rate limited by QBO")
		default:
			return nil, parseFailure(resp)
		}
	})

	if breakerErr != nil {
		return breakerErr
	}

	bodyBytes := executeRes.([]byte)

	if responseObject != nil {
		if err = json.Unmarshal(bodyBytes, &responseObject); err != nil {
			return fmt.Errorf("failed to unmarshal response into object: %v", err)
		}
	}
	return nil
}

// BatchContext executes multiple operations in a single API call, respecting context cancellation.
// Enforces QBO rules: max 30 operations per call, 40 calls/min per realmID.
func (c *Client) BatchContext(ctx context.Context, requests []BatchItemRequest) (*BatchResponse, error) {
	if len(requests) == 0 {
		return nil, errors.New("batch request cannot be empty")
	}
	if len(requests) > BatchMaxSize {
		return nil, fmt.Errorf("batch request exceeds maximum of %d operations (got %d)", BatchMaxSize, len(requests))
	}

	// Enforce the 40 req/min batch-endpoint limit before consuming a general-limiter token.
	if err := c.batchLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("batch rate limiter: %w", err)
	}

	batchReq := BatchRequest{BatchItemRequest: requests}
	var response BatchResponse

	if err := c.postContext(ctx, "batch", batchReq, &response, nil); err != nil {
		return nil, fmt.Errorf("batch request failed: %w", err)
	}

	return &response, nil
}

// Batch executes multiple operations in a single API call.
// Deprecated: prefer BatchContext to propagate cancellation.
func (c *Client) Batch(requests []BatchItemRequest) (*BatchResponse, error) {
	return c.BatchContext(context.Background(), requests)
}

// IsThrottled returns true if the client has hit QBO's rate limit
func (c *Client) IsThrottled() bool {
	return c.throttled
}

func (c *Client) get(endpoint string, responseObject interface{}, queryParameters map[string]string) error {
	return c.reqContext(context.Background(), "GET", endpoint, nil, responseObject, queryParameters)
}

func (c *Client) post(endpoint string, payloadData interface{}, responseObject interface{}, queryParameters map[string]string) error {
	return c.reqContext(context.Background(), "POST", endpoint, payloadData, responseObject, queryParameters)
}

func (c *Client) getContext(ctx context.Context, endpoint string, responseObject interface{}, queryParameters map[string]string) error {
	return c.reqContext(ctx, "GET", endpoint, nil, responseObject, queryParameters)
}

func (c *Client) postContext(ctx context.Context, endpoint string, payloadData interface{}, responseObject interface{}, queryParameters map[string]string) error {
	return c.reqContext(ctx, "POST", endpoint, payloadData, responseObject, queryParameters)
}

// Query makes the specified QBO `query` and unmarshals the result into `responseObject`
func (c *Client) Query(query string, responseObject interface{}) error {
	return c.get("query", responseObject, map[string]string{"query": query})
}

func (c *Client) GetEndpoint() string {
	return c.endpoint.String()
}

type BearerToken struct {
	RefreshToken           string    `json:"refresh_token"`
	AccessToken            string    `json:"access_token"`
	TokenType              string    `json:"token_type"`
	IdToken                string    `json:"id_token"`
	ExpiresIn              int64     `json:"expires_in"`
	XRefreshTokenExpiresIn int64     `json:"x_refresh_token_expires_in"`
	Expiry                 time.Time `json:"-"` // Added for local expiry tracking
}

// notifyTokenSource wraps an oauth2.TokenSource and triggers a callback when a token is refreshed.
type notifyTokenSource struct {
	src            oauth2.TokenSource
	lastKnownToken *oauth2.Token
	onTokenUpdated TokenUpdatedFunc
}

func (s *notifyTokenSource) Token() (*oauth2.Token, error) {
	t, err := s.src.Token()
	if err != nil {
		return nil, err
	}

	// Check if token has changed (refresh occurred)
	// We compare AccessToken as the primary indicator of a change.
	// lastKnownToken is initialized in NewClient, so it won't be nil here.
	if t.AccessToken != s.lastKnownToken.AccessToken {
		// IMPORTANT: QBO refresh tokens are not always rotated. If the new token
		// has an empty RefreshToken, we MUST retain the old one, otherwise
		// we will overwrite the database with an empty string and lose access.
		refreshToken := t.RefreshToken
		if refreshToken == "" {
			refreshToken = s.lastKnownToken.RefreshToken
		}

		if s.onTokenUpdated != nil {
			bt := &BearerToken{
				AccessToken:  t.AccessToken,
				RefreshToken: refreshToken,
				ExpiresIn:    int64(time.Until(t.Expiry).Seconds()),
				Expiry:       t.Expiry,
				TokenType:    t.TokenType,
			}
			if err := s.onTokenUpdated(bt); err != nil {
				return nil, fmt.Errorf("failed to persist refreshed token: %w", err)
			}
		}
		s.lastKnownToken = t
		// Update the returned token too so the caller (http.Client) has the full state
		t.RefreshToken = refreshToken
	}

	return t, nil
}

// CDC (Change Data Capture) Types and Methods

// CDCResponse represents the response from the CDC endpoint
type CDCResponse struct {
	CDCResponse []CDCEntity `json:"CDCResponse"`
	Time        string      `json:"time"`
}

// CDCEntity represents a single entity group in the CDC response
type CDCEntity struct {
	QueryResponse []QueryResponseItem `json:"QueryResponse"`
}

// QueryResponseItem contains arrays of changed entities by type
type QueryResponseItem struct {
	Account      []Account      `json:"Account,omitempty"`
	Vendor       []Vendor       `json:"Vendor,omitempty"`
	Customer     []Customer     `json:"Customer,omitempty"`
	Invoice      []Invoice      `json:"Invoice,omitempty"`
	Bill         []Bill         `json:"Bill,omitempty"`
	JournalEntry []JournalEntry `json:"JournalEntry,omitempty"`
	Purchase     []Purchase     `json:"Purchase,omitempty"`
	Deposit      []Deposit      `json:"Deposit,omitempty"`
	Payment      []Payment      `json:"Payment,omitempty"`
	SalesReceipt []SalesReceipt `json:"SalesReceipt,omitempty"`
	Attachable   []Attachable   `json:"Attachable,omitempty"`
}

// QueryCDC fetches entities changed since the specified timestamp.
// entities: comma-separated list like "Account,Vendor,Customer,Invoice,Bill"
// changedSince: timestamp for changes (max 30 days lookback per QBO limits)
//
// Example: client.QueryCDC("Account,Vendor", time.Now().Add(-24*time.Hour))
func (c *Client) QueryCDC(entities string, changedSince time.Time) (*CDCResponse, error) {
	params := map[string]string{
		"entities":     entities,
		"changedSince": changedSince.Format(time.RFC3339),
	}

	var response CDCResponse
	err := c.get("cdc", &response, params)
	if err != nil {
		return nil, fmt.Errorf("CDC query failed: %w", err)
	}

	return &response, nil
}
