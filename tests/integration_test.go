package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"infermeshai/internal/api"
	"infermeshai/internal/app"
	"infermeshai/internal/backend"
	"infermeshai/internal/cli"
	"infermeshai/internal/control"
	"infermeshai/internal/model"
	"infermeshai/internal/p2p"
	"infermeshai/internal/routing"
)

func TestIntegrationStreamingSingleMachine(t *testing.T) {
	mock := newMockBackend(t, 5*time.Millisecond, -1)
	defer mock.Close()

	providerURL := startProvider(t, mock.URL, "peer-good", []model.ModelSpec{{Name: "llama3"}})
	clientURL := startClient(t, []string{providerURL}, app.ClientOptions{PeerID: "client-a"})

	payload := model.ChatCompletionRequest{
		Model:    "llama3",
		Stream:   true,
		Messages: []model.Message{{Role: "user", Content: "hello mesh"}},
	}
	body := postJSON(t, clientURL+"/v1/chat/completions", payload)
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("expected stream done marker, got: %s", body)
	}
	if !strings.Contains(body, "mock:") {
		t.Fatalf("expected mock response content, got: %s", body)
	}
}

func TestIntegrationPeerFailover(t *testing.T) {
	mock := newMockBackend(t, 1*time.Millisecond, -1)
	defer mock.Close()

	goodProvider := startProvider(t, mock.URL, "peer-good", []model.ModelSpec{{Name: "llama3"}})
	deadProvider := "http://127.0.0.1:65530"
	clientURL := startClient(t, []string{deadProvider, goodProvider}, app.ClientOptions{PeerID: "client-a"})

	payload := model.ChatCompletionRequest{Model: "llama3", Stream: true, Messages: []model.Message{{Role: "user", Content: "failover"}}}
	body := postJSON(t, clientURL+"/v1/chat/completions", payload)
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("expected successful failover stream, got: %s", body)
	}
}

func TestChaosMidStreamDisconnect(t *testing.T) {
	mock := newMockBackend(t, 1*time.Millisecond, 1)
	defer mock.Close()
	providerURL := startProvider(t, mock.URL, "peer-chaos", []model.ModelSpec{{Name: "llama3"}})
	clientURL := startClient(t, []string{providerURL}, app.ClientOptions{PeerID: "client-a"})

	payload := model.ChatCompletionRequest{Model: "llama3", Stream: true, Messages: []model.Message{{Role: "user", Content: "a b c d"}}}
	body := postJSON(t, clientURL+"/v1/chat/completions", payload)
	if strings.Contains(body, "data: [DONE]") {
		t.Fatalf("expected truncated stream without done marker, got: %s", body)
	}
}

func TestProviderQuotaEnforcement(t *testing.T) {
	mock := newMockBackend(t, 50*time.Millisecond, -1)
	defer mock.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cap := app.NewCapabilityState(model.CapabilityDocument{
		PeerID:      "peer-q",
		Models:      []model.ModelSpec{{Name: "llama3"}},
		Reliability: 1,
		GatewayURL:  "http://example.invalid",
	})
	limiter := control.NewLimiter(1, 100, 100)
	providerSvc := app.NewProviderService(
		backend.NewHTTPAdapter(mock.URL, 30*time.Second),
		limiter,
		cap,
		control.ProviderPolicy{},
		nil,
		nil,
		nil,
		logger,
	)
	s := api.NewServer(logger)
	s.HandleProviderInference(providerSvc)
	server := httptest.NewServer(s.Handler())
	defer server.Close()

	payload := model.ChatCompletionRequest{Model: "llama3", Stream: true, Messages: []model.Message{{Role: "user", Content: "quota"}}}
	buf, _ := json.Marshal(payload)

	firstDone := make(chan struct{})
	go func() {
		resp, _ := http.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(buf))
		if resp != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		close(firstDone)
	}()
	time.Sleep(5 * time.Millisecond)

	resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", resp.StatusCode)
	}
	<-firstDone
}

