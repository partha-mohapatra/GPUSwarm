package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Mode                string        `json:"mode" yaml:"mode"`
	Transport           string        `json:"transport" yaml:"transport"`
	ClientName          string        `json:"client_name" yaml:"client_name"`
	ProviderName        string        `json:"provider_name" yaml:"provider_name"`
	IdentityFile        string        `json:"identity_file" yaml:"identity_file"`
	ClientAddr          string        `json:"client_addr" yaml:"client_addr"`
	ProviderAddr        string        `json:"provider_addr" yaml:"provider_addr"`
	ProviderPeerURLs    []string      `json:"provider_peer_urls" yaml:"provider_peer_urls"`
	BackendKind         string        `json:"backend_kind" yaml:"backend_kind"`
	BackendURL          string        `json:"backend_url" yaml:"backend_url"`
	PeerID              string        `json:"peer_id" yaml:"peer_id"`
	GPU                 string        `json:"gpu" yaml:"gpu"`
	VRAMFreeMB          int           `json:"vram_free_mb" yaml:"vram_free_mb"`
	Models              []string      `json:"models" yaml:"models"`
	ModelSpecs          []string      `json:"model_specs" yaml:"model_specs"`
	AdvertiseURL        string        `json:"advertise_url" yaml:"advertise_url"`
	MaxInflight         int           `json:"max_inflight" yaml:"max_inflight"`
	RatePerSecond       float64       `json:"rate_per_second" yaml:"rate_per_second"`
	Burst               int           `json:"burst" yaml:"burst"`
	MaxTokensPerRequest int           `json:"max_tokens_per_request" yaml:"max_tokens_per_request"`
	NightOnly           bool          `json:"night_only" yaml:"night_only"`
	NightStartHour      int           `json:"night_start_hour" yaml:"night_start_hour"`
	NightEndHour        int           `json:"night_end_hour" yaml:"night_end_hour"`
	IdleOnly            bool          `json:"idle_only" yaml:"idle_only"`
	MaxLoad1m           float64       `json:"max_load1m" yaml:"max_load1m"`
	PoWDifficulty       int           `json:"pow_difficulty" yaml:"pow_difficulty"`
	MinPeerReputation   float64       `json:"min_peer_reputation" yaml:"min_peer_reputation"`
	InitialCredits      int64         `json:"initial_credits" yaml:"initial_credits"`
	LANDiscovery        bool          `json:"lan_discovery" yaml:"lan_discovery"`
	LANListenAddr       string        `json:"lan_listen_addr" yaml:"lan_listen_addr"`
	LANBroadcastAddr    string        `json:"lan_broadcast_addr" yaml:"lan_broadcast_addr"`
	LANAnnounceInterval time.Duration `json:"lan_announce_interval" yaml:"lan_announce_interval"`
	LANPeerTTL          time.Duration `json:"lan_peer_ttl" yaml:"lan_peer_ttl"`
	Libp2pListenAddrs   []string      `json:"libp2p_listen_addrs" yaml:"libp2p_listen_addrs"`
	Libp2pBootstrap     []string      `json:"libp2p_bootstrap_peers" yaml:"libp2p_bootstrap_peers"`
	Libp2pRendezvous    string        `json:"libp2p_rendezvous" yaml:"libp2p_rendezvous"`
	Libp2pAutoRelay     bool          `json:"libp2p_auto_relay" yaml:"libp2p_auto_relay"`
	Libp2pRelayService  bool          `json:"libp2p_relay_service" yaml:"libp2p_relay_service"`
	Libp2pNATPortMap    bool          `json:"libp2p_nat_port_map" yaml:"libp2p_nat_port_map"`
	Libp2pHolePunching  bool          `json:"libp2p_hole_punching" yaml:"libp2p_hole_punching"`
	RequestTimeout      time.Duration `json:"request_timeout" yaml:"request_timeout"`
	LogFile             string        `json:"log_file" yaml:"log_file"`
	RoutingPolicy       string        `json:"routing_policy" yaml:"routing_policy"`
	FavoritePeers       []string      `json:"favorite_peers" yaml:"favorite_peers"`
	BlockedPeers        []string      `json:"blocked_peers" yaml:"blocked_peers"`
	AllowedPeers        []string      `json:"allowed_peers" yaml:"allowed_peers"`
	StickyPeerTTL       time.Duration `json:"sticky_peer_ttl" yaml:"sticky_peer_ttl"`
	WeightLatency       float64       `json:"weight_latency" yaml:"weight_latency"`
	WeightQueueDepth    float64       `json:"weight_queue_depth" yaml:"weight_queue_depth"`
	WeightVRAMPressure  float64       `json:"weight_vram_pressure" yaml:"weight_vram_pressure"`
	WeightReliability   float64       `json:"weight_reliability" yaml:"weight_reliability"`
	WeightFavoriteBonus float64       `json:"weight_favorite_bonus" yaml:"weight_favorite_bonus"`
	WeightKnownBonus    float64       `json:"weight_known_bonus" yaml:"weight_known_bonus"`
}

