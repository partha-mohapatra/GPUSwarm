package p2p

import (
	"context"
	"fmt"

	"infermeshai/internal/model"
)

type MultiDiscovery struct {
	discoveries []Discovery
}

func NewMultiDiscovery(discoveries ...Discovery) *MultiDiscovery {
	out := make([]Discovery, 0, len(discoveries))
	for _, d := range discoveries {
		if d != nil {
			out = append(out, d)
		}
	}
	return &MultiDiscovery{discoveries: out}
}

func (m *MultiDiscovery) ListCapabilities(ctx context.Context) ([]model.CapabilityDocument, error) {
	merged := make(map[string]model.CapabilityDocument)
	var anyOK bool
	for _, d := range m.discoveries {
		caps, err := d.ListCapabilities(ctx)
		if err != nil {
			continue
		}
		anyOK = true
		for _, c := range caps {
			key := c.PeerID + "|" + c.GatewayURL
			merged[key] = c
		}
	}
	if !anyOK || len(merged) == 0 {
		return nil, fmt.Errorf("no peers discovered from any source")
	}
	out := make([]model.CapabilityDocument, 0, len(merged))
	for _, c := range merged {
		out = append(out, c)
	}
	return out, nil
}