func TestModelFingerprintRouting(t *testing.T) {
	mock := newMockBackend(t, 1*time.Millisecond, -1)
	defer mock.Close()
	providerURL := startProvider(t, mock.URL, "peer-fp", []model.ModelSpec{{Name: "llama3", SHA256: "abc123", Tokenizer: "tok", Quant: "Q4", Backend: "lmstudio"}})
	clientURL := startClient(t, []string{providerURL}, app.ClientOptions{PeerID: "client-a"})

	okReq := model.ChatCompletionRequest{
		Model: "llama3", Stream: true,
		Requirements: model.ModelRequirements{SHA256: "abc123", Tokenizer: "tok", Quant: "Q4", Backend: "lmstudio"},
		Messages:     []model.Message{{Role: "user", Content: "hello"}},
	}
	body := postJSON(t, clientURL+"/v1/chat/completions", okReq)
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("expected successful fingerprint-routed stream")
	}

	badReq := okReq
	badReq.Requirements.SHA256 = "mismatch"
	resp, bodyStr := postJSONStatus(t, clientURL+"/v1/chat/completions", badReq)
	if resp != http.StatusBadGateway {
		t.Fatalf("expected 502 when no compatible peers, got %d body=%s", resp, bodyStr)
	}
}

func TestPoWEnforcement(t *testing.T) {
	mock := newMockBackend(t, 1*time.Millisecond, -1)
	defer mock.Close()
	providerURL := startProviderWithPolicy(t, mock.URL, "peer-pow", []model.ModelSpec{{Name: "llama3"}}, control.ProviderPolicy{PoWDifficulty: 1})
	clientURL := startClient(t, []string{providerURL}, app.ClientOptions{PeerID: "client-a", PoWDifficulty: 1})
	body := postJSON(t, clientURL+"/v1/chat/completions", model.ChatCompletionRequest{Model: "llama3", Stream: true, Messages: []model.Message{{Role: "user", Content: "pow"}}})
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("expected successful pow-protected stream")
	}
}

func TestCreditFairness(t *testing.T) {
	mock := newMockBackend(t, 1*time.Millisecond, -1)
	defer mock.Close()
	providerURL := startProvider(t, mock.URL, "peer-credit", []model.ModelSpec{{Name: "llama3"}})
	clientURL := startClient(t, []string{providerURL}, app.ClientOptions{PeerID: "client-a", CreditLedger: control.NewCreditLedger(1)})
	ok := model.ChatCompletionRequest{Model: "llama3", Stream: true, Messages: []model.Message{{Role: "user", Content: "one"}}}
	_ = postJSON(t, clientURL+"/v1/chat/completions", ok)
	resp, body := postJSONStatus(t, clientURL+"/v1/chat/completions", ok)
	if resp != http.StatusBadGateway || !strings.Contains(body, "insufficient credits") {
		t.Fatalf("expected insufficient credits failure, status=%d body=%s", resp, body)
	}
}

