package control

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"infermeshai/internal/model"
)

var ErrTokensExceeded = errors.New("max tokens exceeded")
var ErrOutsideServingWindow = errors.New("outside serving window")
var ErrHostBusy = errors.New("host not idle")
var ErrInvalidPoW = errors.New("invalid proof of work")
var ErrPeerReputationLow = errors.New("peer reputation below threshold")

type ProviderPolicy struct {
	MaxTokensPerRequest int
	NightOnly           bool
	NightStartHour      int
	NightEndHour        int
	IdleOnly            bool
	MaxLoad1m           float64
	PoWDifficulty       int
	MinPeerReputation   float64
}

type LoadProbe interface {
	Load1m(ctx context.Context) (float64, error)
}

type ProcLoadProbe struct{}

func (ProcLoadProbe) Load1m(ctx context.Context) (float64, error) {
	_ = ctx
	if runtime.GOOS != "linux" {
		return 0, nil
	}
	f, err := os.Open("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	if !s.Scan() {
		return 0, errors.New("empty /proc/loadavg")
	}
	parts := strings.Fields(s.Text())
	if len(parts) < 1 {
		return 0, errors.New("invalid /proc/loadavg")
	}
	v, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, fmt.Errorf("parse loadavg: %w", err)
	}
	return v, nil
}

func (p ProviderPolicy) Validate(ctx context.Context, req model.ChatCompletionRequest, now time.Time, probe LoadProbe) error {
	if p.MaxTokensPerRequest > 0 && req.MaxTokens > p.MaxTokensPerRequest {
		return ErrTokensExceeded
	}
	if p.NightOnly && !withinWindow(now.Hour(), p.NightStartHour, p.NightEndHour) {
		return ErrOutsideServingWindow
	}
	if p.IdleOnly && p.MaxLoad1m > 0 && probe != nil {
		load, err := probe.Load1m(ctx)
		if err == nil && load > p.MaxLoad1m {
			return ErrHostBusy
		}
	}
	return nil
}

func withinWindow(hour int, start int, end int) bool {
	if start == end {
		return true
	}
	if start < end {
		return hour >= start && hour < end
	}
	return hour >= start || hour < end
}
