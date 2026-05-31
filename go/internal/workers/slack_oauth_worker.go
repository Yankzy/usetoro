package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/internal/database"
	"github.com/Yankzy/usetoro/tap/pkg/core"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
)

type SlackOAuthWorker struct {
	db     *database.Queries
	logger *slog.Logger
	cfg    *config.Config
	nc     *nats.Conn
	redis  *redis.Client
}

func init() {
	RegisterFactory(func(deps Dependencies) (Worker, error) {
		if deps.Config.SlackClientID == "" || deps.Config.SlackClientSecret == "" {
			return nil, nil // Slack OAuth not configured
		}
		return &SlackOAuthWorker{
			db:     deps.Store.Queries,
			logger: deps.Logger,
			cfg:    deps.Config,
			nc:     deps.Queue,
			redis:  deps.Redis,
		}, nil
	})
}

func (w *SlackOAuthWorker) Init(ctx context.Context) error { return nil }

func (w *SlackOAuthWorker) Subscriptions() []SubscriptionConfig {
	_, workerCfg := w.cfg.Workers.GetForWorker(w)
	activityType := workerCfg.ActivityType
	if activityType == "" {
		w.logger.Error("SlackOAuthWorker: no activity_type configured")
		return nil
	}

	subject := workerCfg.Subject
	if subject == "" {
		if derived, err := core.BuildWorkerInboxFromActivity(activityType); err == nil {
			subject = derived
		} else {
			w.logger.Error("SlackOAuthWorker: failed to derive inbox", "activity_type", activityType, "error", err)
			return nil
		}
	}

	group := workerCfg.Group
	if group == "" {
		group = groupFromSubject(subject)
	}

	return []SubscriptionConfig{{
		Subject: subject,
		Group:   group,
		Options: []nats.SubOpt{
			nats.Durable(durableFromSubject(subject)),
			nats.DeliverAll(),
			nats.AckExplicit(),
		},
	}}
}

type slackOAuthPayload struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

type slackOAuthResponse struct {
	OK         bool   `json:"ok"`
	Error      string `json:"error"`
	AppID      string `json:"app_id"`
	AuthedUser struct {
		ID string `json:"id"`
	} `json:"authed_user"`
	Scope       string `json:"scope"`
	TokenType   string `json:"token_type"`
	AccessToken string `json:"access_token"`
	BotUserID   string `json:"bot_user_id"`
	Team        struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"team"`
}

// slackPKCEState represents the JSON object stored in Redis
type slackPKCEState struct {
	TenantID     string `json:"tenant_id"`
	CodeVerifier string `json:"code_verifier"`
	RedirectURI  string `json:"redirect_uri"`
}

func (w *SlackOAuthWorker) Handle(ctx context.Context, msg *nats.Msg) error {
	meta, metaErr := msg.Metadata()
	if metaErr == nil && meta.NumDelivered > 3 {
		w.logger.Error("slack oauth: poison pill exceeded retries", "subject", msg.Subject)
		msg.Term()
		return nil
	}

	var payload slackOAuthPayload
	if err := json.Unmarshal(msg.Data, &payload); err != nil {
		w.logger.Error("slack oauth: failed to unmarshal payload", "error", err)
		msg.Term()
		return nil
	}

	if payload.Code == "" || payload.State == "" {
		w.logger.Error("slack oauth: missing code or state")
		msg.Term()
		return nil
	}

	// Retrieve PKCE state from Redis using the State UUID
	if w.redis == nil {
		w.logger.Error("slack oauth: redis client is not configured")
		msg.Term()
		return nil
	}

	redisKey := "slack_pkce_state:" + payload.State
	stateJSON, err := w.redis.Get(ctx, redisKey).Result()
	if err != nil {
		w.logger.Error("slack oauth: failed to retrieve state from redis or state expired", "state", payload.State, "error", err)
		msg.Term()
		return nil
	}

	// We can delete the state immediately so it can't be reused
	w.redis.Del(ctx, redisKey)

	var pkceState slackPKCEState
	if err := json.Unmarshal([]byte(stateJSON), &pkceState); err != nil {
		w.logger.Error("slack oauth: failed to unmarshal pkce state", "error", err)
		msg.Term()
		return nil
	}

	if pkceState.TenantID == "" || pkceState.CodeVerifier == "" || pkceState.RedirectURI == "" {
		w.logger.Error("slack oauth: invalid pkce state data", "state", pkceState)
		msg.Term()
		return nil
	}

	// Validate TenantID
	var tenantID pgtype.UUID
	if err := tenantID.Scan(pkceState.TenantID); err != nil {
		w.logger.Error("slack oauth: invalid tenant_id in state", "tenant_id", pkceState.TenantID, "error", err)
		msg.Term()
		return nil
	}

	// Exchange Code for Access Token
	data := url.Values{}
	data.Set("client_id", w.cfg.SlackClientID)
	data.Set("client_secret", w.cfg.SlackClientSecret)
	data.Set("code", payload.Code)
	data.Set("code_verifier", pkceState.CodeVerifier)
	data.Set("redirect_uri", pkceState.RedirectURI)
	
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/oauth.v2.access", strings.NewReader(data.Encode()))
	if err != nil {
		w.logger.Error("slack oauth: failed to create request", "error", err)
		msg.Nak()
		return err
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		w.logger.Error("slack oauth: request failed", "error", err)
		msg.Nak()
		return err
	}
	defer resp.Body.Close()

	var oauthResp slackOAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&oauthResp); err != nil {
		w.logger.Error("slack oauth: failed to decode response", "error", err)
		msg.Nak()
		return err
	}

	if !oauthResp.OK {
		w.logger.Error("slack oauth: slack API returned error", "error", oauthResp.Error)
		msg.Term()
		return nil
	}

	// Save the mapping
	_, err = w.db.UpsertSlackTenantMapping(ctx, database.UpsertSlackTenantMappingParams{
		TenantID:         tenantID,
		SlackTeamID:      oauthResp.Team.ID,
		SlackAccessToken: oauthResp.AccessToken,
		SlackBotUserID:   oauthResp.BotUserID,
	})
	if err != nil {
		w.logger.Error("slack oauth: failed to save mapping", "error", err)
		msg.Nak()
		return err
	}

	w.logger.Info("slack oauth: successfully installed and mapped", "team_id", oauthResp.Team.ID, "tenant_id", tenantID)
	msg.Ack()
	return nil
}
