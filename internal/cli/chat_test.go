package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendPromptStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello \"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"world\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewChatRunner(ChatOptions{BaseURL: srv.URL, Model: "llama3"})
	var out bytes.Buffer
	if err := c.SendPrompt(context.Background(), "hi", &out); err != nil {
		t.Fatalf("send prompt: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "hello world") {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestRunREPLCommands(t *testing.T) {
	var selectedPeer string
	var routingPolicy string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mesh/peers":
			_, _ = io.WriteString(w, `{"peers":[{"peer_id":"p-self","self":true,"node_name":"local"},{"peer_id":"p-1","node_name":"remote"}]}`)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/mesh/select/"):
			selectedPeer = strings.TrimPrefix(r.URL.Path, "/v1/mesh/select/")
			_, _ = io.WriteString(w, `{}`)
		case r.Method == http.MethodPut && r.URL.Path == "/v1/mesh/routing":
			b, _ := io.ReadAll(r.Body)
			routingPolicy = string(b)
			_, _ = io.WriteString(w, `{}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	in := strings.NewReader("/\n/model llama3\n/peers\n/use p-1\n/routing manual\nhello\n/quit\n")
	var out bytes.Buffer
	var errOut bytes.Buffer
	c := NewChatRunner(ChatOptions{BaseURL: srv.URL, Model: "x"})
	if err := c.RunREPL(context.Background(), in, &out, &errOut); err != nil {
		t.Fatalf("run repl: %v", err)
	}
	if selectedPeer != "p-1" {
		t.Fatalf("expected selected peer p-1, got %s", selectedPeer)
	}
	if !strings.Contains(routingPolicy, `"routing_policy":"manual"`) {
		t.Fatalf("routing payload missing policy: %s", routingPolicy)
	}
	if !strings.Contains(out.String(), "ok") {
		t.Fatalf("chat output missing token: %q", out.String())
	}
	if !strings.Contains(out.String(), "slash menu:") {
		t.Fatalf("expected slash menu output, got: %q", out.String())
	}
}

func TestSendPromptNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()
	c := NewChatRunner(ChatOptions{BaseURL: srv.URL, Model: "llama3"})
	err := c.SendPrompt(context.Background(), "hi", io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "status=502") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestModelSelectionByIndexAutoSelectsSingleProvider(t *testing.T) {
	var selectedPeer string
	var routingPolicy string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mesh/peers":
			_, _ = io.WriteString(w, `{"peers":[{"peer_id":"p-1","node_name":"remote-a","models":[{"name":"m1"}]},{"peer_id":"p-2","node_name":"remote-b","models":[{"name":"m2"}]}]}`)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/mesh/select/"):
			selectedPeer = strings.TrimPrefix(r.URL.Path, "/v1/mesh/select/")
			_, _ = io.WriteString(w, `{}`)
		case r.Method == http.MethodPut && r.URL.Path == "/v1/mesh/routing":
			b, _ := io.ReadAll(r.Body)
			routingPolicy = string(b)
			_, _ = io.WriteString(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	in := strings.NewReader("/model 1\n/quit\n")
	var out bytes.Buffer
	var errOut bytes.Buffer
	c := NewChatRunner(ChatOptions{BaseURL: srv.URL, Model: "x"})
	if err := c.RunREPL(context.Background(), in, &out, &errOut); err != nil {
		t.Fatalf("run repl: %v", err)
	}
	if selectedPeer != "p-1" {
		t.Fatalf("expected selected peer p-1, got %q", selectedPeer)
	}
	if !strings.Contains(routingPolicy, `"routing_policy":"manual"`) {
		t.Fatalf("expected manual routing update, got %s", routingPolicy)
	}
}

func TestModelsCommandListsAggregatedModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mesh/peers":
			_, _ = io.WriteString(w, `{"peers":[{"peer_id":"p-1","node_name":"remote-a","models":[{"name":"m1"},{"name":"m2"}]},{"peer_id":"p-2","node_name":"remote-b","models":[{"name":"m1"}]}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	in := strings.NewReader("/models\n/quit\n")
	var out bytes.Buffer
	var errOut bytes.Buffer
	c := NewChatRunner(ChatOptions{BaseURL: srv.URL, Model: "x"})
	if err := c.RunREPL(context.Background(), in, &out, &errOut); err != nil {
		t.Fatalf("run repl: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "m1 providers=2") || !strings.Contains(got, "m2 providers=1") {
		t.Fatalf("unexpected models output: %q", got)
	}
}

func TestEnsureModelAvailableAutoSelectsDiscoveredModel(t *testing.T) {
	var selectedPeer string
	var routingPolicy string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mesh/peers":
			_, _ = io.WriteString(w, `{"peers":[{"peer_id":"p-1","node_name":"remote-a","models":[{"name":"smollm2:135m"}],"avg_latency_ms":20,"historical_reliability":1}]}`)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/mesh/select/"):
			selectedPeer = strings.TrimPrefix(r.URL.Path, "/v1/mesh/select/")
			_, _ = io.WriteString(w, `{}`)
		case r.Method == http.MethodPut && r.URL.Path == "/v1/mesh/routing":
			b, _ := io.ReadAll(r.Body)
			routingPolicy = string(b)
			_, _ = io.WriteString(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewChatRunner(ChatOptions{BaseURL: srv.URL, Model: "llama3-8b"})
	var out bytes.Buffer
	if err := c.EnsureModelAvailable(context.Background(), &out, false); err != nil {
		t.Fatalf("ensure model available: %v", err)
	}
	if c.opts.Model != "smollm2:135m" {
		t.Fatalf("expected model auto-selected to smollm2:135m, got %q", c.opts.Model)
	}
	if selectedPeer != "" {
		t.Fatalf("expected no peer preselection in auto mode, got %q", selectedPeer)
	}
	if !strings.Contains(routingPolicy, `"routing_policy":"auto"`) {
		t.Fatalf("expected auto routing update on startup, got %q", routingPolicy)
	}
	if !strings.Contains(out.String(), "auto-selected model: smollm2:135m") {
		t.Fatalf("missing auto-selected model output: %q", out.String())
	}
}

func TestUseSelectsPeerAndAutoSetsSinglePeerModel(t *testing.T) {
	var selectedPeer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mesh/peers":
			_, _ = io.WriteString(w, `{"peers":[{"peer_id":"p-1","node_name":"remote-a","models":[{"name":"m-a"}]}]}`)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/mesh/select/"):
			selectedPeer = strings.TrimPrefix(r.URL.Path, "/v1/mesh/select/")
			_, _ = io.WriteString(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	in := strings.NewReader("/use 1\n/quit\n")
	var out bytes.Buffer
	var errOut bytes.Buffer
	c := NewChatRunner(ChatOptions{BaseURL: srv.URL, Model: "x"})
	if err := c.RunREPL(context.Background(), in, &out, &errOut); err != nil {
		t.Fatalf("run repl: %v", err)
	}
	if selectedPeer != "p-1" {
		t.Fatalf("expected selected peer p-1, got %q", selectedPeer)
	}
	if !strings.Contains(out.String(), "model: m-a") {
		t.Fatalf("expected auto model set output, got: %q", out.String())
	}
}

func TestStatusShowsCurrentModelAndManualPeer(t *testing.T) {
	peerID := "12D3KooWSneySvuiVBXfpwmUrD7BvX7ETWoL8ipZhgQXzLy2L8dg"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mesh/routing":
			_, _ = io.WriteString(w, `{"routing_policy":"manual","allowed_peers":["`+peerID+`"]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mesh/peers":
			_, _ = io.WriteString(w, `{"peers":[{"peer_id":"`+peerID+`","node_name":"remote-z","models":[{"name":"m-x"}]}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	in := strings.NewReader("/status\n/quit\n")
	var out bytes.Buffer
	var errOut bytes.Buffer
	c := NewChatRunner(ChatOptions{BaseURL: srv.URL, Model: "m-x"})
	if err := c.RunREPL(context.Background(), in, &out, &errOut); err != nil {
		t.Fatalf("run repl: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "model: m-x") {
		t.Fatalf("expected model in status output, got: %q", got)
	}
	if !strings.Contains(got, "routing: manual") {
		t.Fatalf("expected routing in status output, got: %q", got)
	}
	if !strings.Contains(got, "selected_peer: remote-z (12D...8dg)") {
		t.Fatalf("expected selected peer in status output, got: %q", got)
	}
}

func ExampleChatRunner_SendPrompt() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()
	c := NewChatRunner(ChatOptions{BaseURL: srv.URL, Model: "llama3"})
	var out bytes.Buffer
	_ = c.SendPrompt(context.Background(), "test", &out)
	fmt.Print(strings.TrimSpace(out.String()))
	// Output: hi
}
