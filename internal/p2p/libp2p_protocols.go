package p2p

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	"infermeshai/internal/model"
)

type capResponse struct {
	OK    bool                      `json:"ok"`
	Error string                    `json:"error,omitempty"`
	Cap   *model.CapabilityDocument `json:"cap,omitempty"`
}

type inferResponseHeader struct {
	StatusCode  int    `json:"status_code"`
	ContentType string `json:"content_type"`
	Error       string `json:"error,omitempty"`
}

type registryResponse struct {
	OK    bool                       `json:"ok"`
	Error string                     `json:"error,omitempty"`
	Peers []model.CapabilityDocument `json:"peers,omitempty"`
}

type inferProxyRequest struct {
	TargetPeerID string                      `json:"target_peer_id"`
	Request      model.ChatCompletionRequest `json:"request"`
}

func RegisterCapabilityHandler(node *Libp2pNode, capProvider interface {
	Snapshot() model.CapabilityDocument
}) {
	node.host.SetStreamHandler(protocol.ID(capabilityProtocolID), func(s network.Stream) {
		defer s.Close()
		resp := capResponse{OK: true}
		cap := capProvider.Snapshot()
		resp.Cap = &cap
		_ = json.NewEncoder(s).Encode(resp)
	})
}

type InferHandler interface {
	Infer(ctx context.Context, req model.ChatCompletionRequest, peerCtx model.PeerContext) (statusCode int, contentType string, body io.ReadCloser, cleanup func(), err error)
}

func RegisterInferHandler(node *Libp2pNode, provider InferHandler) {
	node.host.SetStreamHandler(protocol.ID(inferenceProtocolID), func(s network.Stream) {
		defer s.Close()
		br := bufio.NewReader(s)
		line, err := br.ReadBytes('\n')
		if err != nil {
			return
		}
		var req model.ChatCompletionRequest
		if err := json.Unmarshal(bytesTrim(line), &req); err != nil {
			_ = writeInferHeader(s, inferResponseHeader{StatusCode: http.StatusBadRequest, Error: "invalid json"})
			return
		}
		peerCtx := model.PeerContext{ClientPeerID: req.Metadata[model.HeaderClientPeerID], ClientName: req.Metadata[model.HeaderClientName]}
		if req.Metadata != nil {
			if rawNonce := req.Metadata[model.HeaderPoWNonce]; rawNonce != "" {
				if nonce, err := strconv.ParseUint(rawNonce, 10, 64); err == nil {
					peerCtx.PoWNonce = nonce
				}
			}
		}
		statusCode, contentType, body, done, err := provider.Infer(context.Background(), req, peerCtx)
		if err != nil {
			_ = writeInferHeader(s, inferResponseHeader{StatusCode: http.StatusBadGateway, Error: err.Error()})
			return
		}
		defer done()
		defer body.Close()
		if statusCode == 0 {
			statusCode = http.StatusOK
		}
		if err := writeInferHeader(s, inferResponseHeader{StatusCode: statusCode, ContentType: contentType}); err != nil {
			return
		}
		_, _ = io.Copy(s, body)
	})
}

func RegisterInferProxyHandler(node *Libp2pNode, logger *slog.Logger) {
	node.host.SetStreamHandler(protocol.ID(inferProxyProtocolID), func(s network.Stream) {
		defer s.Close()
		br := bufio.NewReader(s)
		line, err := br.ReadBytes('\n')
		if err != nil {
			_ = writeInferHeader(s, inferResponseHeader{StatusCode: http.StatusBadRequest, Error: "invalid proxy request"})
			return
		}
		var preq inferProxyRequest
		if err := json.Unmarshal(bytesTrim(line), &preq); err != nil {
			_ = writeInferHeader(s, inferResponseHeader{StatusCode: http.StatusBadRequest, Error: "invalid proxy json"})
			return
		}
		targetPeer := strings.TrimSpace(preq.TargetPeerID)
		if targetPeer == "" {
			_ = writeInferHeader(s, inferResponseHeader{StatusCode: http.StatusBadRequest, Error: "missing target peer id"})
			return
		}
		targetID, err := peer.Decode(targetPeer)
		if err != nil {
			_ = writeInferHeader(s, inferResponseHeader{StatusCode: http.StatusBadRequest, Error: "invalid target peer id"})
			return
		}

		dialCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		upstream, err := node.host.NewStream(dialCtx, targetID, protocol.ID(inferenceProtocolID))
		cancel()
		if err != nil {
			_ = writeInferHeader(s, inferResponseHeader{StatusCode: http.StatusBadGateway, Error: "proxy dial failed: " + err.Error()})
			if logger != nil {
				logger.Warn("proxy infer dial failed", slog.String("target_peer_id", targetPeer), slog.String("error", err.Error()))
			}
			return
		}
		defer upstream.Close()
		if logger != nil {
			logger.Info("proxy infer forwarding", slog.String("target_peer_id", targetPeer), slog.String("via_peer_id", node.PeerID()))
		}

		reqBytes, err := json.Marshal(preq.Request)
		if err != nil {
			_ = writeInferHeader(s, inferResponseHeader{StatusCode: http.StatusBadRequest, Error: "proxy marshal failed"})
			return
		}
		if _, err := upstream.Write(append(reqBytes, '\n')); err != nil {
			_ = writeInferHeader(s, inferResponseHeader{StatusCode: http.StatusBadGateway, Error: "proxy write failed: " + err.Error()})
			return
		}

		upBR := bufio.NewReader(upstream)
		hdrLine, err := upBR.ReadBytes('\n')
		if err != nil {
			_ = writeInferHeader(s, inferResponseHeader{StatusCode: http.StatusBadGateway, Error: "proxy read header failed: " + err.Error()})
			return
		}
		var hdr inferResponseHeader
		if err := json.Unmarshal(bytesTrim(hdrLine), &hdr); err != nil {
			_ = writeInferHeader(s, inferResponseHeader{StatusCode: http.StatusBadGateway, Error: "proxy invalid upstream header"})
			return
		}
		if err := writeInferHeader(s, hdr); err != nil {
			return
		}
		if hdr.StatusCode >= 400 {
			return
		}
		_, _ = io.Copy(s, upBR)
	})
}

