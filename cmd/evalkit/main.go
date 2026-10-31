// Command evalkit runs eval suites against AI models.
//
// Exit codes: 0 passed, 1 gate failed, 2 could not run. CI can tell a worse
// model from a broken harness.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/Manoj-Paramsetti/LocoAI/internal/config"
	"github.com/Manoj-Paramsetti/LocoAI/internal/dataset"
	"github.com/Manoj-Paramsetti/LocoAI/internal/grader"
	"github.com/Manoj-Paramsetti/LocoAI/internal/model"
	"github.com/Manoj-Paramsetti/LocoAI/internal/provider"
	"github.com/Manoj-Paramsetti/LocoAI/internal/provider/anthropic"
	"github.com/Manoj-Paramsetti/LocoAI/internal/provider/mock"
	"github.com/Manoj-Paramsetti/LocoAI/internal/report"
	"github.com/Manoj-Paramsetti/LocoAI/internal/runner"
)

const usage = `evalkit: run eval suites against AI models

  evalkit run <suite.yaml> [--out runs] [--baseline run.json] [--model id] [--site dir]
  evalkit validate <suite.yaml>
  evalkit compare <base.json> <current.json>
  evalkit promote <run.json> <baseline.json>
  evalkit report --out site <run.json>...
`

var errGate = errors.New("gate failed")

