package p2p

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"infermeshai/internal/model"
)

var ErrNoPeersDiscovered = fmt.Errorf("no peers discovered")

type Discovery interface {
	ListCapabilities(ctx context.Context) ([]model.CapabilityDocument, error)
}

type HTTPDiscovery struct {
	gatewayURLs []string
	client      *http.Client
}

func NewHTTPDiscovery(gatewayURLs []string, timeout time.Duration) *HTTPDiscovery {
	clean := make([]string, 0, len(gatewayURLs))
	for _, u := range gatewayURLs {
		u = strings.TrimSpace(strings.TrimRight(u, "/"))
		if u == "" {
			continue
		}
		clean = append(clean, u)
	}
	return &HTTPDiscovery{
		gatewayURLs: clean,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (d *HTTPDiscovery) ListCapabilities(ctx context.Context) ([]model.CapabilityDocument, error) {
	type result struct {
		cap model.CapabilityDocument
		err error
	}
	resCh := make(chan result, len(d.gatewayURLs))
	var wg sync.WaitGroup

	for _, gateway := range d.gatewayURLs {
		wg.Add(1)
		go func(base string) {
			defer wg.Done()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/capabilities", nil)
			if err != nil {
				resCh <- result{err: err}
				return
			}
			resp, err := d.client.Do(req)
			if err != nil {
				resCh <- result{err: err}
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				resCh <- result{err: fmt.Errorf("%s status %d", base, resp.StatusCode)}
				return
			}
			var cap model.CapabilityDocument
			if err := json.NewDecoder(resp.Body).Decode(&cap); err != nil {
				resCh <- result{err: err}
				return
			}
			if cap.GatewayURL == "" {
				cap.GatewayURL = base
			}
			resCh <- result{cap: cap}
		}(gateway)
	}

	wg.Wait()
	close(resCh)

	caps := make([]model.CapabilityDocument, 0, len(d.gatewayURLs))
	for r := range resCh {
		if r.err != nil {
			continue
		}
		caps = append(caps, r.cap)
	}
	if len(caps) == 0 {
		return nil, ErrNoPeersDiscovered
	}
	return caps, nil
}
