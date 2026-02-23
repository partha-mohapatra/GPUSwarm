package control

import "sync"

type ReputationTracker struct {
	mu     sync.Mutex
	scores map[string]float64
	floor  float64
	ceil   float64
}

func NewReputationTracker() *ReputationTracker {
	return &ReputationTracker{
		scores: make(map[string]float64),
		floor:  0.0,
		ceil:   1.0,
	}
}

func (r *ReputationTracker) Score(peerID string) float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.scores[peerID]
	if !ok {
		return 1.0
	}
	return s
}

func (r *ReputationTracker) Reward(peerID string, delta float64) {
	r.adjust(peerID, delta)
}

func (r *ReputationTracker) Penalize(peerID string, delta float64) {
	r.adjust(peerID, -delta)
}

func (r *ReputationTracker) adjust(peerID string, delta float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, ok := r.scores[peerID]
	if !ok {
		cur = 1.0
	}
	cur += delta
	if cur < r.floor {
		cur = r.floor
	}
	if cur > r.ceil {
		cur = r.ceil
	}
	r.scores[peerID] = cur
}
