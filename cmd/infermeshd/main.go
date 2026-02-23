package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"infermeshai/internal/api"
	"infermeshai/internal/app"
	"infermeshai/internal/backend"
	cfgpkg "infermeshai/internal/config"
	"infermeshai/internal/control"
	"infermeshai/internal/model"
	"infermeshai/internal/p2p"
	"infermeshai/internal/routing"
)

type p2pProviderAdapter struct {
	svc *app.ProviderService
}

func (a p2pProviderAdapter) Infer(ctx context.Context, req model.ChatCompletionRequest, peerCtx model.PeerContext) (int, string, io.ReadCloser, func(), error) {
	res, cleanup, err := a.svc.Infer(ctx, req, peerCtx)
	if err != nil {
		return 0, "", nil, nil, err
	}
	return res.StatusCode, res.ContentType, res.Body, cleanup, nil
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "chat":
			if err := runChatCommand(args[1:], logger); err != nil {
				logger.Error("chat command failed", slog.String("error", err.Error()))
				os.Exit(1)
			}
			return
		case "daemon":
			// Keep daemon behavior identical when explicitly selected.
			os.Args = append([]string{os.Args[0]}, args[1:]...)
		}
	}

	cfgPath := findArgValue(os.Args[1:], "-config")
	if cfgPath == "" {
		cfgPath = cfgpkg.DefaultPath()
	}
	simpleInteractiveStart := shouldRunSetupWizard(os.Args[1:])
	cfg := cfgpkg.Default()
	cfgExists := cfgpkg.Exists(cfgPath)
	if cfgExists {
		loaded, err := cfgpkg.Load(cfgPath)
		if err != nil {
			logger.Error("failed loading config", slog.String("path", cfgPath), slog.String("error", err.Error()))
			os.Exit(1)
		}
		cfg = loaded
		if simpleInteractiveStart {
			reader := bufio.NewReader(os.Stdin)
			modeHint := strings.TrimSpace(cfg.Mode)
			if modeHint == "" {
				modeHint = "both"
			}
			prompt := fmt.Sprintf("Use saved config (mode=%s) from %s?", modeHint, cfgPath)
			if !promptYesNo(reader, os.Stdout, prompt, true) {
				cfg = runSetupWizard(cfg, reader, os.Stdout, logger)
			}
		}
	} else if simpleInteractiveStart {
		cfg = runSetupWizard(cfg, os.Stdin, os.Stdout, logger)
	}

	var (
		mode          = flag.String("mode", cfg.Mode, "daemon mode: client|provider|both")
		transport     = flag.String("transport", cfg.Transport, "mesh transport mode: http|libp2p")
		clientName    = flag.String("client-name", cfg.ClientName, "human-readable client node name")
		providerName  = flag.String("provider-name", cfg.ProviderName, "human-readable provider node name")
		identityPath  = flag.String("identity-file", cfg.IdentityFile, "path to persistent node identity file")
		clientAddr    = flag.String("client-addr", cfg.ClientAddr, "client api listen addr")
		providerAddr  = flag.String("provider-addr", cfg.ProviderAddr, "provider gateway listen addr")
		providerURL   = flag.String("provider-peer-urls", joinCSV(cfg.ProviderPeerURLs), "comma-separated provider gateway urls for client mode")
		backendKind   = flag.String("backend-kind", cfg.BackendKind, "provider backend adapter: openai|ollama")
		backendURL    = flag.String("backend-url", cfg.BackendURL, "provider local backend url")
		peerID        = flag.String("peer-id", cfg.PeerID, "override peer id (defaults to derived identity)")
		gpuName       = flag.String("gpu", cfg.GPU, "provider gpu name")
		vramFreeMB    = flag.Int("vram-free-mb", cfg.VRAMFreeMB, "provider free vram in mb")
		modelsCSV     = flag.String("models", joinCSV(cfg.Models), "comma-separated model names")
		modelSpecsCSV = flag.String("model-specs", joinCSV(cfg.ModelSpecs), "comma-separated model specs: name[:sha256[:tokenizer[:quant[:backend[:maxctx]]]]]")
		advertiseURL  = flag.String("advertise-url", cfg.AdvertiseURL, "provider advertised gateway url; defaults to inferred local url")
		maxInflight   = flag.Int("max-inflight", cfg.MaxInflight, "provider max concurrent inference requests")
		ratePerSecond = flag.Float64("rate-per-second", cfg.RatePerSecond, "provider request rate limit")
		burst         = flag.Int("burst", cfg.Burst, "provider burst tokens")
		maxTokens     = flag.Int("max-tokens-per-request", cfg.MaxTokensPerRequest, "provider max tokens per request; 0 disables")
		nightOnly     = flag.Bool("night-only", cfg.NightOnly, "serve requests only during configured night window")
		nightStart    = flag.Int("night-start-hour", cfg.NightStartHour, "night window start hour (0-23)")
		nightEnd      = flag.Int("night-end-hour", cfg.NightEndHour, "night window end hour (0-23)")
		idleOnly      = flag.Bool("idle-only", cfg.IdleOnly, "serve only when host load is below max-load1m")
		maxLoad1m     = flag.Float64("max-load1m", cfg.MaxLoad1m, "max 1m load average when idle-only is enabled")
		powDifficulty = flag.Int("pow-difficulty", cfg.PoWDifficulty, "optional PoW difficulty in leading zero hex chars")
		minPeerRep    = flag.Float64("min-peer-reputation", cfg.MinPeerReputation, "minimum accepted peer reputation [0,1]")
		initCredits   = flag.Int64("initial-credits", cfg.InitialCredits, "initial local credits")
		routingPolicy = flag.String("routing-policy", cfg.RoutingPolicy, "routing policy: auto|prefer_known|prefer_favorites|manual")
		favoritePeers = flag.String("favorite-peers", joinCSV(cfg.FavoritePeers), "comma-separated favorite peer IDs")
		blockedPeers  = flag.String("blocked-peers", joinCSV(cfg.BlockedPeers), "comma-separated blocked peer IDs")
		allowedPeers  = flag.String("allowed-peers", joinCSV(cfg.AllowedPeers), "comma-separated allowlisted peer IDs (manual policy)")
		stickyPeerTTL = flag.Duration("sticky-peer-ttl", cfg.StickyPeerTTL, "optional sticky peer TTL per model")
		wLatency      = flag.Float64("weight-latency", cfg.WeightLatency, "routing weight: latency")
		wQueue        = flag.Float64("weight-queue-depth", cfg.WeightQueueDepth, "routing weight: queue depth")
		wVRAM         = flag.Float64("weight-vram-pressure", cfg.WeightVRAMPressure, "routing weight: VRAM pressure")
		wReliability  = flag.Float64("weight-reliability", cfg.WeightReliability, "routing weight: reliability penalty")
		wFavorite     = flag.Float64("weight-favorite-bonus", cfg.WeightFavoriteBonus, "routing score bonus for favorites")
		wKnown        = flag.Float64("weight-known-bonus", cfg.WeightKnownBonus, "routing score bonus for known good peers")
		lanDiscovery  = flag.Bool("lan-discovery", cfg.LANDiscovery, "enable LAN UDP peer discovery")
		lanListenAddr = flag.String("lan-listen-addr", cfg.LANListenAddr, "LAN discovery UDP listen addr")
		lanBroadcast  = flag.String("lan-broadcast-addr", cfg.LANBroadcastAddr, "LAN discovery UDP broadcast addr")
		lanAnnounce   = flag.Duration("lan-announce-interval", cfg.LANAnnounceInterval, "LAN capability announce interval")
		lanTTL        = flag.Duration("lan-peer-ttl", cfg.LANPeerTTL, "LAN-discovered peer TTL")
		libp2pListen  = flag.String("libp2p-listen-addrs", joinCSV(cfg.Libp2pListenAddrs), "comma-separated libp2p listen multiaddrs")
		libp2pBoot    = flag.String("libp2p-bootstrap-peers", joinCSV(cfg.Libp2pBootstrap), "comma-separated libp2p bootstrap peer multiaddrs")
		libp2pRendez  = flag.String("libp2p-rendezvous", cfg.Libp2pRendezvous, "libp2p rendezvous namespace")
		libp2pAutoR   = flag.Bool("libp2p-auto-relay", cfg.Libp2pAutoRelay, "enable auto-relay client mode via bootstrap relays")
		libp2pRelayS  = flag.Bool("libp2p-relay-service", cfg.Libp2pRelayService, "enable relay service mode for this node")
		libp2pNatPM   = flag.Bool("libp2p-nat-port-map", cfg.Libp2pNATPortMap, "enable NAT-PMP/UPnP port mapping")
		libp2pHoleP   = flag.Bool("libp2p-hole-punching", cfg.Libp2pHolePunching, "enable libp2p hole punching")
		requestTO     = flag.Duration("request-timeout", cfg.RequestTimeout, "inference request timeout")
		logFile       = flag.String("log-file", cfg.LogFile, "path to JSON log file")
		configPath    = flag.String("config", cfgPath, "config file path (.yaml/.yml/.json)")
		saveConfig    = flag.Bool("save-config", false, "save resolved runtime config to -config and exit")
	)
	flag.Parse()

	cfg = cfgpkg.Config{
		Mode:                *mode,
		Transport:           *transport,
		ClientName:          *clientName,
		ProviderName:        *providerName,
		IdentityFile:        *identityPath,
		ClientAddr:          *clientAddr,
		ProviderAddr:        *providerAddr,
		ProviderPeerURLs:    splitCSV(*providerURL),
		BackendKind:         *backendKind,
		BackendURL:          *backendURL,
		PeerID:              *peerID,
		GPU:                 *gpuName,
		VRAMFreeMB:          *vramFreeMB,
		Models:              splitCSV(*modelsCSV),
		ModelSpecs:          splitCSV(*modelSpecsCSV),
		AdvertiseURL:        *advertiseURL,
		MaxInflight:         *maxInflight,
		RatePerSecond:       *ratePerSecond,
		Burst:               *burst,
		MaxTokensPerRequest: *maxTokens,
		NightOnly:           *nightOnly,
		NightStartHour:      *nightStart,
		NightEndHour:        *nightEnd,
		IdleOnly:            *idleOnly,
		MaxLoad1m:           *maxLoad1m,
		PoWDifficulty:       *powDifficulty,
		MinPeerReputation:   *minPeerRep,
		InitialCredits:      *initCredits,
		RoutingPolicy:       *routingPolicy,
		FavoritePeers:       splitCSV(*favoritePeers),
		BlockedPeers:        splitCSV(*blockedPeers),
		AllowedPeers:        splitCSV(*allowedPeers),
		StickyPeerTTL:       *stickyPeerTTL,
		WeightLatency:       *wLatency,
		WeightQueueDepth:    *wQueue,
		WeightVRAMPressure:  *wVRAM,
		WeightReliability:   *wReliability,
		WeightFavoriteBonus: *wFavorite,
		WeightKnownBonus:    *wKnown,
		LANDiscovery:        *lanDiscovery,
		LANListenAddr:       *lanListenAddr,
		LANBroadcastAddr:    *lanBroadcast,
		LANAnnounceInterval: *lanAnnounce,
		LANPeerTTL:          *lanTTL,
		Libp2pListenAddrs:   splitCSV(*libp2pListen),
		Libp2pBootstrap:     splitCSV(*libp2pBoot),
		Libp2pRendezvous:    *libp2pRendez,
		Libp2pAutoRelay:     *libp2pAutoR,
		Libp2pRelayService:  *libp2pRelayS,
		Libp2pNATPortMap:    *libp2pNatPM,
		Libp2pHolePunching:  *libp2pHoleP,
		RequestTimeout:      *requestTO,
		LogFile:             *logFile,
	}
	resolvedCfg, err := cfgpkg.ResolveAndValidate(cfg)
	if err != nil {
		logger.Error("invalid configuration", slog.String("error", err.Error()))
		os.Exit(1)
	}
	cfg = resolvedCfg
	if strings.TrimSpace(cfg.LogFile) != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.LogFile), 0o755); err == nil {
			if lf, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				mw := io.MultiWriter(os.Stdout, lf)
				logger = slog.New(slog.NewJSONHandler(mw, nil))
			}
		}
	}
	if *saveConfig || !cfgpkg.Exists(*configPath) {
		if err := cfgpkg.Save(*configPath, cfg); err != nil {
			logger.Error("failed saving config", slog.String("path", *configPath), slog.String("error", err.Error()))
			os.Exit(1)
		}
		if *saveConfig {
			logger.Info("config saved", slog.String("path", *configPath))
			return
		}
	}

	libID, err := p2p.LoadOrCreateLibp2pIdentity(cfg.IdentityFile)
	if err != nil {
		logger.Error("failed to initialize identity", slog.String("error", err.Error()))
		os.Exit(1)
	}
	pid, err := peer.IDFromPublicKey(libID.PubKey)
	if err != nil {
		logger.Error("failed deriving peer id", slog.String("error", err.Error()))
		os.Exit(1)
	}
	effectivePeerID := pid.String()
	if cfg.PeerID != "" {
		effectivePeerID = cfg.PeerID
	}

	weights := model.RoutingWeights{
		Latency:       cfg.WeightLatency,
		QueueDepth:    cfg.WeightQueueDepth,
		VRAMPressure:  cfg.WeightVRAMPressure,
		Reliability:   cfg.WeightReliability,
		FavoriteBonus: cfg.WeightFavoriteBonus,
		KnownBonus:    cfg.WeightKnownBonus,
	}
	router := routing.NewRouter(weights)
	creditLedger := control.NewCreditLedger(cfg.InitialCredits)
	reputation := control.NewReputationTracker()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 3)
	var libp2pNode *p2p.Libp2pNode
	transportMode := strings.ToLower(strings.TrimSpace(cfg.Transport))
	if transportMode == "libp2p" {
		node, err := p2p.NewLibp2pNode(ctx, p2p.Libp2pConfig{
			ListenAddrs:        cfg.Libp2pListenAddrs,
			BootstrapPeers:     cfg.Libp2pBootstrap,
			Rendezvous:         cfg.Libp2pRendezvous,
			EnableMDNS:         true,
			EnableNATPortMap:   cfg.Libp2pNATPortMap,
			EnableHolePunching: cfg.Libp2pHolePunching,
			EnableAutoRelay:    cfg.Libp2pAutoRelay,
			EnableRelayService: cfg.Libp2pRelayService,
		}, cfg.IdentityFile, logger)
		if err != nil {
			logger.Error("failed to initialize libp2p node", slog.String("error", err.Error()))
			os.Exit(1)
		}
		libp2pNode = node
		defer func() { _ = libp2pNode.Close() }()
		effectivePeerID = libp2pNode.PeerID()
		p2p.RegisterRegistryHandler(libp2pNode)
		p2p.RegisterInferProxyHandler(libp2pNode, logger)
	}
	logger.Info("runtime configuration",
		slog.String("mode", cfg.Mode),
		slog.String("transport", transportMode),
		slog.String("peer_id", effectivePeerID),
		slog.String("client_addr", cfg.ClientAddr),
		slog.String("provider_addr", cfg.ProviderAddr),
		slog.Int("bootstrap_peers", len(cfg.Libp2pBootstrap)),
		slog.String("rendezvous", cfg.Libp2pRendezvous),
		slog.Bool("libp2p_auto_relay", cfg.Libp2pAutoRelay),
		slog.Bool("libp2p_relay_service", cfg.Libp2pRelayService),
		slog.Bool("libp2p_nat_port_map", cfg.Libp2pNATPortMap),
		slog.Bool("libp2p_hole_punching", cfg.Libp2pHolePunching),
	)

	if cfg.Mode == "client" || cfg.Mode == "both" {
		discoveries := make([]p2p.Discovery, 0, 3)
		if len(cfg.ProviderPeerURLs) > 0 {
			discoveries = append(discoveries, p2p.NewHTTPDiscovery(cfg.ProviderPeerURLs, 3*time.Second))
		}
		if cfg.LANDiscovery {
			lan, err := p2p.NewLANDiscovery(cfg.LANListenAddr, cfg.LANPeerTTL, logger)
			if err != nil {
				logger.Error("failed to start LAN discovery", slog.String("error", err.Error()))
				os.Exit(1)
			}
			discoveries = append(discoveries, lan)
		}

		var discovery p2p.Discovery
		var gateway p2p.GatewayClient
		if transportMode == "libp2p" {
			discoveries = append(discoveries, p2p.NewLibp2pDiscovery(libp2pNode, cfg.Libp2pRendezvous, 3*time.Second, logger))
			discovery = p2p.NewMultiDiscovery(discoveries...)
			gateway = p2p.NewLibp2pGatewayClient(libp2pNode)
		} else {
			if len(discoveries) == 0 {
				logger.Error("no discovery source configured; set provider_peer_urls/lan_discovery/libp2p")
				os.Exit(1)
			}
			discovery = p2p.NewMultiDiscovery(discoveries...)
			gateway = p2p.NewHTTPGatewayClient(cfg.RequestTimeout)
		}
		clientSvc := app.NewClientService(discovery, router, gateway, app.ClientOptions{
			PeerID:        effectivePeerID,
			PeerName:      cfg.ClientName,
			RoutingPolicy: cfg.RoutingPolicy,
			FavoritePeers: cfg.FavoritePeers,
			BlockedPeers:  cfg.BlockedPeers,
			AllowedPeers:  cfg.AllowedPeers,
			StickyPeerTTL: cfg.StickyPeerTTL,
			PoWDifficulty: cfg.PoWDifficulty,
			CreditLedger:  creditLedger,
			Reputation:    reputation,
		}, logger)
		s := api.NewServer(logger)
		s.HandleClientInference(clientSvc)
		s.HandleClientPeerList(clientSvc)
		s.HandleClientRoutingControl(clientSvc)
		go func() { errCh <- s.Start(cfg.ClientAddr) }()
	}

	if cfg.Mode == "provider" || cfg.Mode == "both" {
		gatewayURL := buildAdvertiseURL(cfg.AdvertiseURL, cfg.ProviderAddr)
		if transportMode == "libp2p" {
			gatewayURL = "libp2p://" + effectivePeerID
		}
		cap := model.CapabilityDocument{
			PeerID:       effectivePeerID,
			NodeName:     cfg.ProviderName,
			Models:       parseModelSpecs(cfg.Models, cfg.ModelSpecs),
			GPU:          cfg.GPU,
			VRAMFreeMB:   cfg.VRAMFreeMB,
			QueueDepth:   0,
			AvgLatencyMS: 50,
			Reliability:  1.0,
			GatewayURL:   gatewayURL,
		}
		if transportMode == "libp2p" {
			cap.AnnounceAddrs = libp2pNode.AdvertiseAddrs()
		}
		capState := app.NewCapabilityState(cap)
		policy := control.ProviderPolicy{
			MaxTokensPerRequest: cfg.MaxTokensPerRequest,
			NightOnly:           cfg.NightOnly,
			NightStartHour:      cfg.NightStartHour,
			NightEndHour:        cfg.NightEndHour,
			IdleOnly:            cfg.IdleOnly,
			MaxLoad1m:           cfg.MaxLoad1m,
			PoWDifficulty:       cfg.PoWDifficulty,
			MinPeerReputation:   cfg.MinPeerReputation,
		}
		limiter := control.NewLimiter(cfg.MaxInflight, cfg.RatePerSecond, cfg.Burst)
		var adapter backend.Adapter
		switch strings.ToLower(strings.TrimSpace(cfg.BackendKind)) {
		case "ollama":
			adapter = backend.NewOllamaAdapter(cfg.BackendURL, cfg.RequestTimeout)
		default:
			adapter = backend.NewHTTPAdapter(cfg.BackendURL, cfg.RequestTimeout)
		}
		providerSvc := app.NewProviderService(adapter, limiter, capState, policy, control.ProcLoadProbe{}, creditLedger, reputation, logger)
		if transportMode == "libp2p" {
			p2p.RegisterCapabilityHandler(libp2pNode, capState)
			p2p.RegisterInferHandler(libp2pNode, p2pProviderAdapter{svc: providerSvc})
			// Expose local provider state for operator visibility even when inference transport is libp2p.
			admin := api.NewServer(logger)
			admin.HandleCapabilities(capState)
			go func() { errCh <- admin.Start(cfg.ProviderAddr) }()
			go func() {
				publish := func() {
					capDoc := capState.Snapshot()
					capDoc.PeerID = libp2pNode.PeerID()
					capDoc.GatewayURL = "libp2p://" + libp2pNode.PeerID()
					capDoc.AnnounceAddrs = libp2pNode.AdvertiseAddrs()
					capState.SetAnnounceAddrs(capDoc.AnnounceAddrs)
					_ = libp2pNode.PublishHeartbeat(ctx, capDoc)
					for _, bp := range libp2pNode.BootstrapPeers() {
						if bp.ID == "" || bp.ID == libp2pNode.Host().ID() {
							continue
						}
						if err := p2p.AnnounceRegistry(ctx, libp2pNode, bp.ID, capDoc, 3*time.Second); err != nil {
							logger.Warn("registry announce failed", slog.String("peer_id", bp.ID.String()), slog.String("error", err.Error()))
						}
					}
					logger.Info("provider heartbeat published", slog.String("peer_id", capDoc.PeerID), slog.Int("models", len(capDoc.Models)), slog.Int("announce_addrs", len(capDoc.AnnounceAddrs)))
				}

				// Publish once immediately so newly started clients can see this provider without waiting for the first tick.
				publish()

				tick := time.NewTicker(cfg.LANAnnounceInterval)
				defer tick.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-tick.C:
						publish()
					}
				}
			}()
		} else {
			s := api.NewServer(logger)
			s.HandleCapabilities(capState)
			s.HandleProviderInference(providerSvc)
			go func() { errCh <- s.Start(cfg.ProviderAddr) }()
		}

		if cfg.LANDiscovery && transportMode != "libp2p" {
			announcer, err := p2p.NewLANAnnouncer(cfg.LANBroadcastAddr, cfg.LANAnnounceInterval, capState.Snapshot, logger)
			if err != nil {
				logger.Error("failed to start LAN announcer", slog.String("error", err.Error()))
				os.Exit(1)
			}
			go func() {
				if err := announcer.Run(ctx); err != nil {
					logger.Warn("lan announcer stopped", slog.String("error", err.Error()))
				}
			}()
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil {
			logger.Error("server exited", slog.String("error", err.Error()))
			os.Exit(1)
		}
	case sig := <-sigCh:
		logger.Info("received signal", slog.String("signal", sig.String()))
		cancel()
	}
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func joinCSV(v []string) string {
	return strings.Join(v, ",")
}

