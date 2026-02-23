package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"infermeshai/internal/model"
)

type LANDiscovery struct {
	mu      sync.RWMutex
	entries map[string]lanEntry
	ttl     time.Duration
	logger  *slog.Logger
}

type lanEntry struct {
	cap      model.CapabilityDocument
	lastSeen time.Time
}

func NewLANDiscovery(listenAddr string, ttl time.Duration, logger *slog.Logger) (*LANDiscovery, error) {
	if ttl <= 0 {
		ttl = 15 * time.Second
	}
	pc, err := net.ListenPacket("udp4", listenAddr)
	if err != nil {
		return nil, err
	}
	d := &LANDiscovery{
		entries: make(map[string]lanEntry),
		ttl:     ttl,
		logger:  logger,
	}
	go d.listen(pc)
	return d, nil
}

func (d *LANDiscovery) listen(pc net.PacketConn) {
	defer pc.Close()
	buf := make([]byte, 64*1024)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		var cap model.CapabilityDocument
		if err := json.Unmarshal(buf[:n], &cap); err != nil {
			continue
		}
		norm := normalizeCapability(cap, addr)
		key := norm.PeerID + "|" + norm.GatewayURL
		d.mu.Lock()
		d.entries[key] = lanEntry{cap: norm, lastSeen: time.Now()}
		d.mu.Unlock()
	}
}

func (d *LANDiscovery) ListCapabilities(ctx context.Context) ([]model.CapabilityDocument, error) {
	_ = ctx
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]model.CapabilityDocument, 0, len(d.entries))
	for k, v := range d.entries {
		if now.Sub(v.lastSeen) > d.ttl {
			delete(d.entries, k)
			continue
		}
		out = append(out, v.cap)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no lan peers discovered")
	}
	return out, nil
}

type LANAnnouncer struct {
	dst      *net.UDPAddr
	interval time.Duration
	snapshot func() model.CapabilityDocument
	logger   *slog.Logger
}

func NewLANAnnouncer(broadcastAddr string, interval time.Duration, snapshot func() model.CapabilityDocument, logger *slog.Logger) (*LANAnnouncer, error) {
	if interval <= 0 {
		interval = 3 * time.Second
	}
	dst, err := net.ResolveUDPAddr("udp4", broadcastAddr)
	if err != nil {
		return nil, err
	}
	return &LANAnnouncer{dst: dst, interval: interval, snapshot: snapshot, logger: logger}, nil
}

func (a *LANAnnouncer) Run(ctx context.Context) error {
	conn, err := net.DialUDP("udp4", nil, a.dst)
	if err != nil {
		return err
	}
	defer conn.Close()
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		cap := a.snapshot()
		b, err := json.Marshal(cap)
		if err == nil {
			_, _ = conn.Write(b)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(a.interval):
		}
	}
}

func normalizeCapability(cap model.CapabilityDocument, addr net.Addr) model.CapabilityDocument {
	udp, _ := addr.(*net.UDPAddr)
	ip := ""
	if udp != nil {
		ip = udp.IP.String()
	}
	if cap.GatewayURL == "" {
		port := 7777
		cap.GatewayURL = "http://" + net.JoinHostPort(ip, strconv.Itoa(port))
		return cap
	}
	u, err := url.Parse(cap.GatewayURL)
	if err != nil {
		return cap
	}
	host := u.Hostname()
	if host == "" || host == "127.0.0.1" || host == "localhost" || strings.HasPrefix(host, "::1") {
		port := u.Port()
		if port == "" {
			port = "7777"
		}
		u.Host = net.JoinHostPort(ip, port)
		cap.GatewayURL = u.String()
	}
	return cap
}
