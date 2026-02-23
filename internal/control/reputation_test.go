package control

import "testing"

func TestReputationTracker(t *testing.T) {
	r := NewReputationTracker()
	if r.Score("p") != 1.0 {
		t.Fatalf("expected default score 1")
	}
	r.Penalize("p", 0.4)
	if r.Score("p") >= 1.0 {
		t.Fatalf("expected reduced score")
	}
	r.Reward("p", 0.2)
	if r.Score("p") <= 0.5 {
		t.Fatalf("expected recovered score")
	}
}
