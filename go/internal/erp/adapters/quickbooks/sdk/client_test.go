package quickbooks

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type mockTransport struct {
	roundTripFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTripFunc(req)
}

func TestParseFailure_ObjectNotFound(t *testing.T) {
	body := `{
		"Fault": {
			"Error": [
				{
					"Message": "Object Not Found",
					"Detail": "Object Not Found : Something you're trying to use has been made inactive.",
					"code": "610",
					"element": ""
				}
			],
			"type": "ValidationFault"
		},
		"time": "2026-02-22T08:00:00.000-07:00"
	}`

	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}

	err := parseFailure(resp)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if _, ok := err.(ObjectNotFoundError); !ok {
		t.Errorf("expected ObjectNotFoundError, got %T: %v", err, err)
	}

	if !IsQBOApplicationError(err) {
		t.Errorf("expected IsQBOApplicationError to be true")
	}
}

func TestCircuitBreaker(t *testing.T) {
	client, _ := NewClient("test", "test", "test", false, "65", nil, nil)
	client.Client = &http.Client{
		Transport: &mockTransport{
			roundTripFunc: func(req *http.Request) (*http.Response, error) {
				return nil, errors.New("network error")
			},
		},
	}

	var err error
	// The settings are 5 requests with 60% failure rate
	for i := 0; i < 5; i++ {
		err = client.Query("select * from Account", nil)
		if err == nil {
			t.Fatal("expected error on trip", i)
		}
	}

	// The 6th request should fail due to Open breaker without calling roundTripFunc
	client.Client.Transport = &mockTransport{
		roundTripFunc: func(req *http.Request) (*http.Response, error) {
			t.Fatal("should not reach network when circuit is open")
			return nil, nil
		},
	}
	err = client.Query("select * from Account", nil)
	if err == nil || !strings.Contains(err.Error(), "circuit breaker is open") {
		t.Fatalf("expected circuit breaker open error, got %v", err)
	}
}

func TestConcurrencyLimit(t *testing.T) {
	client, _ := NewClient("test", "test", "test", false, "65", nil, nil)

	var inFlight int32
	var maxInFlight int32
	var mu sync.Mutex

	client.Client = &http.Client{
		Transport: &mockTransport{
			roundTripFunc: func(req *http.Request) (*http.Response, error) {
				atomic.AddInt32(&inFlight, 1)

				mu.Lock()
				current := atomic.LoadInt32(&inFlight)
				if current > maxInFlight {
					maxInFlight = current
				}
				mu.Unlock()

				// Slight delay to ensure we test concurrency
				time.Sleep(20 * time.Millisecond)

				atomic.AddInt32(&inFlight, -1)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBufferString("{}")),
				}, nil
			},
		},
	}

	// Launch 20 concurrent
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = client.Query("select * from Account", nil)
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if maxInFlight > 10 {
		t.Errorf("expected max configured concurrency to be 10, got %v", maxInFlight)
	}
}
