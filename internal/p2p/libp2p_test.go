package p2p

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/proto"
	"github.com/multiformats/go-multiaddr"

	"infermeshai/internal/model"
)

type capProvider struct{ cap model.CapabilityDocument }

func (c capProvider) Snapshot() model.CapabilityDocument { return c.cap }

type inferStub struct{}

func (inferStub) Infer(ctx context.Context, req model.ChatCompletionRequest, peerCtx model.PeerContext) (int, string, io.ReadCloser, func(), error) {
	_ = ctx
	_ = req
	_ = peerCtx
	body := io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	return 200, "text/event-stream", body, func() {}, nil
}

func TestLibp2pDiscoveryAndTransport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	providerIDFile := t.TempDir() + "/provider.key"
	providerNode, err := NewLibp2pNode(ctx, Libp2pConfig{ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}, Rendezvous: "test-rdz", EnableMDNS: false}, providerIDFile, logger)
	if err != nil {
		t.Fatalf("new provider node: %v", err)
	}
	defer providerNode.Close()

	providerCap := model.CapabilityDocument{PeerID: providerNode.PeerID(), Models: []model.ModelSpec{{Name: "llama3"}}, GatewayURL: "libp2p://" + providerNode.PeerID(), Reliability: 1}
	RegisterCapabilityHandler(providerNode, capProvider{cap: providerCap})
	RegisterInferHandler(providerNode, inferStub{})

	clientIDFile := t.TempDir() + "/client.key"
	clientNode, err := NewLibp2pNode(ctx, Libp2pConfig{ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"}, Rendezvous: "test-rdz", EnableMDNS: false}, clientIDFile, logger)
	if err != nil {
		t.Fatalf("new client node: %v", err)
	}
	defer clientNode.Close()

	if err := connectNodes(ctx, clientNode, providerNode); err != nil {
		t.Fatalf("connect nodes: %v", err)
	}

	disc := NewLibp2pDiscovery(clientNode, "test-rdz", 2*time.Second, logger)
	caps, err := disc.ListCapabilities(ctx)
	if err != nil {
		t.Fatalf("discover caps: %v", err)
	}
	if len(caps) == 0 {
		t.Fatalf("expected discovered capability")
	}

	gc := NewLibp2pGatewayClient(clientNode)
	res, err := gc.Infer(ctx, "libp2p://"+providerNode.PeerID(), model.ChatCompletionRequest{Model: "llama3", Stream: true}, model.PeerContext{ClientPeerID: clientNode.PeerID()})
	if err != nil {
		t.Fatalf("libp2p infer: %v", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(b), "[DONE]") {
		t.Fatalf("expected done marker in stream: %s", string(b))
	}
}

func connectNodes(ctx context.Context, a, b *Libp2pNode) error {
	for _, addr := range b.Host().Addrs() {
		full, err := multiaddr.NewMultiaddr(fmt.Sprintf("%s/p2p/%s", addr.String(), b.Host().ID().String()))
		if err != nil {
			continue
		}
		ai, err := peer.AddrInfoFromP2pAddr(full)
		if err != nil {
			continue
		}
		if err := a.Host().Connect(ctx, *ai); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no connectable address")
}

func TestAddPeerAddrsFromAnnounce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	providerNode, err := NewLibp2pNode(ctx, Libp2pConfig{
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
		Rendezvous:  "test-addrs",
		EnableMDNS:  false,
	}, t.TempDir()+"/provider.key", logger)
	if err != nil {
		t.Fatalf("new provider node: %v", err)
	}
	defer providerNode.Close()

	clientNode, err := NewLibp2pNode(ctx, Libp2pConfig{
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
		Rendezvous:  "test-addrs",
		EnableMDNS:  false,
	}, t.TempDir()+"/client.key", logger)
	if err != nil {
		t.Fatalf("new client node: %v", err)
	}
	defer clientNode.Close()

	added := clientNode.AddPeerAddrs(providerNode.PeerID(), providerNode.AdvertiseAddrs(), time.Minute)
	if added == 0 {
		t.Fatalf("expected announced addresses to be added to peerstore")
	}

	if err := clientNode.Host().Connect(ctx, peer.AddrInfo{ID: providerNode.Host().ID()}); err != nil {
		t.Fatalf("connect using peerstore-announced addresses: %v", err)
	}
}

func TestDiscoveryViaBootstrapRegistry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	bootstrapNode, err := NewLibp2pNode(ctx, Libp2pConfig{
		ListenAddrs:        []string{"/ip4/127.0.0.1/tcp/0"},
		Rendezvous:         "test-registry",
		EnableMDNS:         false,
		EnableRelayService: true,
	}, t.TempDir()+"/bootstrap.key", logger)
	if err != nil {
		t.Fatalf("new bootstrap node: %v", err)
	}
	defer bootstrapNode.Close()
	RegisterRegistryHandler(bootstrapNode)

	bootstrapAddr := fmt.Sprintf("%s/p2p/%s", bootstrapNode.Host().Addrs()[0].String(), bootstrapNode.PeerID())
	providerNode, err := NewLibp2pNode(ctx, Libp2pConfig{
		ListenAddrs:     []string{"/ip4/127.0.0.1/tcp/0"},
		Rendezvous:      "test-registry",
		EnableMDNS:      false,
		BootstrapPeers:  []string{bootstrapAddr},
		EnableAutoRelay: true,
	}, t.TempDir()+"/provider.key", logger)
	if err != nil {
		t.Fatalf("new provider node: %v", err)
	}
	defer providerNode.Close()

	providerCap := model.CapabilityDocument{
		PeerID:     providerNode.PeerID(),
		NodeName:   "provider-registry",
		Models:     []model.ModelSpec{{Name: "smollm2:135m"}},
		GatewayURL: "libp2p://" + providerNode.PeerID(),
	}
	providerCap.AnnounceAddrs = providerNode.AdvertiseAddrs()
	if err := connectNodes(ctx, providerNode, bootstrapNode); err != nil {
		t.Fatalf("connect provider->bootstrap: %v", err)
	}
	if err := connectNodes(ctx, bootstrapNode, providerNode); err != nil {
		t.Fatalf("connect bootstrap->provider: %v", err)
	}

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		_ = providerNode.PublishHeartbeat(ctx, providerCap)
		_ = AnnounceRegistry(ctx, providerNode, bootstrapNode.Host().ID(), providerCap, 2*time.Second)
		if len(bootstrapNode.RegistryCapabilities()) > 0 {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	for len(bootstrapNode.RegistryCapabilities()) == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if len(bootstrapNode.RegistryCapabilities()) == 0 {
		t.Fatalf("bootstrap did not receive provider registry announce")
	}

	clientNode, err := NewLibp2pNode(ctx, Libp2pConfig{
		ListenAddrs:     []string{"/ip4/127.0.0.1/tcp/0"},
		Rendezvous:      "test-registry",
		EnableMDNS:      false,
		BootstrapPeers:  []string{bootstrapAddr},
		EnableAutoRelay: true,
	}, t.TempDir()+"/client.key", logger)
	if err != nil {
		t.Fatalf("new client node: %v", err)
	}
	defer clientNode.Close()

	disc := NewLibp2pDiscovery(clientNode, "test-registry", 2*time.Second, logger)
	caps, err := disc.ListCapabilities(ctx)
	if err != nil {
		t.Fatalf("discover caps via bootstrap registry: %v", err)
	}
	found := false
	for _, c := range caps {
		if c.PeerID == providerNode.PeerID() && c.SupportsModel("smollm2:135m") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected provider capability from bootstrap registry")
	}
}

func TestRelayServiceRegistersHopProtocol(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	relayNode, err := NewLibp2pNode(ctx, Libp2pConfig{
		ListenAddrs:        []string{"/ip4/127.0.0.1/tcp/0"},
		Rendezvous:         "test-relay-hop",
		EnableMDNS:         false,
		EnableRelayService: true,
	}, t.TempDir()+"/relay.key", logger)
	if err != nil {
		t.Fatalf("new relay node: %v", err)
	}
	defer relayNode.Close()

	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, p := range relayNode.Host().Mux().Protocols() {
			if p == proto.ProtoIDv2Hop {
				return
			}
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected relay hop protocol %s to be registered", proto.ProtoIDv2Hop)
}

func TestPrioritizeRelayAddrs(t *testing.T) {
	in := []string{
		"/ip4/10.1.2.3/tcp/39001/p2p/12D3KooAAA",
		"/ip4/3.4.5.6/tcp/39001/p2p/12D3KooBOOT/p2p-circuit/p2p/12D3KooAAA",
		"/ip4/127.0.0.1/tcp/39001/p2p/12D3KooAAA",
	}
	out := prioritizeRelayAddrs(in)
	if len(out) != 1 {
		t.Fatalf("expected only relay addrs when relay is present, got %d", len(out))
	}
	if !strings.Contains(out[0], "/p2p-circuit/") {
		t.Fatalf("expected prioritized relay address, got: %s", out[0])
	}
}

func TestInferViaBootstrapProxyFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	bootstrapNode, err := NewLibp2pNode(ctx, Libp2pConfig{
		ListenAddrs:        []string{"/ip4/127.0.0.1/tcp/0"},
		Rendezvous:         "test-proxy-fallback",
		EnableMDNS:         false,
		EnableRelayService: true,
	}, t.TempDir()+"/bootstrap.key", logger)
	if err != nil {
		t.Fatalf("new bootstrap node: %v", err)
	}
	defer bootstrapNode.Close()
	RegisterInferProxyHandler(bootstrapNode, logger)

	bootstrapAddr := fmt.Sprintf("%s/p2p/%s", bootstrapNode.Host().Addrs()[0].String(), bootstrapNode.PeerID())
	providerNode, err := NewLibp2pNode(ctx, Libp2pConfig{
		ListenAddrs:    []string{"/ip4/127.0.0.1/tcp/0"},
		Rendezvous:     "test-proxy-fallback",
		EnableMDNS:     false,
		BootstrapPeers: []string{bootstrapAddr},
	}, t.TempDir()+"/provider.key", logger)
	if err != nil {
		t.Fatalf("new provider node: %v", err)
	}
	defer providerNode.Close()
	RegisterInferHandler(providerNode, inferStub{})
	if err := connectNodes(ctx, providerNode, bootstrapNode); err != nil {
		t.Fatalf("connect provider->bootstrap: %v", err)
	}
	if err := connectNodes(ctx, bootstrapNode, providerNode); err != nil {
		t.Fatalf("connect bootstrap->provider: %v", err)
	}

	clientNode, err := NewLibp2pNode(ctx, Libp2pConfig{
		ListenAddrs:    []string{"/ip4/127.0.0.1/tcp/0"},
		Rendezvous:     "test-proxy-fallback",
		EnableMDNS:     false,
		BootstrapPeers: []string{bootstrapAddr},
	}, t.TempDir()+"/client.key", logger)
	if err != nil {
		t.Fatalf("new client node: %v", err)
	}
	defer clientNode.Close()
	if err := connectNodes(ctx, clientNode, bootstrapNode); err != nil {
		t.Fatalf("connect client->bootstrap: %v", err)
	}

	gc := NewLibp2pGatewayClient(clientNode)
	res, err := gc.Infer(ctx, "libp2p://"+providerNode.PeerID(), model.ChatCompletionRequest{
		Model:  "llama3",
		Stream: true,
	}, model.PeerContext{ClientPeerID: clientNode.PeerID()})
	if err != nil {
		t.Fatalf("infer via bootstrap proxy fallback: %v", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(b), "[DONE]") {
		t.Fatalf("expected done marker in stream: %s", string(b))
	}
}