func TestClientPeerListEndpoint(t *testing.T) {
	mock := newMockBackend(t, 1*time.Millisecond, -1)
	defer mock.Close()
	providerURL := startProvider(t, mock.URL, "peer-list", []model.ModelSpec{{Name: "llama3"}})
	clientURL := startClient(t, []string{providerURL}, app.ClientOptions{PeerID: "client-a"})

	resp, err := http.Get(clientURL + "/v1/mesh/peers")
	if err != nil {
		t.Fatalf("get peers: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(b))
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode peers payload: %v", err)
	}
	peersAny, ok := payload["peers"].([]any)
	if !ok || len(peersAny) == 0 {
		t.Fatalf("expected non-empty peers list: %+v", payload)
	}
	foundSelf := false
	for _, p := range peersAny {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if pm["peer_id"] == "client-a" {
			if self, _ := pm["self"].(bool); self {
				foundSelf = true
			}
		}
	}
	if !foundSelf {
		t.Fatalf("expected self peer entry in peers payload: %+v", payload)
	}
}

func TestClientRoutingControlEndpoints(t *testing.T) {
	mock := newMockBackend(t, 1*time.Millisecond, -1)
	defer mock.Close()
	providerURL := startProvider(t, mock.URL, "peer-ctl", []model.ModelSpec{{Name: "llama3"}})
	clientURL := startClient(t, []string{providerURL}, app.ClientOptions{PeerID: "client-a"})

	resp, err := http.Get(clientURL + "/v1/mesh/routing")
	if err != nil {
		t.Fatalf("get routing: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body := `{"routing_policy":"manual","allowed_peers":["peer-ctl"]}`
	req, _ := http.NewRequest(http.MethodPut, clientURL+"/v1/mesh/routing", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("put routing: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp2.Body)
		t.Fatalf("expected 200, got %d body=%s", resp2.StatusCode, string(b))
	}

	resp3, err := http.Post(clientURL+"/v1/mesh/select/peer-ctl", "application/json", nil)
	if err != nil {
		t.Fatalf("post select: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp3.StatusCode)
	}
}

func TestProviderCapabilitiesEndpoint(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cap := app.NewCapabilityState(model.CapabilityDocument{
		PeerID:     "peer-cap",
		NodeName:   "provider-a",
		Models:     []model.ModelSpec{{Name: "llama3-8b"}},
		GatewayURL: "libp2p://peer-cap",
	})
	s := api.NewServer(logger)
	s.HandleCapabilities(cap)
	server := httptest.NewServer(s.Handler())
	defer server.Close()

	resp, err := http.Get(server.URL + "/capabilities")
	if err != nil {
		t.Fatalf("get capabilities: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, string(b))
	}
	var doc model.CapabilityDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode capability doc: %v", err)
	}
	if doc.PeerID != "peer-cap" || len(doc.Models) != 1 || doc.Models[0].Name != "llama3-8b" {
		t.Fatalf("unexpected capability doc: %+v", doc)
	}
}

func TestChatCLIEndToEndAgainstClientDaemon(t *testing.T) {
	mock := newMockBackend(t, 1*time.Millisecond, -1)
	defer mock.Close()
	providerURL := startProvider(t, mock.URL, "peer-chat", []model.ModelSpec{{Name: "llama3"}})
	clientURL := startClient(t, []string{providerURL}, app.ClientOptions{PeerID: "client-chat"})

	r := cli.NewChatRunner(cli.ChatOptions{
		BaseURL: clientURL,
		Model:   "llama3",
	})
	var out bytes.Buffer
	if err := r.SendPrompt(context.Background(), "hello from chat", &out); err != nil {
		t.Fatalf("send prompt via chat cli: %v", err)
	}
	if !strings.Contains(out.String(), "mock:") {
		t.Fatalf("expected streamed mock response, got: %s", out.String())
	}
}

func startProvider(t *testing.T, backendURL, peerID string, models []model.ModelSpec) string {
	return startProviderWithPolicy(t, backendURL, peerID, models, control.ProviderPolicy{})
}

func startProviderWithPolicy(t *testing.T, backendURL, peerID string, models []model.ModelSpec, policy control.ProviderPolicy) string {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cap := app.NewCapabilityState(model.CapabilityDocument{
		PeerID:       peerID,
		Models:       models,
		GPU:          "test-gpu",
		VRAMFreeMB:   16000,
		AvgLatencyMS: 10,
		Reliability:  1,
	})
	providerSvc := app.NewProviderService(
		backend.NewHTTPAdapter(backendURL, 30*time.Second),
		control.NewLimiter(4, 100, 100),
		cap,
		policy,
		nil,
		control.NewCreditLedger(0),
		control.NewReputationTracker(),
		logger,
	)
	s := api.NewServer(logger)
	s.HandleCapabilities(cap)
	s.HandleProviderInference(providerSvc)
	server := httptest.NewServer(s.Handler())
	t.Cleanup(server.Close)
	return server.URL
}

func startClient(t *testing.T, providerURLs []string, opts app.ClientOptions) string {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	clientSvc := app.NewClientService(
		p2p.NewHTTPDiscovery(providerURLs, 3*time.Second),
		routing.NewRouter(model.RoutingWeights{Latency: 1, QueueDepth: 1, VRAMPressure: 1, Reliability: 1}),
		p2p.NewHTTPGatewayClient(30*time.Second),
		opts,
		logger,
	)
	s := api.NewServer(logger)
	s.HandleClientInference(clientSvc)
	s.HandleClientPeerList(clientSvc)
	s.HandleClientRoutingControl(clientSvc)
	server := httptest.NewServer(s.Handler())
	t.Cleanup(server.Close)
	return server.URL
}

func newMockBackend(t *testing.T, tokenLatency time.Duration, failAfter int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		var req model.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		prompt := ""
		if len(req.Messages) > 0 {
			prompt = req.Messages[len(req.Messages)-1].Content
		}
		if !req.Stream {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"mock:` + prompt + `"},"finish_reason":"stop"}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		toks := strings.Fields("mock: " + prompt)
		for i, tok := range toks {
			if failAfter >= 0 && i >= failAfter {
				if hj, ok := w.(http.Hijacker); ok {
					conn, _, _ := hj.Hijack()
					_ = conn.Close()
				}
				return
			}
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"" + tok + " \"}}]}\n\n"))
			fl.Flush()
			time.Sleep(tokenLatency)
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	})
	return httptest.NewServer(mux)
}

func postJSON(t *testing.T, url string, req any) string {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("post request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, string(body))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return string(body)
}

func postJSONStatus(t *testing.T, url string, req any) (int, string) {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("post request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}
