// Package model holds the types shared by every stage of an eval run.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// SchemaVersion is bumped when the stored run format changes incompatibly.
const SchemaVersion = 1

// Case is one test case from a dataset.
type Case struct {
	ID       string   `json:"id" yaml:"id"`
	Input    string   `json:"input" yaml:"input"`
	Expected any      `json:"expected,omitempty" yaml:"expected,omitempty"`
	Rubric   string   `json:"rubric,omitempty" yaml:"rubric,omitempty"`
	System   string   `json:"system,omitempty" yaml:"system,omitempty"`
	Tags     []string `json:"tags,omitempty" yaml:"tags,omitempty"`
}

// Hash identifies the case content, so run comparison can tell an unchanged
// case from one that was edited under the same id. Tags do not affect grading
// and are left out.
func (c Case) Hash() string {
	b, _ := json.Marshal([]any{c.Input, c.Expected, c.Rubric, c.System})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// GraderScore is one grader's verdict on one case.
type GraderScore struct {
	Grader string  `json:"grader"`
	Score  float64 `json:"score"`
	Passed bool    `json:"passed"`
	Reason string  `json:"reason"`
	Error  string  `json:"error,omitempty"`
}

// CaseResult is the full record of one case in one run.
type CaseResult struct {
	ID           string        `json:"id"`
	Hash         string        `json:"hash"`
	Input        string        `json:"input"`
	Output       string        `json:"output"`
	StopReason   string        `json:"stop_reason,omitempty"`
	ServedModel  string        `json:"served_model,omitempty"`
	FellBack     bool          `json:"fell_back,omitempty"`
	Attempts     int           `json:"attempts"`
	LatencyMS    int64         `json:"latency_ms"`
	InputTokens  int64         `json:"input_tokens"`
	OutputTokens int64         `json:"output_tokens"`
	Scores       []GraderScore `json:"scores"`
	Score        float64       `json:"score"`
	Passed       bool          `json:"passed"`
	Error        string        `json:"error,omitempty"`
}

// Summary holds run-level aggregates.
type Summary struct {
	Total        int     `json:"total"`
	Passed       int     `json:"passed"`
	Failed       int     `json:"failed"`
	Errored      int     `json:"errored"`
	FellBack     int     `json:"fell_back"`
	PassRate     float64 `json:"pass_rate"`
	MeanScore    float64 `json:"mean_score"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
}

// Run is the stored result of executing a suite once.
type Run struct {
	SchemaVersion int          `json:"schema_version"`
	ID            string       `json:"id"`
	Suite         string       `json:"suite"`
	Provider      string       `json:"provider"`
	Model         string       `json:"model"`
	Fingerprint   string       `json:"fingerprint"`
	Commit        string       `json:"commit,omitempty"`
	Threshold     float64      `json:"threshold"`
	StartedAt     time.Time    `json:"started_at"`
	FinishedAt    time.Time    `json:"finished_at"`
	Summary       Summary      `json:"summary"`
	Cases         []CaseResult `json:"cases"`
}

// Summarize computes aggregates. Errored cases count as failures: excluding
// them would let an outage raise the pass rate.
func Summarize(cases []CaseResult) Summary {
	s := Summary{Total: len(cases)}
	var score float64
	for _, c := range cases {
		switch {
		case c.Error != "":
			s.Errored++
		case c.Passed:
			s.Passed++
		default:
			s.Failed++
		}
		if c.FellBack {
			s.FellBack++
		}
		score += c.Score
		s.InputTokens += c.InputTokens
		s.OutputTokens += c.OutputTokens
	}
	if s.Total > 0 {
		s.PassRate = float64(s.Passed) / float64(s.Total)
		s.MeanScore = score / float64(s.Total)
	}
	return s
}
