package wshandler

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
	"strings"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/nats-io/nats.go"

	"github.com/Yankzy/usetoro/internal/auth"
	"github.com/Yankzy/usetoro/internal/services/fignode"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer.
	maxMessageSize = 512
)

var (
	newline = []byte{'\n'}
	space   = []byte{' '}
)

// Client is a middleman between the websocket connection and the hub.
type Client struct {
	hub *Hub

	// The websocket connection.
	conn *websocket.Conn

	// Buffered channel of outbound messages.
	send chan []byte

	// Logger
	logger *slog.Logger

	// Message handler for processing requests
	// Message handler for processing requests
	messageHandler *MessageHandler

	// Host from handshake request
	host string

	// Identity
	entityID string

	// JetStream locking
	unackedMu       sync.Mutex
	unackedMessages map[string]*nats.Msg
	cardPumpRunning bool
	ctx             context.Context
	cancel          context.CancelFunc
}

// NewClient creates a new Client instance
func NewClient(hub *Hub, conn *websocket.Conn, logger *slog.Logger, messageHandler *MessageHandler, host string, rCtx context.Context) *Client {
	// DO NOT inherit cancellation from rCtx, as the HTTP context is destroyed instantly after WebSocket upgrade
	ctx, cancel := context.WithCancel(context.WithoutCancel(rCtx))
	// Extract entityID from context (set by auth middleware)
	entityID := "unknown"
	if eid, ok := rCtx.Value(auth.EntityIDKey).(uuid.UUID); ok {
		entityID = eid.String()
	}

	return &Client{
		hub:             hub,
		conn:            conn,
		send:            make(chan []byte, 256),
		logger:          logger,
		messageHandler:  messageHandler,
		host:            host,
		entityID:        entityID,
		unackedMessages: make(map[string]*nats.Msg),
		ctx:             ctx,
		cancel:          cancel,
	}
}

// readPump pumps messages from the websocket connection to the hub.
//
// The application runs readPump in a per-connection goroutine. The application
// ensures that there is at most one reader on a connection by executing all
// reads from this goroutine.
func (c *Client) readPump() {
	defer func() {
		// Stop cardPump if running
		if c.cancel != nil {
			c.cancel()
		}

		// Nak unacknowledged cards
		c.unackedMu.Lock()
		for id, msg := range c.unackedMessages {
			msg.Nak()
			c.logger.Info("NAK'd card due to disconnect", "txn_id", id)
		}
		c.unackedMessages = nil
		c.unackedMu.Unlock()

		c.hub.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { c.conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.logger.Error("WebSocket read error", "error", err)
			}
			break
		}
		c.logger.Debug("Received message from client", "message", string(message))

		// Intercept custom JetStream flow messages
		var parsedMsg Message
		jsonErr := json.Unmarshal(message, &parsedMsg)
		
		if jsonErr == nil {
			if parsedMsg.Type == MessageTypeSubscribeCards {
				c.unackedMu.Lock()
				if !c.cardPumpRunning {
					c.cardPumpRunning = true
					go c.cardPump()
					c.logger.Info("Started card pump for client")
				}
				c.unackedMu.Unlock()
				continue
			}

			if parsedMsg.Type == MessageTypeSwipeResult {
				if parsedMsg.Data != nil {
					if txnID, ok := parsedMsg.Data["txnId"].(string); ok {
						c.unackedMu.Lock()
						if natsMsg, exists := c.unackedMessages[txnID]; exists {
							natsMsg.Ack()
							delete(c.unackedMessages, txnID)
							c.logger.Info("ACK'd card successfully after swipe", "txn_id", txnID)
						} else {
							c.logger.Warn("Cannot ACK card; not locked by this client", "txn_id", txnID)
						}
						c.unackedMu.Unlock()
					}
				}
				// We still pass swipe_result to messageHandler so it can process DB save if implemented
			}
		}

		// Process the message if handler is available
		if c.messageHandler != nil {
			ctx := context.WithValue(context.Background(), "host", c.host)
			response, err := c.messageHandler.HandleMessage(ctx, message)
			if err != nil {
				c.logger.Error("Failed to handle message", "error", err)
				errorResp, _ := NewErrorMessage(err.Error())
				c.send <- errorResp
			} else if response != nil {
				// Send response directly back to this client
				c.send <- response
			}
		}
	}
}

