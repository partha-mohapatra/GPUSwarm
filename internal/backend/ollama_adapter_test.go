package backend

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"infermeshai/internal/model"
)

func TestOllamaAdapterStreamingToSSE(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"message":{"content":"hello "},"done":false}` + "\n"))
		_, _ = w.Write([]byte(`{"message":{"content":"world"},"done":false}` + "\n"))
		_, _ = w.Write([]byte(`{"done":true}` + "\n"))
	})
	s := httptest.NewServer(mux)
	defer s.Close()

	a := NewOllamaAdapter(s.URL, 10*time.Second)
	res, err := a.Infer(context.Background(), model.ChatCompletionRequest{Model: "llama3", Stream: true})
	if err != nil {
		t.Fatalf("infer failed: %v", err)
	}
	defer res.Body.Close()
	if res.ContentType != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %s", res.ContentType)
	}
	b, _ := io.ReadAll(res.Body)
	body := string(b)
	if !strings.Contains(body, "hello") || !strings.Contains(body, "[DONE]") {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestOllamaAdapterRequestShape(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		var got map[string]any
		_ = json.NewDecoder(r.Body).Decode(&got)
		if got["model"] != "m1" {
			t.Fatalf("expected model m1")
		}
		_, _ = w.Write([]byte(`{"done":true}` + "\n"))
	})
	s := httptest.NewServer(mux)
	defer s.Close()
	a := NewOllamaAdapter(s.URL, 10*time.Second)
	res, err := a.Infer(context.Background(), model.ChatCompletionRequest{Model: "m1", Stream: true})
	if err != nil {
		t.Fatalf("infer failed: %v", err)
	}
	defer res.Body.Close()
}
