// Package grader holds the pluggable scoring functions.
package grader

import (
	"context"
	"fmt"

	"github.com/Manoj-Paramsetti/LocoAI/internal/config"
	"github.com/Manoj-Paramsetti/LocoAI/internal/model"
	"github.com/Manoj-Paramsetti/LocoAI/internal/provider"
)

// Result is a score in [0, 1] plus the reason for it.
type Result struct {
	Score  float64
	Reason string
}

// Grader scores a model output. An error means the grader could not reach a
// verdict, which is different from a low score.
type Grader interface {
	Name() string
	// Validate reports whether this grader can score the case, so unusable
	// datasets fail before any model call is paid for.
	Validate(c model.Case) error
	Grade(ctx context.Context, c model.Case, output string) (Result, error)
}

// Bound is a grader with its pass mark.
type Bound struct {
	Grader
	PassAt float64
}

// Caller sends a request through the runner's retries and rate limits.
type Caller func(ctx context.Context, req provider.Request) (provider.Response, error)

// Deps supplies what graders need from the outside.
type Deps struct {
	Suite *config.Suite
	// NewCaller returns a Caller for spec, or the suite's own when spec is nil.
	NewCaller func(spec *config.ProviderSpec) (Caller, error)
}

// Registry maps a grader type to its constructor. Add to it to plug in a grader.
var Registry = map[string]func(config.GraderSpec, Deps) (Grader, error){
	"exact":       newExact,
	"regex":       newRegex,
	"json_schema": newJSONSchema,
	"llm_judge":   newJudge,
}

// Build constructs the suite's graders.
func Build(deps Deps) ([]Bound, error) {
	var out []Bound
	for i, spec := range deps.Suite.Graders {
		ctor, ok := Registry[spec.Type]
		if !ok {
			return nil, fmt.Errorf("grader %d: unknown type %q", i+1, spec.Type)
		}
		g, err := ctor(spec, deps)
		if err != nil {
			return nil, fmt.Errorf("grader %d (%s): %w", i+1, spec.Type, err)
		}
		passAt := 1.0
		if spec.Type == "llm_judge" {
			passAt = 0.7
		}
		if spec.PassAt != nil {
			passAt = *spec.PassAt
		}
		out = append(out, Bound{Grader: g, PassAt: passAt})
	}
	return out, nil
}
