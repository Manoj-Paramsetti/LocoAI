package grader

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/Manoj-Paramsetti/LocoAI/internal/config"
	"github.com/Manoj-Paramsetti/LocoAI/internal/model"
)

// regex matches the suite-level pattern, or each case's expected value when
// the suite sets none. With negate, a match is a failure.
type regex struct {
	fixed      *regexp.Regexp
	negate     bool
	ignoreCase bool
}

func newRegex(spec config.GraderSpec, _ Deps) (Grader, error) {
	g := &regex{negate: spec.Negate, ignoreCase: spec.IgnoreCase}
	if spec.Pattern != "" {
		re, err := g.compile(spec.Pattern)
		if err != nil {
			return nil, err
		}
		g.fixed = re
	}
	return g, nil
}

func (g *regex) compile(p string) (*regexp.Regexp, error) {
	if g.ignoreCase {
		p = "(?i)" + p
	}
	return regexp.Compile(p)
}

func (g *regex) pattern(c model.Case) (*regexp.Regexp, error) {
	if g.fixed != nil {
		return g.fixed, nil
	}
	p, ok := c.Expected.(string)
	if !ok {
		return nil, errors.New("regex grader needs a pattern or a string expected value")
	}
	return g.compile(p)
}

func (g *regex) Name() string { return "regex" }

func (g *regex) Validate(c model.Case) error {
	_, err := g.pattern(c)
	return err
}

func (g *regex) Grade(_ context.Context, c model.Case, output string) (Result, error) {
	re, err := g.pattern(c)
	if err != nil {
		return Result{}, err
	}
	found := re.FindString(output)
	matched := re.MatchString(output)
	switch {
	case matched && g.negate:
		return Result{0, fmt.Sprintf("forbidden pattern %q matched %q", re, clip(found))}, nil
	case matched:
		return Result{1, fmt.Sprintf("pattern %q matched", re)}, nil
	case g.negate:
		return Result{1, fmt.Sprintf("forbidden pattern %q absent", re)}, nil
	}
	return Result{0, fmt.Sprintf("pattern %q did not match", re)}, nil
}
