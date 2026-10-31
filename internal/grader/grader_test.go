package grader

import (
	"context"
	"strings"
	"testing"

	"github.com/Manoj-Paramsetti/LocoAI/internal/config"
	"github.com/Manoj-Paramsetti/LocoAI/internal/model"
	"github.com/Manoj-Paramsetti/LocoAI/internal/provider"
)

func grade(t *testing.T, spec config.GraderSpec, deps Deps, c model.Case, output string) float64 {
	t.Helper()
	g, err := Registry[spec.Type](spec, deps)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Validate(c); err != nil {
		t.Fatal(err)
	}
	r, err := g.Grade(context.Background(), c, output)
	if err != nil {
		t.Fatal(err)
	}
	return r.Score
}

func TestBuiltins(t *testing.T) {
	off := false
	schema := map[string]any{"type": "object", "required": []any{"a"}}
	tests := []struct {
		name   string
		spec   config.GraderSpec
		c      model.Case
		output string
		want   float64
	}{
		{"exact trims", config.GraderSpec{Type: "exact"}, model.Case{Expected: "Paris"}, " Paris\n", 1},
		{"exact is case sensitive", config.GraderSpec{Type: "exact"}, model.Case{Expected: "Paris"}, "paris", 0},
		{"exact can ignore case", config.GraderSpec{Type: "exact", CaseSensitive: &off}, model.Case{Expected: "Paris"}, "paris", 1},
		{"regex from case", config.GraderSpec{Type: "regex"}, model.Case{Expected: `v\d`}, "v1", 1},
		{"regex miss", config.GraderSpec{Type: "regex", Pattern: "x"}, model.Case{}, "y", 0},
		{"regex negate hit", config.GraderSpec{Type: "regex", Pattern: "x", Negate: true}, model.Case{}, "x", 0},
		{"regex negate miss", config.GraderSpec{Type: "regex", Pattern: "x", Negate: true}, model.Case{}, "", 1},
		{"regex ignore case", config.GraderSpec{Type: "regex", Pattern: "x", IgnoreCase: true}, model.Case{}, "X", 1},
		{"schema ok", config.GraderSpec{Type: "json_schema", Schema: schema}, model.Case{}, `{"a":1}`, 1},
		{"schema violation", config.GraderSpec{Type: "json_schema", Schema: schema}, model.Case{}, `{}`, 0},
		{"schema not json", config.GraderSpec{Type: "json_schema", Schema: schema}, model.Case{}, "nope", 0},
		{"schema from case", config.GraderSpec{Type: "json_schema"}, model.Case{Expected: map[string]any{"type": "array"}}, "[]", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := grade(t, tt.spec, Deps{}, tt.c, tt.output); got != tt.want {
				t.Fatalf("score = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateRejectsUnusableCases(t *testing.T) {
	for _, tt := range []struct {
		spec config.GraderSpec
		c    model.Case
	}{
		{config.GraderSpec{Type: "exact"}, model.Case{Rubric: "r"}},
		{config.GraderSpec{Type: "regex"}, model.Case{Expected: "("}},
		{config.GraderSpec{Type: "json_schema"}, model.Case{Expected: "text"}},
		{config.GraderSpec{Type: "llm_judge"}, model.Case{Expected: "text"}},
	} {
		deps := judgeDeps(provider.Response{}, nil)
		g, err := Registry[tt.spec.Type](tt.spec, deps)
		if err != nil {
			t.Fatal(err)
		}
		if g.Validate(tt.c) == nil {
			t.Errorf("%s: want validation error", tt.spec.Type)
		}
	}
}

func judgeDeps(resp provider.Response, seen *provider.Request) Deps {
	return Deps{
		Suite: &config.Suite{Model: "m"},
		NewCaller: func(*config.ProviderSpec) (Caller, error) {
			return func(_ context.Context, r provider.Request) (provider.Response, error) {
				if seen != nil {
					*seen = r
				}
				return resp, nil
			}, nil
		},
	}
}

func TestJudgeScoresAndFencesUntrustedText(t *testing.T) {
	var seen provider.Request
	c := model.Case{Input: "in", Rubric: "be nice"}
	deps := judgeDeps(provider.Response{Text: `{"reasoning":"fine","score":0.8}`}, &seen)
	if got := grade(t, config.GraderSpec{Type: "llm_judge"}, deps, c, "out"); got != 0.8 {
		t.Fatalf("score = %v", got)
	}
	if !strings.Contains(seen.Prompt, "<response>\nout\n</response>") || seen.JSONSchema == nil || seen.Model != "m" {
		t.Fatalf("bad request: %+v", seen)
	}
}

func TestJudgeFailuresAreErrorsNotScores(t *testing.T) {
	for name, resp := range map[string]provider.Response{
		"refusal":      {StopReason: "refusal"},
		"invalid json": {Text: "nope"},
		"out of range": {Text: `{"reasoning":"x","score":7}`},
		"no score":     {Text: `{"reasoning":"x"}`},
	} {
		g, err := newJudge(config.GraderSpec{}, judgeDeps(resp, nil))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := g.Grade(context.Background(), model.Case{Rubric: "r"}, "o"); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestBuildPassMarks(t *testing.T) {
	half := 0.5
	got, err := Build(judgeDeps2(config.GraderSpec{Type: "exact"}, config.GraderSpec{Type: "llm_judge"}, config.GraderSpec{Type: "exact", PassAt: &half}))
	if err != nil {
		t.Fatal(err)
	}
	if got[0].PassAt != 1 || got[1].PassAt != 0.7 || got[2].PassAt != 0.5 {
		t.Fatalf("pass marks: %v %v %v", got[0].PassAt, got[1].PassAt, got[2].PassAt)
	}
	if _, err := Build(judgeDeps2(config.GraderSpec{Type: "nope"})); err == nil {
		t.Fatal("want unknown type error")
	}
}

func judgeDeps2(specs ...config.GraderSpec) Deps {
	d := judgeDeps(provider.Response{}, nil)
	d.Suite.Graders = specs
	return d
}