// cardPump pulls unlocked cards from JetStream and sends them to the client
func (c *Client) cardPump() {
	queueClient := c.hub.queueClient
	if queueClient == nil {
		c.logger.Error("QueueClient is nil, cannot start card pump")
		return
	}

	js := queueClient.JetStream()
	
	subjectStr := "cards.unswiped"
	consumerName := "junior_accountants"
	// Hydrate immediately from DB if allowed
	if entityID, ok := c.ctx.Value(auth.EntityIDKey).(uuid.UUID); ok {
		subjectStr = fmt.Sprintf("cards.unswiped.%s", entityID.String())
		consumerName = fmt.Sprintf("junior_accountants_%s", strings.ReplaceAll(entityID.String(), "-", "_"))
		
		// --- Secondary DB Trigger (Seed Initial Connectivity payload immediately upon connection) ---
		if c.hub != nil && c.hub.db != nil {
			var entityUUID pgtype.UUID
			if pgErr := entityUUID.Scan(entityID.String()); pgErr == nil {
				conn, dbErr := c.hub.db.GetERPConnection(context.Background(), entityUUID)
				if dbErr == nil && conn.RealmID != "" {
					// We must align JetStream specifically with the actual QBO realm mapping, not the internal UUID
					subjectStr = fmt.Sprintf("cards.unswiped.%s", conn.RealmID)
					consumerName = fmt.Sprintf("junior_accountants_%s", conn.RealmID)

					c.blastDatabaseCards(conn.RealmID)
					c.hub.JoinRoom(c, conn.RealmID)

					// Dynamically wait for reconciliation pipeline completion to instantly rehydrate the mobile websocket!
					if c.hub.queueClient != nil && c.hub.queueClient.Conn() != nil {
						proofTopic := "proof.accounting.cleanup.reconcile.>"
						c.logger.Info("Attempting to bind NATS Core subscriber to Proofs for WebSocket fast-streaming", "topic", proofTopic)

						proofSub, syncErr := c.hub.queueClient.Conn().Subscribe(proofTopic, func(msg *nats.Msg) {
							c.logger.Info("⚡️ [DEBUG] WebSocket DETECTED PIPELINE NATS PROOF!", "realmID", conn.RealmID, "subject", msg.Subject, "len", len(msg.Data))
							c.blastDatabaseCards(conn.RealmID)
						})
						if syncErr == nil {
							c.logger.Info("✅ Successfully bound NATS Proof subscriber for WebSocket streams", "realmID", conn.RealmID)
							go func() {
								<-c.ctx.Done()
								c.logger.Info("🛑 WebSocket Context Closed - unbinding Proof subscriber", "realmID", conn.RealmID)
								proofSub.Unsubscribe()
							}()
						} else {
							c.logger.Error("❌ Failed to bind NATS Proof subscriber for WebSocket", "error", syncErr)
						}
					}
				} else {
					c.logger.Warn("Failed to map Entity ID to QBO Realm ID", "entity", entityID, "error", dbErr)
				}
			}
		}
	} else { // belongs to if entityID, ok := ...
		c.logger.Warn("WebSocket missing EntityID, subscribing to generic unswiped feed (potential leak or misconfig)")
	}

	// Create or bind to JetStream pull consumer locally yielding incoming NATS events
	sub, err := js.PullSubscribe(subjectStr, consumerName, nats.ManualAck())
	if err != nil {
		c.logger.Error("Failed to pull subscribe to cards", "error", err, "subject", subjectStr)
		return
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			// Fetch 1 card at a time to prevent buffering unneeded cards
			msgs, err := sub.Fetch(1, nats.MaxWait(1*time.Second))
			if err != nil {
				if err != nats.ErrTimeout && err != context.DeadlineExceeded {
					c.logger.Error("Pull error reading cards from NATS", "error", err)
				}
				continue
			}

			for _, msg := range msgs {
				// We expect the JSON body wrapped in a message "data" obj
				var cardData map[string]interface{}
				if err := json.Unmarshal(msg.Data, &cardData); err != nil {
					msg.Ack()
					continue
				}

				txnID, ok := cardData["txnId"].(string)
				if !ok {
					// Card has no ID? Cannot be tracked properly
					msg.Ack()
					continue
				}

				c.unackedMu.Lock()
				c.unackedMessages[txnID] = msg
				c.unackedMu.Unlock()

				resp := Message{
					Type: "new_card",
					Data: cardData,
				}
				b, _ := json.Marshal(resp)

				select {
				case c.send <- b:
				case <-time.After(500 * time.Millisecond):
					// Client buffer failed
					c.unackedMu.Lock()
					delete(c.unackedMessages, txnID)
					c.unackedMu.Unlock()
					msg.Nak()
				}
			}
		}
	}
}

