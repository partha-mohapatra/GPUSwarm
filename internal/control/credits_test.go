package control

import "testing"

func TestCreditLedger(t *testing.T) {
	l := NewCreditLedger(2)
	if !l.Spend(1) {
		t.Fatalf("expected spend success")
	}
	if l.Balance() != 1 {
		t.Fatalf("expected balance 1, got %d", l.Balance())
	}
	if !l.Spend(1) {
		t.Fatalf("expected second spend success")
	}
	if l.Spend(1) {
		t.Fatalf("expected third spend fail")
	}
	l.Earn(3)
	if l.Balance() != 3 {
		t.Fatalf("expected balance 3, got %d", l.Balance())
	}
}
