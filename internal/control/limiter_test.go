package control

import (
	"context"
	"testing"
	"time"
)

func TestLimiterRejectsAboveInFlight(t *testing.T) {
	l := NewLimiter(1, 100, 1)
	if err := l.Acquire(context.Background()); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if err := l.Acquire(context.Background()); err != ErrTooManyInFlight {
		t.Fatalf("expected ErrTooManyInFlight, got %v", err)
	}
	l.Release()
}

func TestLimiterRefills(t *testing.T) {
	l := NewLimiter(2, 1, 1)
	if err := l.Acquire(context.Background()); err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	l.Release()
	if err := l.Acquire(context.Background()); err != ErrRateLimited {
		t.Fatalf("expected rate limit, got %v", err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := l.Acquire(context.Background()); err != nil {
		t.Fatalf("expected token refill, got %v", err)
	}
}