// writePump pumps messages from the hub to the websocket connection.
//
// A goroutine running writePump is started for each connection. The
// application ensures that there is at most one writer to a connection by
// executing all writes from this goroutine.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued messages to the current websocket message.
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write(newline)
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// Start begins the client's read and write pumps
func (c *Client) Start() {
	go c.writePump()
	go c.readPump()
	go c.blastActiveWorkflows()
}

func (c *Client) blastDatabaseCards(realmID string) {
	if c.hub == nil || c.hub.db == nil {
		return
	}
	pgRealm := pgtype.Text{String: realmID, Valid: true}
	rows, dbErr := c.hub.db.GetInitialEnrichedTransactionsByRealm(context.Background(), pgRealm)
	if dbErr != nil || len(rows) == 0 {
		return
	}

	compName := "Unknown Company"
	compTax := fignode.CompanyTaxonomy{}
	info, infoErr := c.hub.db.GetCompanyInfo(context.Background(), realmID)
	if infoErr == nil && info.CompanyName != "" {
		compName = info.CompanyName
		compTax, _ = fignode.EnsureCompanyContext(context.Background(), c.hub.db, c.hub.llm, info)
	}

	c.logger.Info("⚡️ Fast Hydrating Fignode Cards Iteratively", "count", len(rows), "realm_id", realmID)

	for _, r := range rows {
		amtVal := 0.0
		if r.RawAmount != "" {
			if parsed, e := strconv.ParseFloat(r.RawAmount, 64); e == nil { amtVal = parsed }
		}

		txType := "expense"
		if amtVal > 0 { txType = "revenue" }

		dateStr := ""
		if r.RawDate.Valid { dateStr = r.RawDate.Time.Format("2006-01-02") }
		desc := ""
		if r.RawDescription.Valid { desc = r.RawDescription.String }
		suggestion := ""
		if r.PredictedAccountName.Valid { suggestion = r.PredictedAccountName.String }

		confVal := 0.0
		if f, e := r.ConfidenceScore.Float64Value(); e == nil && f.Valid { confVal = f.Float64 }

		var accountType string
		if r.PredictedAccountID.Valid {
			if acc, e := c.hub.db.GetAccountByID(context.Background(), r.PredictedAccountID); e == nil {
				accountType = acc.AccountType
			}
		}

		industry := "Unknown"
		industryIcon := "question"
		entityDesc := ""
		entityName := ""

		if txType == "expense" {
			entityName = r.PredictedVendorName.String
			if r.PredictedVendorID.Valid {
				if vendorRec, vErr := c.hub.db.GetVendorByID(context.Background(), r.PredictedVendorID); vErr == nil {
					vTax, _ := fignode.EnsureVendorContext(context.Background(), c.hub.db, c.hub.llm, vendorRec)
					if vTax.Industry != "" { industry = vTax.Industry }
					if vTax.IndustryIcon != "" { industryIcon = vTax.IndustryIcon }
					if vTax.VendorDescription != "" { entityDesc = vTax.VendorDescription }
				}
			}
		} else {
			entityName = r.PredictedCustomerName.String
			if r.PredictedCustomerID.Valid {
				if customerRec, cErr := c.hub.db.GetCustomerByID(context.Background(), r.PredictedCustomerID); cErr == nil {
					cTax, _ := fignode.EnsureCustomerContext(context.Background(), c.hub.db, c.hub.llm, customerRec)
					if cTax.Industry != "" { industry = cTax.Industry }
					if cTax.IndustryIcon != "" { industryIcon = cTax.IndustryIcon }
					if cTax.CustomerDescription != "" { entityDesc = cTax.CustomerDescription }
				}
			}
		}

		cardData := map[string]interface{}{
			"txnId":          uuid.UUID(r.ID.Bytes).String(),
			"companyName":    compName,
			"rawDescription": strings.TrimSpace(desc),
			"industry":       industry,
			"industryIcon":   industryIcon,
			"amount":         amtVal,
			"date":           dateStr,
			"aiSuggestion":   suggestion,
			"aiConfidence":   confVal,
			"status":         r.Status,
			"type":           txType,
			"accountType":    accountType,
			"isRecurring":    r.IsRecurring,
			"clientContext": map[string]interface{}{
				"industry":      compTax.Industry,
				"industryIcon":  compTax.IndustryIcon,
				"businessModel": compTax.BusinessModel,
				"mindsetHint":   compTax.MindsetHint,
				"name":          compName,
			},
		}

		if txType == "expense" {
			cardData["vendor"] = strings.TrimSpace(entityName)
			cardData["vendorDescription"] = entityDesc
		} else {
			cardData["customer"] = strings.TrimSpace(entityName)
			cardData["customerDescription"] = entityDesc
		}

		resp := Message{ Type: "new_card", Data: cardData }
		b, _ := json.Marshal(resp)

		select {
		case c.send <- b:
		case <-time.After(100 * time.Millisecond):
			// If buffer full, skip to avoid deadlock. JS will resend.
		}
	}
}

