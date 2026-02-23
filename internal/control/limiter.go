package control

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrRateLimited = errors.New("rate limited")
var ErrTooManyInFlight = errors.New("too many in-flight requests")

type Limiter struct {
	mu            sync.Mutex
	inFlight      int
	maxInFlight   int
	tokens        float64
	ratePerSecond float64
	burst         float64
	lastRefill    time.Time
}

func NewLimiter(maxInFlight int, ratePerSecond float64, burst int) *Limiter {
	now := time.Now()
	return &Limiter{
		maxInFlight:   maxInFlight,
		ratePerSecond: ratePerSecond,
		burst:         float64(burst),
		tokens:        float64(burst),
		lastRefill:    now,
	}
}

func (l *Limiter) Acquire(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	if l.inFlight >= l.maxInFlight {
		return ErrTooManyInFlight
	}
	l.refillLocked(time.Now())
	if l.tokens < 1 {
		return ErrRateLimited
	}
	l.tokens -= 1
	l.inFlight++
	return nil
}

func (l *Limiter) Release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight > 0 {
		l.inFlight--
	}
}

func (l *Limiter) refillLocked(now time.Time) {
	elapsed := now.Sub(l.lastRefill).Seconds()
	if elapsed <= 0 {
		return
	}
	l.tokens += elapsed * l.ratePerSecond
	if l.tokens > l.burst {
		l.tokens = l.burst
	}
	l.lastRefill = now
}