func parseModelSpecs(baseNames, encoded []string) []model.ModelSpec {
	out := make([]model.ModelSpec, 0, len(baseNames)+len(encoded))
	for _, m := range baseNames {
		out = append(out, model.ModelSpec{Name: m, Backend: "openai-compatible"})
	}
	for _, raw := range encoded {
		parts := strings.Split(raw, ":")
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		spec := model.ModelSpec{Name: parts[0]}
		if len(parts) > 1 {
			spec.SHA256 = parts[1]
		}
		if len(parts) > 2 {
			spec.Tokenizer = parts[2]
		}
		if len(parts) > 3 {
			spec.Quant = parts[3]
		}
		if len(parts) > 4 {
			spec.Backend = parts[4]
		}
		if len(parts) > 5 {
			if v, err := strconv.Atoi(parts[5]); err == nil {
				spec.MaxCtx = v
			}
		}
		out = append(out, spec)
	}
	return out
}

func buildAdvertiseURL(advertiseURL, providerAddr string) string {
	if advertiseURL != "" {
		return strings.TrimRight(advertiseURL, "/")
	}
	host, port, err := net.SplitHostPort(providerAddr)
	if err != nil {
		return "http://127.0.0.1:7777"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	if port == "" {
		port = "7777"
	}
	return fmt.Sprintf("http://%s", net.JoinHostPort(host, port))
}

type backendDetection struct {
	Label  string
	Kind   string
	URL    string
	Models []string
}

func shouldRunSetupWizard(args []string) bool {
	if !isInteractiveStdin() {
		return false
	}
	filtered := args
	if len(filtered) > 0 && filtered[0] == "daemon" {
		filtered = filtered[1:]
	}
	if len(filtered) == 0 {
		return true
	}
	for i := 0; i < len(filtered); i++ {
		a := filtered[i]
		if a == "-config" {
			i++
			continue
		}
		if strings.HasPrefix(a, "-config=") {
			continue
		}
		if strings.HasPrefix(a, "-") {
			return false
		}
		return false
	}
	return true
}

func isInteractiveStdin() bool {
	st, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (st.Mode() & os.ModeCharDevice) != 0
}

func runSetupWizard(cfg cfgpkg.Config, in io.Reader, out io.Writer, logger *slog.Logger) cfgpkg.Config {
	reader := bufio.NewReader(in)
	_, _ = fmt.Fprintln(out, "GPUSwarm first-run setup")
	_, _ = fmt.Fprintln(out, "Press Enter to accept defaults.")
	cfg.Transport = "libp2p"

	modeChoice := promptChoice(reader, out, "Select mode", []string{"client", "provider", "both"}, cfg.Mode)
	cfg.Mode = modeChoice

	if cfg.Mode == "client" || cfg.Mode == "both" {
		cfg.ClientName = promptDefault(reader, out, "Client name", cfg.ClientName)
	}
	if cfg.Mode == "provider" || cfg.Mode == "both" {
		cfg.ProviderName = promptDefault(reader, out, "Provider name", cfg.ProviderName)
		detected := detectBackends()
		cfg = configureProviderBackend(cfg, detected, reader, out)
	}

	if len(cfg.Libp2pBootstrap) == 0 {
		def := cfgpkg.Default()
		cfg.Libp2pBootstrap = append([]string(nil), def.Libp2pBootstrap...)
	}
	if strings.TrimSpace(cfg.Libp2pRendezvous) == "" {
		cfg.Libp2pRendezvous = cfgpkg.Default().Libp2pRendezvous
	}
	cfg.Libp2pAutoRelay = true
	cfg.Libp2pNATPortMap = true
	cfg.Libp2pHolePunching = true

	_, _ = fmt.Fprintf(out, "Configured mode=%s transport=%s\n", cfg.Mode, cfg.Transport)
	if logger != nil {
		logger.Info("interactive setup completed", slog.String("mode", cfg.Mode), slog.String("transport", cfg.Transport), slog.String("backend_kind", cfg.BackendKind))
	}
	return cfg
}

func configureProviderBackend(cfg cfgpkg.Config, detected []backendDetection, reader *bufio.Reader, out io.Writer) cfgpkg.Config {
	selected := backendDetection{Label: "Ollama", Kind: "ollama", URL: "http://127.0.0.1:11434"}
	if len(detected) > 0 {
		_, _ = fmt.Fprintln(out, "Detected local backends:")
		for i, d := range detected {
			_, _ = fmt.Fprintf(out, "  [%d] %s (%s) models=%d\n", i+1, d.Label, d.URL, len(d.Models))
		}
		choices := make([]string, 0, len(detected)+1)
		for _, d := range detected {
			choices = append(choices, d.Label)
		}
		choices = append(choices, "Manual")
		chosen := promptChoice(reader, out, "Select provider backend", choices, choices[0])
		if chosen == "Manual" {
			selected = promptManualBackend(reader, out)
		} else {
			for _, d := range detected {
				if d.Label == chosen {
					selected = d
					break
				}
			}
		}
	} else {
		_, _ = fmt.Fprintln(out, "No local backend auto-detected. Using manual setup.")
		selected = promptManualBackend(reader, out)
	}

	cfg.BackendKind = selected.Kind
	cfg.BackendURL = promptDefault(reader, out, "Backend URL", selected.URL)

	modelsDefault := cfg.Models
	if len(selected.Models) > 0 {
		modelsDefault = selected.Models
	}
	if len(modelsDefault) > 0 {
		_, _ = fmt.Fprintf(out, "Detected models: %s\n", strings.Join(modelsDefault, ","))
		exposeAll := promptYesNo(reader, out, "Expose all detected models?", true)
		if exposeAll {
			cfg.Models = append([]string(nil), modelsDefault...)
			return cfg
		}
	}
	modelsInput := promptDefault(reader, out, "Models to expose (comma-separated)", strings.Join(modelsDefault, ","))
	cfg.Models = splitCSV(modelsInput)
	if len(cfg.Models) == 0 && len(modelsDefault) > 0 {
		cfg.Models = append([]string(nil), modelsDefault...)
	}
	return cfg
}

func promptManualBackend(reader *bufio.Reader, out io.Writer) backendDetection {
	choice := promptChoice(reader, out, "Backend type", []string{"Ollama", "LM Studio", "OpenAI Compatible"}, "Ollama")
	switch choice {
	case "LM Studio":
		return backendDetection{Label: "LM Studio", Kind: "openai", URL: "http://127.0.0.1:1234"}
	case "OpenAI Compatible":
		return backendDetection{Label: "OpenAI Compatible", Kind: "openai", URL: "http://127.0.0.1:9000"}
	default:
		return backendDetection{Label: "Ollama", Kind: "ollama", URL: "http://127.0.0.1:11434"}
	}
}

func promptChoice(reader *bufio.Reader, out io.Writer, label string, options []string, defaultValue string) string {
	_, _ = fmt.Fprintf(out, "%s [%s] (default: %s): ", label, strings.Join(options, "/"), defaultValue)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultValue
	}
	for _, o := range options {
		if strings.EqualFold(line, o) {
			return o
		}
	}
	if idx, err := strconv.Atoi(line); err == nil && idx >= 1 && idx <= len(options) {
		return options[idx-1]
	}
	return defaultValue
}