func RegisterRegistryHandler(node *Libp2pNode) {
	node.host.SetStreamHandler(protocol.ID(registryProtocolID), func(s network.Stream) {
		defer s.Close()
		resp := registryResponse{OK: true, Peers: node.RegistryCapabilities()}
		_ = json.NewEncoder(s).Encode(resp)
	})
	node.host.SetStreamHandler(protocol.ID(registryAnnounceID), func(s network.Stream) {
		defer s.Close()
		var cap model.CapabilityDocument
		if err := json.NewDecoder(s).Decode(&cap); err != nil {
			_ = json.NewEncoder(s).Encode(capResponse{OK: false, Error: "invalid registry announce payload"})
			return
		}
		if strings.TrimSpace(cap.PeerID) == "" {
			_ = json.NewEncoder(s).Encode(capResponse{OK: false, Error: "missing peer id"})
			return
		}
		node.UpsertRegistryCapability(cap)
		if node.logger != nil {
			node.logger.Info(
				"registry announce",
				slog.String("peer_id", cap.PeerID),
				slog.String("node_name", cap.NodeName),
				slog.Int("models", len(cap.Models)),
			)
		}
		_ = json.NewEncoder(s).Encode(capResponse{OK: true})
	})
}

func QueryCapability(ctx context.Context, node *Libp2pNode, pid peer.ID, timeout time.Duration) (model.CapabilityDocument, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	s, err := node.host.NewStream(ctx, pid, protocol.ID(capabilityProtocolID))
	if err != nil {
		return model.CapabilityDocument{}, err
	}
	defer s.Close()
	var resp capResponse
	if err := json.NewDecoder(s).Decode(&resp); err != nil {
		return model.CapabilityDocument{}, err
	}
	if !resp.OK || resp.Cap == nil {
		if resp.Error == "" {
			resp.Error = "capability query failed"
		}
		return model.CapabilityDocument{}, fmt.Errorf(resp.Error)
	}
	return *resp.Cap, nil
}

func QueryRegistry(ctx context.Context, node *Libp2pNode, pid peer.ID, timeout time.Duration) ([]model.CapabilityDocument, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	s, err := node.host.NewStream(ctx, pid, protocol.ID(registryProtocolID))
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var resp registryResponse
	if err := json.NewDecoder(s).Decode(&resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		if resp.Error == "" {
			resp.Error = "registry query failed"
		}
		return nil, fmt.Errorf(resp.Error)
	}
	return resp.Peers, nil
}

func AnnounceRegistry(ctx context.Context, node *Libp2pNode, pid peer.ID, cap model.CapabilityDocument, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	s, err := node.host.NewStream(ctx, pid, protocol.ID(registryAnnounceID))
	if err != nil {
		return err
	}
	defer s.Close()
	if err := json.NewEncoder(s).Encode(cap); err != nil {
		return err
	}
	var resp capResponse
	if err := json.NewDecoder(s).Decode(&resp); err != nil {
		return err
	}
	if !resp.OK {
		if resp.Error == "" {
			resp.Error = "registry announce failed"
		}
		return fmt.Errorf(resp.Error)
	}
	return nil
}

type Libp2pGatewayClient struct {
	node *Libp2pNode
	mu   sync.Mutex
	// route cache: peerID -> "direct" or "proxy:<bootstrapPeerID>"
	routes map[string]string
}

