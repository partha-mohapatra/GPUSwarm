package config

import "testing"

func TestResolveClientMinimal(t *testing.T) {
	cfg, err := ResolveAndValidate(Config{Mode: "client", Transport: "libp2p"})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if cfg.ClientAddr == "" || len(cfg.Libp2pListenAddrs) == 0 || cfg.ClientName == "" {
		t.Fatalf("expected client defaults to be populated")
	}
}

func TestResolveInvalidMode(t *testing.T) {
	_, err := ResolveAndValidate(Config{Mode: "bad"})
	if err == nil {
		t.Fatalf("expected invalid mode error")
	}
}

func TestResolveHTTPClientNeedsDiscovery(t *testing.T) {
	_, err := ResolveAndValidate(Config{Mode: "client", Transport: "http", LANDiscovery: false})
	if err == nil {
		t.Fatalf("expected discovery validation error")
	}
}

func TestResolveRoutingPolicy(t *testing.T) {
	cfg, err := ResolveAndValidate(Config{Mode: "client", Transport: "libp2p", RoutingPolicy: "prefer_favorites"})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if cfg.RoutingPolicy != "prefer_favorites" {
		t.Fatalf("expected policy prefer_favorites, got %s", cfg.RoutingPolicy)
	}
}

func TestResolveLibp2pTraversalDefaultsAndOverride(t *testing.T) {
	base := Default()
	base.Mode = "client"
	base.Transport = "libp2p"
	cfg, err := ResolveAndValidate(base)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if !cfg.Libp2pAutoRelay || !cfg.Libp2pHolePunching || !cfg.Libp2pNATPortMap {
		t.Fatalf("expected libp2p traversal defaults enabled")
	}

	base.Libp2pAutoRelay = false
	base.Libp2pHolePunching = false
	base.Libp2pNATPortMap = false
	cfg2, err := ResolveAndValidate(base)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if cfg2.Libp2pAutoRelay || cfg2.Libp2pHolePunching || cfg2.Libp2pNATPortMap {
		t.Fatalf("expected explicit traversal disables to be preserved")
	}
}

func TestResolveFillsLibp2pBootstrapWhenMissing(t *testing.T) {
	cfg, err := ResolveAndValidate(Config{
		Mode:            "client",
		Transport:       "libp2p",
		Libp2pBootstrap: []string{},
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if len(cfg.Libp2pBootstrap) == 0 {
		t.Fatalf("expected default libp2p bootstrap peers to be populated")
	}
}

func TestResolveProviderDoesNotInjectDefaultModels(t *testing.T) {
	cfg, err := ResolveAndValidate(Config{
		Mode:       "provider",
		Transport:  "libp2p",
		BackendURL: "http://127.0.0.1:9000",
		Models:     []string{},
	})
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if len(cfg.Models) != 0 {
		t.Fatalf("expected no default provider models to be injected")
	}
}
