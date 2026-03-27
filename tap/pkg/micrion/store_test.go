package micrion

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nats-io/nats.go"
)

type mockLedger struct {
	purchased    int64
	burned       int64
	lastRevision uint64
	mu           sync.Mutex
}

func (m *mockLedger) LogPurchase(ctx context.Context, entityID string, stripeSessionID string, usdAmount int64, micrionAmount int64) error {
	m.mu.Lock()
	m.purchased += micrionAmount
	m.mu.Unlock()
	return nil
}

func (m *mockLedger) LogBulkBurn(ctx context.Context, entityID string, agentDID string, burnedAmount int64, natsRevision uint64) error {
	m.mu.Lock()
	m.burned += burnedAmount
	m.lastRevision = natsRevision
	m.mu.Unlock()
	return nil
}

func (m *mockLedger) GetFiatPurchased(ctx context.Context, entityID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.purchased, nil
}

func TestMicrionStateChannels(t *testing.T) {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = nats.DefaultURL
	}
	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Skipf("NATS not available at %s, skipping Micrion tests: %v", natsURL, err)
	}
	defer nc.Close()

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("Failed to create JetStream context: %v", err)
	}

	_ = js.DeleteKeyValue(BucketName)

	kv, err := SetupKV(js)
	if err != nil {
		t.Fatalf("Failed to setup KV: %v", err)
	}
	defer js.DeleteKeyValue(BucketName)

	ledger := &mockLedger{}
	wm := NewWalletManager(ledger, kv)
	ctx := context.Background()
	entityID := "test-entity-123"

	t.Run("TopUp And MicroBurn Sequence", func(t *testing.T) {
		agentDID := "did:toro:testagent123"

		// Initial burn should fail with AgentNotFound
		_, err := wm.MicroBurn(ctx, agentDID, 1616)
		if !errors.Is(err, ErrAgentNotFound) {
			t.Errorf("Expected ErrAgentNotFound, got %v", err)
		}

		// TopUp 10 Million Micrions
		err = wm.TopUp(agentDID, 10_000_000, entityID)
		if err != nil {
			t.Fatalf("Failed to TopUp: %v", err)
		}

		// Successful MicroBurn
		newBal, err := wm.MicroBurn(ctx, agentDID, 1616)
		if err != nil {
			t.Fatalf("Failed to MicroBurn: %v", err)
		}
		if newBal != 10_000_000-1616 {
			t.Errorf("Expected balance %d, got %d", 10_000_000-1616, newBal)
		}

		// Exhaust balance
		_, err = wm.MicroBurn(ctx, agentDID, 10_000_000)
		if !errors.Is(err, ErrPaymentRequired) {
			t.Errorf("Expected ErrPaymentRequired, got %v", err)
		}
	})

	t.Run("Concurrent CAS Operations and 50-Tx Rollup", func(t *testing.T) {
		agentDID := "did:toro:concurrent_agent"
		
		err := wm.TopUp(agentDID, 100_000, entityID)
		if err != nil {
			t.Fatalf("Initial TopUp failed: %v", err)
		}

		// Run exactly 50 burns to trigger the synchronous LogBulkBurn
		const routines = 50
		const toll = 1000

		var wg sync.WaitGroup
		wg.Add(routines)

		var successCount int32
		errs := make(chan error, routines)

		for i := 0; i < routines; i++ {
			go func() {
				defer wg.Done()
				_, burnErr := wm.MicroBurn(ctx, agentDID, toll)
				if burnErr != nil {
					errs <- burnErr
				} else {
					atomic.AddInt32(&successCount, 1)
				}
			}()
		}

		wg.Wait()
		close(errs)

		for e := range errs {
			t.Errorf("Concurrent burn error: %v", e)
		}

		if successCount != routines {
			t.Errorf("Expected %d successful burns, got %d", routines, successCount)
		}

		state, err := GetExecutionState(kv, agentDID)
		if err != nil {
			t.Fatalf("Failed to get final state: %v", err)
		}

		// After 50th transaction, UncommittedBurns and UncommittedCount should be exactly 0
		if state.Balance != 50000 {
			t.Errorf("Expected balance 50000, got %d", state.Balance)
		}
		if state.UncommittedBurns != 0 {
			t.Errorf("Expected uncommitted burns 0 after rollup, got %d", state.UncommittedBurns)
		}
		if state.UncommittedCount != 0 {
			t.Errorf("Expected uncommitted count 0 after rollup, got %d", state.UncommittedCount)
		}

		ledger.mu.Lock()
		if ledger.burned != 50000 {
			t.Errorf("Expected FiatLedger to have permanently recorded 50000 burned micrions, got %d", ledger.burned)
		}
		ledger.mu.Unlock()
	})

	t.Run("Middleware Execution", func(t *testing.T) {
		agentDID := "did:toro:http_agent"
		err := wm.TopUp(agentDID, 1616, entityID)
		if err != nil {
			t.Fatalf("Failed TopUp: %v", err)
		}

		mw := TollboothMiddleware(wm, 1616)
		handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Success"))
		}))

		req, _ := http.NewRequest(http.MethodGet, "/almanac/suppliers", nil)
		req.Header.Set("X-Agent-DID", agentDID)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusOK {
			t.Errorf("Expected HTTP 200, got %v", status)
		}

		req2, _ := http.NewRequest(http.MethodGet, "/almanac/suppliers", nil)
		req2.Header.Set("X-Agent-DID", agentDID)
		rr2 := httptest.NewRecorder()

		handler.ServeHTTP(rr2, req2)

		if status := rr2.Code; status != http.StatusPaymentRequired {
			t.Errorf("Expected HTTP 402, got %v", status)
		}
	})
}
