package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryOnRateLimitThenSucceed(t *testing.T) {
	oldBase, oldMax := retryBaseDelay, maxRetryAttempts
	retryBaseDelay = time.Millisecond
	maxRetryAttempts = 5
	defer func() { retryBaseDelay, maxRetryAttempts = oldBase, oldMax }()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n <= 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`))
	}))
	defer srv.Close()

	c := &LLMClient{
		baseURL:    srv.URL,
		model:      "test-model",
		httpClient: srv.Client(),
	}
	resp, err := c.ChatWithProviderStream(context.Background(), "", "test-model", []LLMMessage{{Role: RoleUser, Content: "hi"}}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "ok" {
		t.Fatalf("content = %q", resp.Content)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("calls = %d, want 3", got)
	}
}

func TestRetryExhaustedReturnsError(t *testing.T) {
	oldBase, oldMax := retryBaseDelay, maxRetryAttempts
	retryBaseDelay = time.Millisecond
	maxRetryAttempts = 3
	defer func() { retryBaseDelay, maxRetryAttempts = oldBase, oldMax }()

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"unavailable"}`))
	}))
	defer srv.Close()

	c := &LLMClient{baseURL: srv.URL, model: "test-model", httpClient: srv.Client()}
	_, err := c.ChatWithProviderStream(context.Background(), "", "test-model", []LLMMessage{{Role: RoleUser, Content: "hi"}}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("calls = %d, want 3", got)
	}
}

func TestIsRetryableStatus(t *testing.T) {
	for _, code := range []int{408, 425, 429, 500, 502, 503, 504} {
		if !isRetryableStatus(code) {
			t.Fatalf("%d should be retryable", code)
		}
	}
	for _, code := range []int{200, 400, 401, 403, 404, 422} {
		if isRetryableStatus(code) {
			t.Fatalf("%d should not be retryable", code)
		}
	}
}

func TestRetryAfterDelay(t *testing.T) {
	if d := retryAfterDelay("2"); d != 2*time.Second {
		t.Fatalf("got %s", d)
	}
	if d := retryAfterDelay(""); d != 0 {
		t.Fatalf("got %s", d)
	}
}
