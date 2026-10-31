// Package mock is a scripted provider for tests and for CI without API keys.
package mock

import (
	"context"
	"fmt"

	"github.com/Manoj-Paramsetti/LocoAI/internal/provider"
)

// Provider answers from a table keyed by case id, then Default.
type Provider struct {
	Responses map[string]string
	Default   string
}

// Name implements provider.Provider.
func (p *Provider) Name() string { return "mock" }

// Complete implements provider.Provider.
func (p *Provider) Complete(_ context.Context, req provider.Request) (provider.Response, error) {
	text, ok := p.Responses[req.CaseID]
	if !ok {
		if p.Default == "" {
			return provider.Response{}, fmt.Errorf("mock: no response for case %q", req.CaseID)
		}
		text = p.Default
	}
	return provider.Response{Text: text, StopReason: "end_turn", ServedModel: req.Model}, nil
}
