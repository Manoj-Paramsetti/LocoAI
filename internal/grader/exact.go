package grader

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Manoj-Paramsetti/LocoAI/internal/config"
	"github.com/Manoj-Paramsetti/LocoAI/internal/model"
)

type exact struct{ caseSensitive bool }

func newExact(spec config.GraderSpec, _ Deps) (Grader, error) {
	return &exact{caseSensitive: spec.CaseSensitive == nil || *spec.CaseSensitive}, nil
}

func (g *exact) Name() string { return "exact" }

func (g *exact) Validate(c model.Case) error {
	if _, ok := c.Expected.(string); !ok {
		return errors.New("exact grader needs a string expected value")
	}
	return nil
}

func (g *exact) Grade(_ context.Context, c model.Case, output string) (Result, error) {
	want, got := strings.TrimSpace(c.Expected.(string)), strings.TrimSpace(output)
	match := want == got
	if !g.caseSensitive {
		match = strings.EqualFold(want, got)
	}
	if match {
		return Result{1, "output matches expected"}, nil
	}
	return Result{0, fmt.Sprintf("expected %q, got %q", want, clip(got))}, nil
}

func clip(s string) string {
	const limit = 200
	if r := []rune(s); len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}