func main() {
	if err := dispatch(os.Args[1:], os.Stdout); err != nil {
		if errors.Is(err, errGate) {
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
}

func dispatch(args []string, out io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return errors.New("no command given")
	}
	switch cmd, rest := args[0], args[1:]; cmd {
	case "run":
		return cmdRun(rest, out)
	case "validate":
		return cmdValidate(rest, out)
	case "compare":
		return cmdCompare(rest, out)
	case "promote":
		return cmdPromote(rest, out)
	case "report":
		return cmdReport(rest, out)
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// parse accepts flags before or after positional arguments.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return positional, nil
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
}

func newProvider(spec config.ProviderSpec) (provider.Provider, error) {
	switch spec.Name {
	case "anthropic":
		return anthropic.New(spec.Fallbacks), nil
	case "mock":
		return &mock.Provider{Responses: spec.Responses, Default: spec.Default}, nil
	}
	return nil, fmt.Errorf("unknown provider %q", spec.Name)
}

// prepared is a suite ready to execute.
type prepared struct {
	suite   *config.Suite
	cases   []model.Case
	client  *runner.Client
	graders []grader.Bound
}

func prepare(path string) (*prepared, error) {
	s, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	cases, err := dataset.Load(s.Path(s.Dataset))
	if err != nil {
		return nil, err
	}
	p, err := newProvider(s.Provider)
	if err != nil {
		return nil, err
	}
	client := runner.NewClient(p, s.Runner)
	graders, err := grader.Build(grader.Deps{
		Suite: s,
		NewCaller: func(spec *config.ProviderSpec) (grader.Caller, error) {
			if spec == nil {
				return client.Caller(), nil
			}
			jp, err := newProvider(*spec)
			if err != nil {
				return nil, err
			}
			return runner.NewClient(jp, s.Runner).Caller(), nil
		},
	})
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, c := range cases {
		for _, g := range graders {
			if err := g.Validate(c); err != nil {
				errs = append(errs, fmt.Errorf("case %q: %w", c.ID, err))
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return &prepared{s, cases, client, graders}, nil
}

func cmdValidate(args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: evalkit validate <suite.yaml>")
	}
	p, err := prepare(args[0])
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: ok (%d cases, %d graders)\n", p.suite.Name, len(p.cases), len(p.graders))
	return nil
}

func cmdRun(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	dir := fs.String("out", "runs", "directory for run results")
	baseline := fs.String("baseline", "", "run file to check for regressions against")
	modelID := fs.String("model", "", "override the suite's model")
	site := fs.String("site", "", "also render an HTML report here")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: evalkit run <suite.yaml> [flags]")
	}
	p, err := prepare(pos[0])
	if err != nil {
		return err
	}
	if *modelID != "" {
		p.suite.Model = *modelID
	}
	// Read the baseline first: a bad path should fail before any spend.
	var base *model.Run
	if *baseline != "" {
		if base, err = report.Load(*baseline); err != nil {
			return fmt.Errorf("baseline: %w", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	started := time.Now().UTC()
	results, err := runner.Execute(ctx, p.suite, p.cases, p.client, p.graders)
	if err != nil {
		return err
	}
	run := &model.Run{
		SchemaVersion: model.SchemaVersion,
		ID:            runID(started),
		Suite:         p.suite.Name,
		Provider:      p.suite.Provider.Name,
		Model:         p.suite.Model,
		Fingerprint:   p.suite.Hash,
		Commit:        os.Getenv("GITHUB_SHA"),
		Threshold:     *p.suite.Gate.Threshold,
		StartedAt:     started,
		FinishedAt:    time.Now().UTC(),
		Summary:       model.Summarize(results),
		Cases:         results,
	}
	path, err := report.Save(run, *dir)
	if err != nil {
		return err
	}
	printRun(out, run)
	fmt.Fprintf(out, "results: %s\n", path)

	var cmp *report.Comparison
	if base != nil {
		cmp = report.Compare(base, run)
		printComparison(out, cmp)
	}
	if *site != "" {
		if err := report.WriteSite([]*model.Run{run}, *site); err != nil {
			return err
		}
		fmt.Fprintf(out, "report: %s/index.html\n", *site)
	}
	failures := report.Gate(run, cmp, p.suite.Gate.RegressionTolerance)
	for _, f := range failures {
		fmt.Fprintln(out, "GATE FAILED:", f)
	}
	if len(failures) > 0 {
		return errGate
	}
	fmt.Fprintln(out, "gate passed")
	return nil
}

func runID(t time.Time) string {
	b := make([]byte, 3)
	rand.Read(b)
	return t.Format("20060102T150405Z") + "-" + hex.EncodeToString(b)
}

func printRun(out io.Writer, r *model.Run) {
	s := r.Summary
	for _, c := range r.Cases {
		if c.Passed {
			continue
		}
		reason := c.Error
		for _, g := range c.Scores {
			if reason == "" && !g.Passed {
				reason = g.Grader + ": " + g.Reason
			}
		}
		fmt.Fprintf(out, "  FAIL %s: %s\n", c.ID, reason)
	}
	fmt.Fprintf(out, "%s on %s: %d/%d passed (%.1f%%), %d errored, %d served by fallback\n",
		r.Suite, r.Model, s.Passed, s.Total, s.PassRate*100, s.Errored, s.FellBack)
}

func printComparison(out io.Writer, c *report.Comparison) {
	for _, w := range c.Warnings {
		fmt.Fprintln(out, "warning:", w)
	}
	fmt.Fprintf(out, "baseline: %.1f%% -> %.1f%% over %d comparable cases\n",
		c.BasePassRate*100, c.CurPassRate*100, c.Comparable)
	for _, g := range []struct {
		label string
		ids   []string
	}{{"regressed", c.Regressions}, {"fixed", c.Fixes}, {"added", c.Added}, {"removed", c.Removed}, {"changed", c.Changed}} {
		if len(g.ids) > 0 {
			fmt.Fprintf(out, "  %s: %v\n", g.label, g.ids)
		}
	}
}

func cmdCompare(args []string, out io.Writer) error {
	if len(args) != 2 {
		return errors.New("usage: evalkit compare <base.json> <current.json>")
	}
	base, err := report.Load(args[0])
	if err != nil {
		return err
	}
	cur, err := report.Load(args[1])
	if err != nil {
		return err
	}
	cmp := report.Compare(base, cur)
	printComparison(out, cmp)
	if len(cmp.Regressions) > 0 {
		return errGate
	}
	return nil
}

func cmdPromote(args []string, out io.Writer) error {
	if len(args) != 2 {
		return errors.New("usage: evalkit promote <run.json> <baseline.json>")
	}
	run, err := report.Load(args[0])
	if err != nil {
		return err
	}
	if err := report.WriteRun(run, args[1]); err != nil {
		return err
	}
	fmt.Fprintf(out, "baseline %s now at %.1f%%\n", args[1], run.Summary.PassRate*100)
	return nil
}

func cmdReport(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	dir := fs.String("out", "site", "output directory")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return errors.New("usage: evalkit report --out site <run.json>...")
	}
	runs := make([]*model.Run, 0, len(pos))
	for _, p := range pos {
		r, err := report.Load(p)
		if err != nil {
			return err
		}
		runs = append(runs, r)
	}
	if err := report.WriteSite(runs, *dir); err != nil {
		return err
	}
	fmt.Fprintf(out, "report: %s/index.html\n", *dir)
	return nil
}
