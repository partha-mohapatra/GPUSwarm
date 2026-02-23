package p2p

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"

	"infermeshai/internal/model"
)

type Libp2pDiscovery struct {
	node       *Libp2pNode
	rendezvous string
	timeout    time.Duration
	logger     *slog.Logger
	mu         sync.Mutex
	retryAfter map[peer.ID]time.Time
}

func NewLibp2pDiscovery(node *Libp2pNode, rendezvous string, timeout time.Duration, logger *slog.Logger) *Libp2pDiscovery {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &Libp2pDiscovery{
		node:       node,
		rendezvous: rendezvous,
		timeout:    timeout,
		logger:     logger,
		retryAfter: make(map[peer.ID]time.Time),
	}
}

func (d *Libp2pDiscovery) ListCapabilities(ctx context.Context) ([]model.CapabilityDocument, error) {
	peerSet := make(map[peer.ID]struct{})
	for _, p := range d.node.Peers() {
		peerSet[p] = struct{}{}
	}
	found := d.node.DiscoverPeers(ctx, d.rendezvous, 32)
	dhtCandidates := len(found)
	connectedViaDHT := 0
	for _, ai := range found {
		if ai.ID == "" || ai.ID == d.node.host.ID() {
			continue
		}
		d.node.host.Peerstore().AddAddrs(ai.ID, ai.Addrs, time.Minute)
		if err := d.node.host.Connect(ctx, ai); err == nil {
			connectedViaDHT++
			d.logger.Info("discovered and connected peer", slog.String("peer_id", ai.ID.String()))
		} else {
			d.logger.Warn("discovered peer connect failed", slog.String("peer_id", ai.ID.String()), slog.String("error", err.Error()))
		}
		peerSet[ai.ID] = struct{}{}
	}

	caps := make(map[string]model.CapabilityDocument)
	heartbeats := d.node.Heartbeats()
	announcedAddrs := 0
	for _, hb := range heartbeats {
		if hb.PeerID != "" && len(hb.AnnounceAddrs) > 0 {
			addrs := prioritizeRelayAddrs(hb.AnnounceAddrs)
			if len(addrs) < len(hb.AnnounceAddrs) {
				if pid, err := peer.Decode(hb.PeerID); err == nil {
					d.node.host.Peerstore().ClearAddrs(pid)
				}
			}
			added := d.node.AddPeerAddrs(hb.PeerID, addrs, time.Minute)
			announcedAddrs += added
			if added > 0 {
				if pid, err := peer.Decode(hb.PeerID); err == nil {
					peerSet[pid] = struct{}{}
				}
			}
		}
		caps[hb.PeerID] = hb
	}

	registryPeers := 0
	registryCaps := 0
	for _, bp := range d.node.BootstrapPeers() {
		if bp.ID == "" || bp.ID == d.node.host.ID() {
			continue
		}
		registryPeers++
		d.node.host.Peerstore().AddAddrs(bp.ID, bp.Addrs, time.Minute)
		if err := d.node.host.Connect(ctx, bp); err != nil {
			d.logger.Warn("bootstrap registry connect failed", slog.String("peer_id", bp.ID.String()), slog.String("error", err.Error()))
			continue
		}
		entries, err := QueryRegistry(ctx, d.node, bp.ID, d.timeout)
		if err != nil {
			d.logger.Warn("bootstrap registry query failed", slog.String("peer_id", bp.ID.String()), slog.String("error", err.Error()))
			continue
		}
		for _, c := range entries {
			if c.PeerID == "" || c.PeerID == d.node.PeerID() {
				continue
			}
			relayAddrs := synthesizeRelayAddrs(bp, c.PeerID)
			prioritized := prioritizeRelayAddrs(c.AnnounceAddrs)
			if len(relayAddrs) > 0 {
				if pid, err := peer.Decode(c.PeerID); err == nil {
					d.node.host.Peerstore().ClearAddrs(pid)
				}
			}
			if len(relayAddrs) > 0 {
				added := d.node.AddPeerAddrs(c.PeerID, relayAddrs, time.Minute)
				announcedAddrs += added
			}
			if len(prioritized) > 0 {
				added := d.node.AddPeerAddrs(c.PeerID, prioritized, time.Minute)
				announcedAddrs += added
			}
			if pid, err := peer.Decode(c.PeerID); err == nil {
				peerSet[pid] = struct{}{}
			}
			caps[c.PeerID] = c
			registryCaps++
		}
	}

	queryAttempts := 0
	querySuccess := 0
	for pid := range peerSet {
		// If we already have a recent capability from heartbeat/registry, do not
		// re-probe over capability stream on every request.
		if _, ok := caps[pid.String()]; ok {
			continue
		}
		if !d.shouldQuery(pid) {
			continue
		}
		queryAttempts++
		cap, err := QueryCapability(ctx, d.node, pid, d.timeout)
		if err != nil {
			d.markQueryFailure(pid)
			d.logger.Warn("capability query failed", slog.String("peer_id", pid.String()), slog.String("error", err.Error()))
			continue
		}
		d.markQuerySuccess(pid)
		if cap.GatewayURL == "" {
			cap.GatewayURL = "libp2p://" + pid.String()
		}
		caps[cap.PeerID] = cap
		querySuccess++
	}
	d.logger.Info(
		"discovery snapshot",
		slog.String("self_peer_id", d.node.PeerID()),
		slog.String("rendezvous", d.rendezvous),
		slog.Int("network_peers", len(peerSet)),
		slog.Int("dht_candidates", dhtCandidates),
		slog.Int("connected_via_dht", connectedViaDHT),
		slog.Int("heartbeat_cache", len(heartbeats)),
		slog.Int("heartbeat_announced_addrs", announcedAddrs),
		slog.Int("bootstrap_registry_peers", registryPeers),
		slog.Int("bootstrap_registry_caps", registryCaps),
		slog.Int("cap_query_attempts", queryAttempts),
		slog.Int("cap_query_success", querySuccess),
		slog.Int("peer_count", len(caps)),
	)

	out := make([]model.CapabilityDocument, 0, len(caps))
	for _, c := range caps {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PeerID < out[j].PeerID })
	if len(out) == 0 {
		d.logger.Warn(
			"discovery yielded no remote peers",
			slog.String("hint", "check bootstrap peers, provider mode, security groups/NAT, and matching rendezvous"),
		)
		return nil, ErrNoPeersDiscovered
	}
	return out, nil
}

