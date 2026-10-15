package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOneToolCallAndModelName(t *testing.T) {
	var seen modelCapture
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("auth %s", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &seen.req); err != nil {
			t.Error(err)
		}
		writeChoice(w, chatMessage{
			ToolCalls: []chatToolCall{{
				ID: "call-1", Type: "function",
				Function: chatFunctionCall{Name: "analyze", Arguments: `{"metrics":[]}`},
			}},
		})
	}))
	defer server.Close()

	resp, err := (XAI{APIKey: "test-key", Model: "grok-4.6", BaseURL: server.URL}).Complete(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "How many records?"}},
		Tools:    []Tool{{Name: "analyze", Description: "aggregate", Parameters: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen.req.Model != "grok-4.6" {
		t.Fatalf("model %s", seen.req.Model)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "analyze" {
		t.Fatalf("%+v", resp)
	}
}

func TestRateLimitThenSuccess(t *testing.T) {
	var n atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			http.Error(w, `{"error":"slow down"}`, http.StatusTooManyRequests)
			return
		}
		writeChoice(w, chatMessage{Content: "42 trips"})
	}))
	defer server.Close()
	resp, err := (XAI{APIKey: "k", Model: "grok-4.6", BaseURL: server.URL}).Complete(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "count"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "42 trips" || n.Load() != 3 {
		t.Fatalf("%+v attempts %d", resp, n.Load())
	}
}

func TestRateLimitExhaustedAndRedaction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `key secret-value was rejected`, http.StatusTooManyRequests)
	}))
	defer server.Close()
	_, err := (XAI{APIKey: "secret-value", Model: "grok-4.6", BaseURL: server.URL}).Complete(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "count"}},
	})
	var api *APIError
	if !errors.As(err, &api) || api.Status != http.StatusTooManyRequests {
		t.Fatalf("%v", err)
	}
	if strings.Contains(err.Error(), "secret-value") || strings.Contains(api.Detail(), "secret-value") {
		t.Fatalf("secret leaked %q %q", err.Error(), api.Detail())
	}
}

func TestServerErrorAndMalformed(t *testing.T) {
	var n atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer server.Close()
	_, err := (XAI{APIKey: "k", Model: "grok-4.6", BaseURL: server.URL}).Complete(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "count"}},
	})
	var api *APIError
	if !errors.As(err, &api) || api.Status != http.StatusBadGateway || n.Load() != 3 {
		t.Fatalf("%v attempts %d", err, n.Load())
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer bad.Close()
	_, err = (XAI{APIKey: "k", Model: "grok-4.6", BaseURL: bad.URL}).Complete(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "count"}},
	})
	if err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("%v", err)
	}
}

func TestTimeoutAndCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, err := (XAI{APIKey: "k", Model: "grok-4.6", BaseURL: server.URL}).Complete(ctx, Request{
		Messages: []Message{{Role: "user", Content: "count"}},
	})
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}

type modelCapture struct {
	req chatRequest
}

func writeChoice(w http.ResponseWriter, msg chatMessage) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(chatResponse{Choices: []struct {
		Message chatMessage `json:"message"`
	}{{Message: msg}}})
}
