package control

import (
	"context"
	"testing"
	"time"

	"infermeshai/internal/model"
)

type fakeProbe struct{ load float64 }

func (f fakeProbe) Load1m(ctx context.Context) (float64, error) {
	return f.load, nil
}

func TestPolicyMaxTokens(t *testing.T) {
	p := ProviderPolicy{MaxTokensPerRequest: 128}
	req := model.ChatCompletionRequest{Model: "m", MaxTokens: 256}
	if err := p.Validate(context.Background(), req, time.Now(), nil); err != ErrTokensExceeded {
		t.Fatalf("expected ErrTokensExceeded, got %v", err)
	}
}

func TestPolicyNightWindowWrap(t *testing.T) {
	p := ProviderPolicy{NightOnly: true, NightStartHour: 22, NightEndHour: 6}
	req := model.ChatCompletionRequest{Model: "m"}
	inside := time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC)
	if err := p.Validate(context.Background(), req, inside, nil); err != nil {
		t.Fatalf("expected allowed in night window, got %v", err)
	}
	outside := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if err := p.Validate(context.Background(), req, outside, nil); err != ErrOutsideServingWindow {
		t.Fatalf("expected ErrOutsideServingWindow, got %v", err)
	}
}

func TestPolicyIdleOnly(t *testing.T) {
	p := ProviderPolicy{IdleOnly: true, MaxLoad1m: 0.50}
	req := model.ChatCompletionRequest{Model: "m"}
	if err := p.Validate(context.Background(), req, time.Now(), fakeProbe{load: 0.9}); err != ErrHostBusy {
		t.Fatalf("expected ErrHostBusy, got %v", err)
	}
}