func promptDefault(reader *bufio.Reader, out io.Writer, label, defaultValue string) string {
	_, _ = fmt.Fprintf(out, "%s (default: %s): ", label, defaultValue)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultValue
	}
	return line
}

func promptYesNo(reader *bufio.Reader, out io.Writer, label string, defaultYes bool) bool {
	def := "y/N"
	if defaultYes {
		def = "Y/n"
	}
	_, _ = fmt.Fprintf(out, "%s [%s]: ", label, def)
	line, _ := reader.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return defaultYes
	}
	return line == "y" || line == "yes"
}

func detectBackends() []backendDetection {
	out := make([]backendDetection, 0, 2)
	if models, ok := detectOllamaModels(); ok {
		out = append(out, backendDetection{
			Label:  "Ollama",
			Kind:   "ollama",
			URL:    "http://127.0.0.1:11434",
			Models: models,
		})
	}
	if models, ok := detectLMStudioModels(); ok {
		out = append(out, backendDetection{
			Label:  "LM Studio",
			Kind:   "openai",
			URL:    "http://127.0.0.1:1234",
			Models: models,
		})
	}
	return out
}

func detectOllamaModels() ([]string, bool) {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:11434/api/tags")
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, false
	}
	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, false
	}
	models := make([]string, 0, len(payload.Models))
	for _, m := range payload.Models {
		name := strings.TrimSpace(m.Name)
		if name != "" {
			models = append(models, name)
		}
	}
	return models, true
}

func detectLMStudioModels() ([]string, bool) {
	client := &http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get("http://127.0.0.1:1234/v1/models")
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, false
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, false
	}
	models := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		id := strings.TrimSpace(m.ID)
		if id != "" {
			models = append(models, id)
		}
	}
	return models, true
}

func findArgValue(args []string, name string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(args[i], name+"=") {
			return strings.TrimPrefix(args[i], name+"=")
		}
	}
	return ""
}
