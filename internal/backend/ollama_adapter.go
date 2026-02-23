package backend

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"infermeshai/internal/model"
)

type OllamaAdapter struct {
	BaseURL string
	Client  *http.Client
}

func NewOllamaAdapter(baseURL string, timeout time.Duration) *OllamaAdapter {
	return &OllamaAdapter{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Client:  &http.Client{Timeout: timeout},
	}
}

type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []model.Message `json:"messages"`
	Stream   bool            `json:"stream"`
}

type ollamaLine struct {
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done bool `json:"done"`
}

func (a *OllamaAdapter) Infer(ctx context.Context, req model.ChatCompletionRequest) (*InferenceStream, error) {
	or := ollamaRequest{Model: req.Model, Messages: req.Messages, Stream: req.Stream}
	b, err := json.Marshal(or)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.BaseURL+"/api/chat", bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("build backend request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := a.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("backend request failed: %w", err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, fmt.Errorf("backend status %d: %s", resp.StatusCode, string(body))
	}
	if !req.Stream {
		return &InferenceStream{StatusCode: http.StatusOK, ContentType: "application/json", Body: resp.Body}, nil
	}

	pr, pw := io.Pipe()
	go func() {
		defer resp.Body.Close()
		defer pw.Close()
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			var line ollamaLine
			if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
				continue
			}
			if line.Message.Content != "" {
				chunk := fmt.Sprintf("data: {\"id\":\"chatcmpl-ollama\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%q},\"finish_reason\":null}]}\n\n", line.Message.Content)
				_, _ = pw.Write([]byte(chunk))
			}
			if line.Done {
				_, _ = pw.Write([]byte("data: [DONE]\\n\\n"))
				return
			}
		}
	}()
	return &InferenceStream{StatusCode: http.StatusOK, ContentType: "text/event-stream", Body: pr}, nil
}
