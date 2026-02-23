package app

import (
	"sync"
	"time"

	"infermeshai/internal/model"
)

type CapabilityState struct {
	mu         sync.RWMutex
	doc        model.CapabilityDocument
	latencyEMA float64
	successes  int64
	failures   int64
}

func NewCapabilityState(initial model.CapabilityDocument) *CapabilityState {
	if initial.Reliability == 0 {
		initial.Reliability = 1
	}
	if initial.UpdatedAt.IsZero() {
		initial.UpdatedAt = time.Now().UTC()
	}
	if initial.AvgLatencyMS == 0 {
		initial.AvgLatencyMS = 50
	}
	return &CapabilityState{doc: initial, latencyEMA: float64(initial.AvgLatencyMS)}
}

func (s *CapabilityState) Snapshot() model.CapabilityDocument {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d := s.doc
	d.UpdatedAt = time.Now().UTC()
	return d
}

func (s *CapabilityState) SetQueueDepth(depth int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.doc.QueueDepth = depth
	s.doc.UpdatedAt = time.Now().UTC()
}

func (s *CapabilityState) UpdateLatency(d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ms := float64(d.Milliseconds())
	if ms <= 0 {
		ms = 1
	}
	const alpha = 0.3
	s.latencyEMA = alpha*ms + (1-alpha)*s.latencyEMA
	s.doc.AvgLatencyMS = int(s.latencyEMA)
	s.doc.UpdatedAt = time.Now().UTC()
}

func (s *CapabilityState) RecordOutcome(success bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if success {
		s.successes++
	} else {
		s.failures++
	}
	total := s.successes + s.failures
	if total == 0 {
		s.doc.Reliability = 1
	} else {
		s.doc.Reliability = float64(s.successes) / float64(total)
	}
	s.doc.UpdatedAt = time.Now().UTC()
}

func (s *CapabilityState) SetAnnounceAddrs(addrs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.doc.AnnounceAddrs = append([]string(nil), addrs...)
	s.doc.UpdatedAt = time.Now().UTC()
}
