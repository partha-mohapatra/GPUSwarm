package backend

import (
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

type InferenceStream struct {
	StatusCode  int
	ContentType string
	Body        io.ReadCloser
}

type Adapter interface {
	Infer(ctx context.Context, req model.ChatCompletionRequest) (*InferenceStream, error)
}

type HTTPAdapter struct {
	BaseURL string
	Client  *http.Client
}

func NewHTTPAdapter(baseURL string, timeout time.Duration) *HTTPAdapter {
	return &HTTPAdapter{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (a *HTTPAdapter) Infer(ctx context.Context, req model.ChatCompletionRequest) (*InferenceStream, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.BaseURL+"/v1/chat/completions", bytes.NewReader(b))
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
	return &InferenceStream{
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Body:        resp.Body,
	}, nil
}
