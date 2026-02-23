package control

import "testing"

func TestPoWSolveAndValidate(t *testing.T) {
	nonce, ok := SolvePoW("peer-a", "llama3", 1, 100000)
	if !ok {
		t.Fatalf("expected solvable pow")
	}
	if !ValidatePoW("peer-a", nonce, "llama3", 1) {
		t.Fatalf("expected valid solved pow")
	}
	if ValidatePoW("peer-a", nonce+1, "llama3", 1) {
		t.Fatalf("expected invalid random nonce")
	}
}