func (d *Libp2pDiscovery) shouldQuery(pid peer.ID) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if next, ok := d.retryAfter[pid]; ok && time.Now().Before(next) {
		return false
	}
	return true
}

func (d *Libp2pDiscovery) markQueryFailure(pid peer.ID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.retryAfter[pid] = time.Now().Add(30 * time.Second)
}

func (d *Libp2pDiscovery) markQuerySuccess(pid peer.ID) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.retryAfter, pid)
}

func synthesizeRelayAddrs(relay peer.AddrInfo, targetPeerID string) []string {
	out := make([]string, 0, len(relay.Addrs))
	for _, a := range relay.Addrs {
		relaySuffix, err := multiaddr.NewMultiaddr("/p2p/" + relay.ID.String() + "/p2p-circuit/p2p/" + targetPeerID)
		if err != nil {
			continue
		}
		out = append(out, a.Encapsulate(relaySuffix).String())
	}
	return out
}

func prioritizeRelayAddrs(in []string) []string {
	relay := make([]string, 0, len(in))
	other := make([]string, 0, len(in))
	for _, a := range in {
		if strings.Contains(a, "/p2p-circuit/") {
			relay = append(relay, a)
			continue
		}
		other = append(other, a)
	}
	if len(relay) > 0 {
		return relay
	}
	return other
}