func NewLibp2pGatewayClient(node *Libp2pNode) *Libp2pGatewayClient {
	return &Libp2pGatewayClient{node: node, routes: make(map[string]string)}
}

func (c *Libp2pGatewayClient) Infer(ctx context.Context, gatewayURL string, req model.ChatCompletionRequest, peerCtx model.PeerContext) (*GatewayResponse, error) {
	pid, err := parsePeerIDFromGateway(gatewayURL)
	if err != nil {
		return nil, err
	}
	if req.Metadata == nil {
		req.Metadata = make(map[string]string)
	}
	if peerCtx.ClientPeerID != "" {
		req.Metadata[model.HeaderClientPeerID] = peerCtx.ClientPeerID
	}
	if peerCtx.ClientName != "" {
		req.Metadata[model.HeaderClientName] = peerCtx.ClientName
	}
	req.Metadata[model.HeaderPoWNonce] = fmt.Sprintf("%d", peerCtx.PoWNonce)

	if route, ok := c.getRoute(pid.String()); ok {
		if route == "direct" {
			if res, derr := c.tryDirect(ctx, pid, req); derr == nil {
				return res, nil
			}
		} else if strings.HasPrefix(route, "proxy:") {
			if bpID, decErr := peer.Decode(strings.TrimPrefix(route, "proxy:")); decErr == nil {
				if res, perr := c.tryProxy(ctx, bpID, pid, req); perr == nil {
					return res, nil
				}
			}
		}
	}

	if res, derr := c.tryDirect(ctx, pid, req); derr == nil {
		c.setRoute(pid.String(), "direct")
		return res, nil
	}
	var lastErr error
	bootstrapPeers := c.node.BootstrapPeers()
	for _, bp := range bootstrapPeers {
		if bp.ID == "" || bp.ID == c.node.host.ID() || bp.ID == pid {
			continue
		}
		res, pErr := c.tryProxy(ctx, bp.ID, pid, req)
		if pErr == nil {
			c.setRoute(pid.String(), "proxy:"+bp.ID.String())
			return res, nil
		}
		lastErr = pErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no route to peer %s", pid.String())
}

func (c *Libp2pGatewayClient) tryDirect(ctx context.Context, pid peer.ID, req model.ChatCompletionRequest) (*GatewayResponse, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	s, err := c.node.host.NewStream(dialCtx, pid, protocol.ID(inferenceProtocolID))
	if err != nil {
		return nil, err
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	return inferOverStream(s, reqBytes)
}

func (c *Libp2pGatewayClient) tryProxy(ctx context.Context, bootstrapID peer.ID, targetID peer.ID, req model.ChatCompletionRequest) (*GatewayResponse, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	proxyStream, err := c.node.host.NewStream(dialCtx, bootstrapID, protocol.ID(inferProxyProtocolID))
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(inferProxyRequest{TargetPeerID: targetID.String(), Request: req})
	if err != nil {
		_ = proxyStream.Close()
		return nil, err
	}
	return inferOverStream(proxyStream, payload)
}

func (c *Libp2pGatewayClient) getRoute(peerID string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.routes[peerID]
	return v, ok
}

func (c *Libp2pGatewayClient) setRoute(peerID, route string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.routes[peerID] = route
}

func inferOverStream(s network.Stream, payload []byte) (*GatewayResponse, error) {
	if _, err := s.Write(append(payload, '\n')); err != nil {
		_ = s.Close()
		return nil, err
	}
	br := bufio.NewReader(s)
	hdrLine, err := br.ReadBytes('\n')
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	var hdr inferResponseHeader
	if err := json.Unmarshal(bytesTrim(hdrLine), &hdr); err != nil {
		_ = s.Close()
		return nil, err
	}
	if hdr.StatusCode >= 400 {
		_ = s.Close()
		if hdr.Error == "" {
			hdr.Error = "remote infer failed"
		}
		return nil, fmt.Errorf(hdr.Error)
	}
	return &GatewayResponse{StatusCode: hdr.StatusCode, ContentType: hdr.ContentType, Body: &streamReadCloser{Reader: br, stream: s}}, nil
}

type streamReadCloser struct {
	*bufio.Reader
	stream network.Stream
}

func (s *streamReadCloser) Close() error { return s.stream.Close() }

func writeInferHeader(w io.Writer, hdr inferResponseHeader) error {
	b, err := json.Marshal(hdr)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func parsePeerIDFromGateway(gatewayURL string) (peer.ID, error) {
	parts := strings.Split(strings.TrimSpace(gatewayURL), "/")
	if len(parts) == 0 {
		return "", fmt.Errorf("invalid gateway url")
	}
	pid := parts[len(parts)-1]
	if pid == "" {
		return "", fmt.Errorf("missing peer id")
	}
	return peer.Decode(pid)
}

func bytesTrim(b []byte) []byte { return []byte(strings.TrimSpace(string(b))) }
