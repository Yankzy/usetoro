package connectors

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/financialconnections/account"
	"github.com/stripe/stripe-go/v76/financialconnections/session"
	"github.com/stripe/stripe-go/v76/webhook"
)

// CreateSessionRequest is the input for creating a Financial Connections session.
type CreateSessionRequest struct {
	CustomerEmail string   `json:"customer_email"`
	Permissions   []string `json:"permissions"`
}

// CreateSessionResponse is returned after successfully creating a session.
type CreateSessionResponse struct {
	ID           string `json:"id"`
	ClientSecret string `json:"client_secret"`
	URL          string `json:"url"`
}

// AccountResponse contains the details of a connected financial account.
type AccountResponse struct {
	ID                   string   `json:"id"`
	Status               string   `json:"status"`
	InstitutionName      string   `json:"institution_name"`
	Subcategory          string   `json:"subcategory"`
	Currency             string   `json:"currency"`
	CurrentBalance       float64  `json:"current_balance,omitempty"`
	CurrentBalanceCash   float64  `json:"current_balance_cash,omitempty"`
	CurrentBalanceCredit float64  `json:"current_balance_credit,omitempty"`
	AccountHolderName    string   `json:"account_holder_name,omitempty"`
	Last4                string   `json:"last4,omitempty"`
	SupportedNetworks    []string `json:"supported_networks,omitempty"`
	Livemode             bool     `json:"livemode"`
	Created              int64    `json:"created"`
}

// StripeConnector integrates with Stripe Financial Connections.
type StripeConnector struct {
	logger        *slog.Logger
	cfg           *config.Config
	webhookSecret string
}

// NewStripeConnector creates a new StripeConnector and sets the global API key.
func NewStripeConnector(logger *slog.Logger, cfg *config.Config) *StripeConnector {
	stripe.Key = cfg.StripeSecretKey
	return &StripeConnector{
		logger:        logger,
		cfg:           cfg,
		webhookSecret: cfg.StripeWebhookSecret,
	}
}

// Fetch fulfills the Connector interface but is a no-op for Stripe.
func (c *StripeConnector) Fetch(ctx context.Context, tenantID string) error {
	c.logger.Info("Fetch called on StripeConnector, but Stripe sync is webhook-driven", "tenant_id", tenantID)
	return nil
}

var validPermissions = map[string]bool{
	"balances":       true,
	"ownership":      true,
	"payment_method": true,
	"transactions":   true,
}

// CreateSession creates a new Stripe Financial Connections session.
func (c *StripeConnector) CreateSession(ctx context.Context, req CreateSessionRequest) (*CreateSessionResponse, error) {
	if req.CustomerEmail == "" {
		return nil, &StripeServiceError{
			Message:    "customer_email is required",
			StatusCode: http.StatusBadRequest,
			Code:       "VALIDATION_ERROR",
		}
	}

	if len(req.Permissions) == 0 {
		return nil, &StripeServiceError{
			Message:    "at least one permission is required",
			StatusCode: http.StatusBadRequest,
			Code:       "VALIDATION_ERROR",
		}
	}

	for _, p := range req.Permissions {
		if !validPermissions[p] {
			return nil, &StripeServiceError{
				Message:    fmt.Sprintf("invalid permission: %s", p),
				StatusCode: http.StatusBadRequest,
				Code:       "VALIDATION_ERROR",
			}
		}
	}

	params := &stripe.FinancialConnectionsSessionParams{
		// Omit AccountHolder if we only have an email instead of a Customer ID
		Permissions: stripe.StringSlice(req.Permissions),
	}

	result, err := session.New(params)
	if err != nil {
		return nil, c.wrapStripeError(err)
	}

	c.logger.Info("created financial connections session",
		"session_id", result.ID,
		"customer_email", req.CustomerEmail,
	)

	return &CreateSessionResponse{
		ID:           result.ID,
		ClientSecret: result.ClientSecret,
		URL:          "", // URL is not returned by the API for standard sessions
	}, nil
}

