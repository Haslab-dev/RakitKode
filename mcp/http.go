package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// httpTransport speaks JSON-RPC over streamable HTTP (POST + JSON or SSE
// response). It also carries the Mcp-Session-Id header returned by initialize.
type httpTransport struct {
	url     string
	headers map[string]string
	client  *http.Client

	mu        sync.Mutex
	nextID    int64
	sessionID string
	closed    bool

	notifyMu       sync.RWMutex
	onNotification func(method string, params json.RawMessage)
}

func newHTTPTransport(cfg ServerConfig) (*httpTransport, error) {
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, fmt.Errorf("mcp server %q: url is required for remote transport", cfg.Name)
	}
	headers := map[string]string{}
	for k, v := range cfg.Headers {
		headers[k] = v
	}
	if auth := cfg.Env["AUTHORIZATION"]; auth != "" {
		headers["Authorization"] = auth
	} else if auth := cfg.Env["MCP_API_KEY"]; auth != "" {
		headers["Authorization"] = "Bearer " + auth
	}
	return &httpTransport{
		url:     cfg.URL,
		headers: headers,
		client:  &http.Client{Timeout: 120 * time.Second},
	}, nil
}

func (t *httpTransport) setNotificationHandler(fn func(method string, params json.RawMessage)) {
	t.notifyMu.Lock()
	t.onNotification = fn
	t.notifyMu.Unlock()
}

func (t *httpTransport) nextRequestID() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextID++
	return t.nextID
}

func (t *httpTransport) sessionHeader() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessionID
}

func (t *httpTransport) post(ctx context.Context, payload any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	if sid := t.sessionHeader(); sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.mu.Lock()
		t.sessionID = sid
		t.mu.Unlock()
	}
	return resp, nil
}

func (t *httpTransport) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := t.nextRequestID()
	req := jsonRpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	resp, err := t.post(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("mcp http %s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("mcp http %s: status %d: %s", method, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/event-stream") {
		return readSSEResponse(resp.Body, id)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return json.RawMessage("{}"), nil
	}
	return decodeRPCResponse(body, id)
}

func (t *httpTransport) notify(method string, params any) error {
	msg := jsonRpcNotification{JSONRPC: "2.0", Method: method, Params: params}
	resp, err := t.post(context.Background(), msg)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("mcp http notification %s: status %d", method, resp.StatusCode)
	}
	return nil
}

func (t *httpTransport) close() {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
}

func readSSEResponse(r io.Reader, id int64) (json.RawMessage, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		if res, ok := tryDecodeRPCResponse([]byte(data), id); ok {
			return res, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("mcp sse: no response for request %d", id)
}

func decodeRPCResponse(body []byte, id int64) (json.RawMessage, error) {
	if res, ok := tryDecodeRPCResponse(body, id); ok {
		return res, nil
	}
	return nil, fmt.Errorf("mcp http: invalid JSON-RPC response")
}

func tryDecodeRPCResponse(body []byte, id int64) (json.RawMessage, bool) {
	var envelope struct {
		ID     any             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *jsonRpcError   `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return nil, false
	}
	if envelope.ID != nil {
		var got int64
		switch v := envelope.ID.(type) {
		case float64:
			got = int64(v)
		case json.Number:
			n, _ := v.Int64()
			got = n
		}
		if got != 0 && got != id {
			return nil, false
		}
	}
	if envelope.Error != nil {
		return nil, false
	}
	return envelope.Result, true
}