func DefaultPath() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "./infermeshai.config.yaml"
	}
	return filepath.Join(h, ".infermeshai", "config.yaml")
}

func Default() Config {
	host, _ := os.Hostname()
	host = strings.TrimSpace(host)
	if host == "" {
		host = "infermesh-node"
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		home = "."
	}
	return Config{
		Mode:                "both",
		Transport:           "libp2p",
		ClientName:          host + "-client",
		ProviderName:        host + "-provider",
		IdentityFile:        filepath.Join(home, ".infermeshai", "infermeshd.identity.pem"),
		ClientAddr:          ":5555",
		ProviderAddr:        ":7777",
		BackendKind:         "openai",
		BackendURL:          "http://127.0.0.1:9000",
		GPU:                 "mock-gpu",
		VRAMFreeMB:          16000,
		Models:              []string{},
		MaxInflight:         4,
		RatePerSecond:       20.0,
		Burst:               20,
		NightStartHour:      22,
		NightEndHour:        6,
		MaxLoad1m:           0.8,
		InitialCredits:      100,
		LANDiscovery:        true,
		LANListenAddr:       ":9559",
		LANBroadcastAddr:    "255.255.255.255:9559",
		LANAnnounceInterval: 3 * time.Second,
		LANPeerTTL:          15 * time.Second,
		Libp2pListenAddrs:   []string{"/ip4/0.0.0.0/tcp/39001"},
		Libp2pBootstrap:     []string{"/ip4/3.234.252.173/tcp/39001/p2p/12D3KooWFU1RPiGmvFcURiaph5GueGpvQ2VPkQfR2YQej6ogmpzg"},
		Libp2pRendezvous:    "infermesh-rendezvous",
		Libp2pAutoRelay:     true,
		Libp2pNATPortMap:    true,
		Libp2pHolePunching:  true,
		RequestTimeout:      5 * time.Minute,
		LogFile:             filepath.Join(home, ".infermeshai", "infermeshd.log"),
		RoutingPolicy:       "auto",
		StickyPeerTTL:       0,
		WeightLatency:       1.0,
		WeightQueueDepth:    2.0,
		WeightVRAMPressure:  500.0,
		WeightReliability:   100.0,
		WeightFavoriteBonus: 10.0,
		WeightKnownBonus:    3.0,
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if isJSON(path) {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return cfg, err
		}
		return cfg, nil
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var (
		b   []byte
		err error
	)
	if isJSON(path) {
		b, err = json.MarshalIndent(cfg, "", "  ")
	} else {
		b, err = yaml.Marshal(cfg)
	}
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(path, b, 0o600)
}

func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ResolveAndValidate(cfg Config) (Config, error) {
	def := Default()

	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		mode = def.Mode
	}
	switch mode {
	case "client", "provider", "both":
	default:
		return cfg, fmt.Errorf("invalid mode: %s", cfg.Mode)
	}
	cfg.Mode = mode

	transport := strings.ToLower(strings.TrimSpace(cfg.Transport))
	if transport == "" {
		transport = def.Transport
	}
	switch transport {
	case "http", "libp2p":
	default:
		return cfg, fmt.Errorf("invalid transport: %s", cfg.Transport)
	}
	cfg.Transport = transport

	if cfg.IdentityFile == "" {
		cfg.IdentityFile = def.IdentityFile
	}
	if strings.TrimSpace(cfg.ClientName) == "" {
		cfg.ClientName = def.ClientName
	}
	if strings.TrimSpace(cfg.ProviderName) == "" {
		cfg.ProviderName = def.ProviderName
	}
	if cfg.ClientAddr == "" {
		cfg.ClientAddr = def.ClientAddr
	}
	if cfg.ProviderAddr == "" {
		cfg.ProviderAddr = def.ProviderAddr
	}
	if cfg.BackendKind == "" {
		cfg.BackendKind = def.BackendKind
	}
	if cfg.BackendURL == "" {
		cfg.BackendURL = def.BackendURL
	}
	if cfg.GPU == "" {
		cfg.GPU = def.GPU
	}
	if cfg.VRAMFreeMB == 0 {
		cfg.VRAMFreeMB = def.VRAMFreeMB
	}
	if cfg.MaxInflight == 0 {
		cfg.MaxInflight = def.MaxInflight
	}
	if cfg.RatePerSecond == 0 {
		cfg.RatePerSecond = def.RatePerSecond
	}
	if cfg.Burst == 0 {
		cfg.Burst = def.Burst
	}
	if cfg.NightStartHour == 0 && cfg.NightEndHour == 0 {
		cfg.NightStartHour = def.NightStartHour
		cfg.NightEndHour = def.NightEndHour
	}
	if cfg.MaxLoad1m == 0 {
		cfg.MaxLoad1m = def.MaxLoad1m
	}
	if cfg.InitialCredits == 0 {
		cfg.InitialCredits = def.InitialCredits
	}
	if cfg.LANListenAddr == "" {
		cfg.LANListenAddr = def.LANListenAddr
	}
	if cfg.LANBroadcastAddr == "" {
		cfg.LANBroadcastAddr = def.LANBroadcastAddr
	}
	if cfg.LANAnnounceInterval == 0 {
		cfg.LANAnnounceInterval = def.LANAnnounceInterval
	}
	if cfg.LANPeerTTL == 0 {
		cfg.LANPeerTTL = def.LANPeerTTL
	}
	if len(cfg.Libp2pListenAddrs) == 0 {
		cfg.Libp2pListenAddrs = def.Libp2pListenAddrs
	}
	if len(cfg.Libp2pBootstrap) == 0 && cfg.Transport == "libp2p" {
		cfg.Libp2pBootstrap = append([]string(nil), def.Libp2pBootstrap...)
	}
	if cfg.Libp2pRendezvous == "" {
		cfg.Libp2pRendezvous = def.Libp2pRendezvous
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = def.RequestTimeout
	}
	if strings.TrimSpace(cfg.LogFile) == "" {
		cfg.LogFile = def.LogFile
	}
	if strings.TrimSpace(cfg.RoutingPolicy) == "" {
		cfg.RoutingPolicy = def.RoutingPolicy
	}
	cfg.RoutingPolicy = strings.ToLower(strings.TrimSpace(cfg.RoutingPolicy))
	switch cfg.RoutingPolicy {
	case "auto", "prefer_known", "prefer_favorites", "manual":
	default:
		return cfg, fmt.Errorf("invalid routing_policy: %s", cfg.RoutingPolicy)
	}
	if cfg.WeightLatency == 0 {
		cfg.WeightLatency = def.WeightLatency
	}
	if cfg.WeightQueueDepth == 0 {
		cfg.WeightQueueDepth = def.WeightQueueDepth
	}
	if cfg.WeightVRAMPressure == 0 {
		cfg.WeightVRAMPressure = def.WeightVRAMPressure
	}
	if cfg.WeightReliability == 0 {
		cfg.WeightReliability = def.WeightReliability
	}
	if cfg.WeightFavoriteBonus == 0 {
		cfg.WeightFavoriteBonus = def.WeightFavoriteBonus
	}
	if cfg.WeightKnownBonus == 0 {
		cfg.WeightKnownBonus = def.WeightKnownBonus
	}

	if (cfg.Mode == "provider" || cfg.Mode == "both") && cfg.BackendURL == "" {
		return cfg, errors.New("backend_url is required in provider/both mode")
	}
	if cfg.Transport == "http" && (cfg.Mode == "client" || cfg.Mode == "both") && len(cfg.ProviderPeerURLs) == 0 && !cfg.LANDiscovery {
		return cfg, errors.New("http client mode requires provider_peer_urls or lan_discovery=true")
	}
	return cfg, nil
}

func isJSON(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".json"
}