// GetAccount retrieves a Financial Connections account by ID.
func (c *StripeConnector) GetAccount(ctx context.Context, accountID string) (*AccountResponse, error) {
	if accountID == "" {
		return nil, &StripeServiceError{
			Message:    "account_id is required",
			StatusCode: http.StatusBadRequest,
			Code:       "VALIDATION_ERROR",
		}
	}

	params := &stripe.FinancialConnectionsAccountParams{}
	params.AddExpand("balance")
	params.AddExpand("ownership")

	result, err := account.GetByID(accountID, params)
	if err != nil {
		return nil, c.wrapStripeError(err)
	}

	resp := &AccountResponse{
		ID:              result.ID,
		Status:          string(result.Status),
		InstitutionName: result.InstitutionName,
		Subcategory:     string(result.Subcategory),
		Livemode:        result.Livemode,
		Created:         result.Created,
	}

	if result.Balance != nil {
		for curr, val := range result.Balance.Current {
			resp.Currency = curr
			resp.CurrentBalance = float64(val) / 100.0
			break // Just take the first currency found
		}
		if result.Balance.Cash != nil {
			for _, val := range result.Balance.Cash.Available {
				resp.CurrentBalanceCash = float64(val) / 100.0
				break
			}
		}
		if result.Balance.Credit != nil {
			for _, val := range result.Balance.Credit.Used {
				resp.CurrentBalanceCredit = float64(val) / 100.0
				break
			}
		}
	}

	if result.Last4 != "" {
		resp.Last4 = result.Last4
	}

	if len(result.SupportedPaymentMethodTypes) > 0 {
		resp.SupportedNetworks = make([]string, len(result.SupportedPaymentMethodTypes))
		for i, n := range result.SupportedPaymentMethodTypes {
			resp.SupportedNetworks[i] = string(n)
		}
	}

	return resp, nil
}

// VerifyWebhook verifies the Stripe webhook signature and returns the event.
func (c *StripeConnector) VerifyWebhook(payload []byte, signatureHeader string) (stripe.Event, error) {
	if signatureHeader == "" {
		return stripe.Event{}, &StripeServiceError{
			Message:    "missing Stripe-Signature header",
			StatusCode: http.StatusBadRequest,
			Code:       "MISSING_SIGNATURE",
		}
	}

	event, err := webhook.ConstructEvent(payload, signatureHeader, c.webhookSecret)
	if err != nil {
		return stripe.Event{}, &StripeServiceError{
			Message:    "webhook signature verification failed",
			StatusCode: http.StatusBadRequest,
			Code:       "SIGNATURE_VERIFICATION_FAILED",
		}
	}

	return event, nil
}

// wrapStripeError converts Stripe SDK errors to StripeServiceError.
func (c *StripeConnector) wrapStripeError(err error) error {
	if serr, ok := err.(*stripe.Error); ok {
		status := stripeErrToHTTPStatus(serr.Type)
		c.logger.Error("stripe api error",
			"type", serr.Type,
			"http_status", serr.HTTPStatusCode,
			"request_id", serr.RequestID,
			"msg", serr.Msg,
		)
		return &StripeServiceError{
			Message:    serr.Msg,
			StatusCode: status,
			Code:       string(serr.Type),
		}
	}

	c.logger.Error("unexpected stripe error", "error", err)
	return &StripeServiceError{
		Message:    "internal server error",
		StatusCode: http.StatusInternalServerError,
		Code:       "INTERNAL_ERROR",
	}
}

func stripeErrToHTTPStatus(t stripe.ErrorType) int {
	switch t {
	case stripe.ErrorTypeCard:
		return http.StatusPaymentRequired
	case stripe.ErrorTypeInvalidRequest:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// StripeServiceError is a typed error with HTTP status code.
type StripeServiceError struct {
	Message    string `json:"message"`
	StatusCode int    `json:"-"`
	Code       string `json:"code"`
}

func (e *StripeServiceError) Error() string {
	return e.Message
}
