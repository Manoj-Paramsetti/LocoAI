package report

import (
	"fmt"

	"github.com/Manoj-Paramsetti/LocoAI/internal/model"
)

// Comparison describes how a run moved against a baseline. Pass rates cover
// only cases present and unchanged in both runs, so adding hard cases does not
// read as a regression and removing them does not read as a fix.
type Comparison struct {
	Comparable   int      `json:"comparable"`
	BasePassRate float64  `json:"base_pass_rate"`
	CurPassRate  float64  `json:"cur_pass_rate"`
	Delta        float64  `json:"delta"`
	Regressions  []string `json:"regressions"`
	Fixes        []string `json:"fixes"`
	Added        []string `json:"added"`
	Removed      []string `json:"removed"`
	Changed      []string `json:"changed"`
	Warnings     []string `json:"warnings"`
}

// Compare diffs cur against base.
func Compare(base, cur *model.Run) *Comparison {
	c := &Comparison{}
	if base.Suite != cur.Suite {
		c.Warnings = append(c.Warnings, fmt.Sprintf("comparing different suites: %s and %s", base.Suite, cur.Suite))
	}
	if base.Model != cur.Model {
		c.Warnings = append(c.Warnings, fmt.Sprintf("comparing different models: %s and %s", base.Model, cur.Model))
	}
	baseCases := make(map[string]model.CaseResult, len(base.Cases))
	for _, r := range base.Cases {
		baseCases[r.ID] = r
	}
	var basePassed, curPassed int
	seen := make(map[string]bool, len(cur.Cases))
	for _, r := range cur.Cases {
		seen[r.ID] = true
		b, ok := baseCases[r.ID]
		switch {
		case !ok:
			c.Added = append(c.Added, r.ID)
			continue
		case b.Hash != r.Hash:
			c.Changed = append(c.Changed, r.ID)
			continue
		}
		c.Comparable++
		if b.Passed {
			basePassed++
		}
		if r.Passed {
			curPassed++
		}
		switch {
		case b.Passed && !r.Passed:
			c.Regressions = append(c.Regressions, r.ID)
		case !b.Passed && r.Passed:
			c.Fixes = append(c.Fixes, r.ID)
		}
	}
	for _, r := range base.Cases {
		if !seen[r.ID] {
			c.Removed = append(c.Removed, r.ID)
		}
	}
	if c.Comparable > 0 {
		c.BasePassRate = float64(basePassed) / float64(c.Comparable)
		c.CurPassRate = float64(curPassed) / float64(c.Comparable)
		c.Delta = c.CurPassRate - c.BasePassRate
	} else {
		c.Warnings = append(c.Warnings, "no comparable cases; regression check skipped")
	}
	return c
}

// Gate returns the reasons the run fails, empty when it passes. cmp may be nil.
func Gate(run *model.Run, cmp *Comparison, tolerance float64) []string {
	var failures []string
	if run.Summary.PassRate < run.Threshold {
		failures = append(failures, fmt.Sprintf("pass rate %.1f%% is below threshold %.1f%%",
			run.Summary.PassRate*100, run.Threshold*100))
	}
	if cmp != nil && cmp.Comparable > 0 && cmp.Delta < -tolerance-1e-9 {
		failures = append(failures, fmt.Sprintf("pass rate fell %.1f points against baseline (tolerance %.1f); regressed: %v",
			-cmp.Delta*100, tolerance*100, cmp.Regressions))
	}
	return failures
}
