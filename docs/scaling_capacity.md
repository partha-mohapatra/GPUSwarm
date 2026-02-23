# GPUSwarm Scaling and Capacity Guide

This document captures practical sizing guidance for bootstrap/relay infrastructure and provider scale-out.

## 1. Important Clarification

Not every inference request must traverse bootstrap.

Request path is:

1. Direct: `client -> provider` (preferred)
2. Fallback: `client -> bootstrap proxy -> provider` (when direct fails)

Bootstrap load therefore depends heavily on NAT/firewall conditions and relay fallback ratio.

## 2. Bootstrap Responsibilities

A public bootstrap node may handle:

- DHT participation and peer discovery
- registry query/announce (`/infermesh/registry/*`)
- heartbeat gossip participation
- optional relay/proxy forwarding for inference

These are very different loads. Discovery/control-plane is light; relay/proxy data-plane is heavy.

## 3. Capacity Expectations (Rule-of-Thumb)

`t.micro` is acceptable for early testing, but not for sustained proxy/relay traffic.

Conservative guidance:

- `t.micro`:
  - discovery/control-plane only: small meshes (dozens of peers)
  - proxy/relay traffic: very limited; easily saturated
- `t3.small` / `t3.medium`:
  - better baseline for public bootstrap + relay usage
  - suitable for moderate concurrent stream forwarding
- larger instances or relay pool:
  - required when many clients are behind restrictive NAT and proxy fallback is frequent

Always benchmark with your real model/token streaming profile before production rollout.

## 4. What to Monitor

Track at minimum:

- concurrent libp2p streams
- bootstrap CPU, memory, NIC throughput
- proxy fallback rate vs direct success rate
- discovery latency and registry query latency
- inference stream failure rates (`peer infer failed`, `proxy dial failed`)

If proxy fallback rate is high and CPU/NIC is elevated, bootstrap is becoming a bottleneck.

## 5. Recommended Production Topology

Use separation of concerns:

1. Bootstrap/relay pool (public, lightweight model exposure or none)
2. Provider pool (actual inference backends: Ollama/LM Studio/etc.)
3. Client nodes (local API + routing)

## 6. Scale-Out Patterns

### 6.1 Multiple Bootstrap Peers

Configure multiple bootstrap multiaddrs in `libp2p_bootstrap_peers`.

Benefits:

- no single discovery choke point
- better resilience during node failures/restarts

### 6.2 Relay Pool

Run multiple relay-capable nodes:

- `-libp2p-relay-service=true`
- public reachability

Keep them stateless and horizontally scalable.

### 6.3 Provider Horizontal Scale

Scale providers by model family:

- deploy more providers for hot models
- keep client routing in `auto` for score-based distribution

### 6.4 DNS and Load Balancer Usage

Use DNS/LB for control-plane convenience (bootstrap node addressing), but do not assume LB replaces peer identity semantics.

Practical approach:

- DNS name(s) to publish bootstrap endpoints
- static peer multiaddrs in config (or generated config)
- optional LB for ancillary HTTP admin/observability endpoints

For libp2p relay/proxy traffic, preserve stable relay peer identities.

## 7. Autoscaling Approach (AWS Example)

For relay/bootstrap fleet:

- use ASG with minimum 2 nodes across AZs
- health check on daemon process + critical ports
- scale on CPU + network throughput + connection count signals

For provider fleet:

- scale based on queue depth, request latency, and backend saturation
- group by model capability when needed

## 8. Operational Checklist

Before increasing traffic:

1. Add at least 2 bootstrap/relay nodes.
2. Verify clients/providers list all bootstrap peers.
3. Measure direct vs proxy path ratio.
4. Benchmark concurrent chat streams under expected load.
5. Increase provider count before bootstrap becomes saturated.

## 9. Capacity Planning Summary

- Start: `t.micro` only for PoC and low concurrency.
- Early real usage: move bootstrap to `t3.small`/`t3.medium`.
- Growth: relay/ bootstrap pool + horizontal providers.
- Design goal: maximize direct paths; minimize proxy fallback.
