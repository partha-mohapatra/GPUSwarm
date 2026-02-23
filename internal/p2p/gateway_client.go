package p2p

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

type GatewayResponse struct {
	StatusCode  int
	ContentType string
	Body        io.ReadCloser
}

type GatewayClient interface {
	Infer(ctx context.Context, gatewayURL string, req model.ChatCompletionRequest, peerCtx model.PeerContext) (*GatewayResponse, error)
}

type HTTPGatewayClient struct {
	client *http.Client
}

func NewHTTPGatewayClient(timeout time.Duration) *HTTPGatewayClient {
	return &HTTPGatewayClient{
		client: &http.Client{Timeout: timeout},
	}
}

func (c *HTTPGatewayClient) Infer(ctx context.Context, gatewayURL string, req model.ChatCompletionRequest, peerCtx model.PeerContext) (*GatewayResponse, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	url := strings.TrimRight(gatewayURL, "/") + "/v1/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if peerCtx.ClientPeerID != "" {
		httpReq.Header.Set(model.HeaderClientPeerID, peerCtx.ClientPeerID)
	}
	if peerCtx.ClientName != "" {
		httpReq.Header.Set(model.HeaderClientName, peerCtx.ClientName)
	}
	httpReq.Header.Set(model.HeaderPoWNonce, fmt.Sprintf("%d", peerCtx.PoWNonce))
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gateway request failed: %w", err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, fmt.Errorf("gateway status %d: %s", resp.StatusCode, string(body))
	}
	return &GatewayResponse{
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Body:        resp.Body,
	}, nil
}
