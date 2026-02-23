package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"infermeshai/internal/app"
	"infermeshai/internal/control"
	"infermeshai/internal/model"
)

type ClientInferer interface {
	Infer(ctx context.Context, req model.ChatCompletionRequest) (*app.InferenceResult, error)
}

type ClientPeerLister interface {
	ListPeers(ctx context.Context) ([]model.CapabilityDocument, error)
}

type ClientRoutingController interface {
	GetRoutingSettings() app.RoutingSettings
	UpdateRoutingSettings(in app.RoutingSettings)
	SelectPeerManual(peerID string)
}

type ProviderInferer interface {
	Infer(ctx context.Context, req model.ChatCompletionRequest, peerCtx model.PeerContext) (*app.InferenceResult, func(), error)
}

type CapabilityProvider interface {
	Snapshot() model.CapabilityDocument
}

type Server struct {
	mux    *http.ServeMux
	logger *slog.Logger
}

func NewServer(logger *slog.Logger) *Server {
	return &Server{mux: http.NewServeMux(), logger: logger}
}

func (s *Server) HandleClientInference(inferer ClientInferer) {
	s.mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req model.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if req.Model == "" {
			http.Error(w, "model required", http.StatusBadRequest)
			return
		}

		res, err := inferer.Infer(r.Context(), req)
		if err != nil {
			s.logger.Warn("client inference failed",
				slog.String("model", req.Model),
				slog.String("error", err.Error()),
				slog.String("remote_addr", r.RemoteAddr),
			)
			http.Error(w, fmt.Sprintf("inference failed: %v", err), http.StatusBadGateway)
			return
		}
		defer res.Body.Close()
		relayResponse(w, res)
	})
}

func (s *Server) HandleProviderInference(inferer ProviderInferer) {
	s.mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req model.ChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if req.Model == "" {
			http.Error(w, "model required", http.StatusBadRequest)
			return
		}
		peerCtx := model.PeerContext{ClientPeerID: r.Header.Get(model.HeaderClientPeerID)}
		peerCtx.ClientName = r.Header.Get(model.HeaderClientName)
		if rawNonce := r.Header.Get(model.HeaderPoWNonce); rawNonce != "" {
			if nonce, err := strconv.ParseUint(rawNonce, 10, 64); err == nil {
				peerCtx.PoWNonce = nonce
			}
		}
		res, done, err := inferer.Infer(r.Context(), req, peerCtx)
		if err != nil {
			status := http.StatusTooManyRequests
			if errors.Is(err, control.ErrTokensExceeded) {
				status = http.StatusBadRequest
			}
			if errors.Is(err, control.ErrOutsideServingWindow) || errors.Is(err, control.ErrHostBusy) {
				status = http.StatusServiceUnavailable
			}
			if errors.Is(err, control.ErrInvalidPoW) {
				status = http.StatusForbidden
			}
			if errors.Is(err, control.ErrPeerReputationLow) {
				status = http.StatusTooManyRequests
			}
			if !errors.Is(err, control.ErrRateLimited) && !errors.Is(err, control.ErrTooManyInFlight) && !errors.Is(err, control.ErrTokensExceeded) && !errors.Is(err, control.ErrOutsideServingWindow) && !errors.Is(err, control.ErrHostBusy) && !errors.Is(err, control.ErrInvalidPoW) && !errors.Is(err, control.ErrPeerReputationLow) {
				status = http.StatusBadGateway
			}
			http.Error(w, err.Error(), status)
			return
		}
		defer done()
		defer res.Body.Close()
		relayResponse(w, res)
	})
}

func (s *Server) HandleClientPeerList(lister ClientPeerLister) {
	s.mux.HandleFunc("/v1/mesh/peers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		peers, err := lister.ListPeers(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"peers": peers})
	})
}

func (s *Server) HandleClientRoutingControl(ctrl ClientRoutingController) {
	s.mux.HandleFunc("/v1/mesh/routing", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ctrl.GetRoutingSettings())
		case http.MethodPut:
			var in app.RoutingSettings
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				http.Error(w, "invalid json", http.StatusBadRequest)
				return
			}
			ctrl.UpdateRoutingSettings(in)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ctrl.GetRoutingSettings())
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	s.mux.HandleFunc("/v1/mesh/select/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		peerID := strings.TrimPrefix(r.URL.Path, "/v1/mesh/select/")
		peerID = strings.TrimSpace(peerID)
		if peerID == "" {
			http.Error(w, "peer id required", http.StatusBadRequest)
			return
		}
		ctrl.SelectPeerManual(peerID)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ctrl.GetRoutingSettings())
	})
}

func (s *Server) HandleCapabilities(cap CapabilityProvider) {
	s.mux.HandleFunc("/capabilities", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cap.Snapshot())
	})
}

func (s *Server) Start(addr string) error {
	h := withTimeout(s.mux, 0)
	server := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.logger.Info("http server listening", slog.String("addr", addr))
	return server.ListenAndServe()
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func withTimeout(next http.Handler, timeout time.Duration) http.Handler {
	if timeout <= 0 {
		return next
	}
	return http.TimeoutHandler(next, timeout, "timeout")
}

func relayResponse(w http.ResponseWriter, res *app.InferenceResult) {
	if res.ContentType != "" {
		w.Header().Set("Content-Type", res.ContentType)
	}
	if res.StatusCode == 0 {
		res.StatusCode = http.StatusOK
	}
	w.WriteHeader(res.StatusCode)

	if fl, ok := w.(http.Flusher); ok {
		buf := make([]byte, 8*1024)
		for {
			n, err := res.Body.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
				fl.Flush()
			}
			if err != nil {
				break
			}
		}
		return
	}
	_, _ = io.Copy(w, res.Body)
}