func (c *Client) blastActiveWorkflows() {
	if c.hub == nil || c.hub.db == nil || c.entityID == "unknown" {
		return
	}

	entityUUID, err := uuid.Parse(c.entityID)
	if err != nil {
		c.logger.Error("Failed to parse entity ID for workflows", "error", err)
		return
	}

	workflows, err := c.hub.db.GetWorkflowsByEntityID(context.Background(), pgtype.UUID{Bytes: entityUUID, Valid: true})
	if err != nil {
		c.logger.Error("Failed to fetch active workflows", "error", err)
		return
	}

	for _, wf := range workflows {
		// Parse state
		var state map[string]interface{}
		if err := json.Unmarshal(wf.State, &state); err != nil {
			continue
		}

		workflowDefName, _ := state["workflow_def"].(string)
		currentStepID, _ := state["current_step_id"].(string)

		activeSteps := []string{}
		if activeMap, ok := state["active_steps"].(map[string]interface{}); ok {
			for k, v := range activeMap {
				if b, ok := v.(bool); ok && b {
					activeSteps = append(activeSteps, k)
				}
			}
		}

		if len(activeSteps) == 0 && currentStepID != "" {
			activeSteps = []string{currentStepID}
		}

		blueprintRow, err := c.hub.db.GetBlueprintByName(context.Background(), workflowDefName)
		if err != nil {
			c.logger.Warn("Failed to fetch blueprint for workflow", "name", workflowDefName, "error", err)
			continue
		}

		var blueprint interface{}
		if err := json.Unmarshal(blueprintRow.Definition, &blueprint); err != nil {
			continue
		}

		evt := map[string]interface{}{
			"instance_id":     uuid.UUID(wf.ID.Bytes).String(),
			"entity_id":       c.entityID,
			"status":          wf.Status,
			"current_step_id": currentStepID,
			"blueprint":       blueprint,
			"active_steps":    activeSteps,
			"timestamp":       time.Now().UTC().Format(time.RFC3339),
		}

		wsMsg, err := NewWorkflowStatusMessage(evt)
		if err != nil {
			continue
		}

		select {
		case c.send <- wsMsg:
		case <-time.After(100 * time.Millisecond):
		}
	}
}
