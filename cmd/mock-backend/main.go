package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"infermeshai/internal/model"
)

type chatResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

func main() {
	addr := flag.String("addr", ":9000", "listen address")
	latency := flag.Duration("token-latency", 15*time.Millisecond, "latency between streamed chunks")
	failAfter := flag.Int("fail-after", -1, "if >=0, disconnect mid-stream after N tokens")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req model.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		prompt := ""
		if len(req.Messages) > 0 {
			prompt = req.Messages[len(req.Messages)-1].Content
		}
		generated := "mock:" + strings.TrimSpace(prompt)
		if generated == "mock:" {
			generated = "mock: hello"
		}

		if !req.Stream {
			resp := chatResponse{
				ID:      "chatcmpl-mock",
				Object:  "chat.completion",
				Created: time.Now().Unix(),
				Model:   req.Model,
			}
			resp.Choices = append(resp.Choices, struct {
				Index   int `json:"index"`
				Message struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			}{Index: 0, FinishReason: "stop"})
			resp.Choices[0].Message.Role = "assistant"
			resp.Choices[0].Message.Content = generated
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		tokens := strings.Fields(generated)
		for i, tok := range tokens {
			if *failAfter >= 0 && i >= *failAfter {
				return
			}
			chunk := fmt.Sprintf("data: {\"id\":\"chatcmpl-mock\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"%s \"},\"finish_reason\":null}]}\n\n", escape(tok))
			_, _ = w.Write([]byte(chunk))
			fl.Flush()
			time.Sleep(*latency)
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		fl.Flush()
	})

	log.Printf("mock backend listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func escape(s string) string {
	r := strings.NewReplacer(`\\`, `\\\\`, `"`, `\\"`)
	return r.Replace(s)
}
