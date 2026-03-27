package micrion

import (
	"errors"
	"log"
	"net/http"
	"strconv"
)

// TollboothMiddleware acts as the API Gateway interceptor for queries that require a toll.
func TollboothMiddleware(wm *WalletManager, toll int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// For this implementation, extract the Agent identity from a standard header.
			agentDID := r.Header.Get("X-Agent-DID")
			if agentDID == "" {
				http.Error(w, "X-Agent-DID header required", http.StatusUnauthorized)
				return
			}

			// Immediately deduct the toll via the NATS CAS operation
			newBal, err := wm.MicroBurn(r.Context(), agentDID, toll)
			if err != nil {
				if errors.Is(err, ErrPaymentRequired) || errors.Is(err, ErrAgentNotFound) {
					// Toro Depletion & Breakage Logic:
					// Instantly drops the request with a 402 HTTP status if funds are insufficient.
					http.Error(w, err.Error(), http.StatusPaymentRequired)
					return
				}

				log.Printf("[Tollbooth] Error deducting micrions for %s: %v", agentDID, err)
				http.Error(w, "Internal Execution Error", http.StatusInternalServerError)
				return
			}

			log.Printf("[Tollbooth] Authorized %s: deducted %d µC, new balance %d µC", agentDID, toll, newBal)

			// Expose the new balance to downstream handlers or back to the client
			w.Header().Set("X-Micrion-Balance", strconv.FormatInt(newBal, 10))

			next.ServeHTTP(w, r)
		})
	}
}
