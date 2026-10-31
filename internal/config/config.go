// Package config parses suite files.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// DefaultModel is used when a suite does not name a model.
const DefaultModel = "claude-opus-5-5"

// ProviderSpec selects and configures a model provider.
type ProviderSpec struct {
	Name string `yaml:"name"`
	// Fallbacks enables Anthropic server-side refusal fallbacks. Nil means on
	// for models that support them. Fallback use is recorded per case.
	Fallbacks *bool `yaml:"fallbacks"`
	// Responses and Default script the mock provider.
	Responses map[string]string `yaml:"responses"`
	Default   string            `yaml:"default"`
}

// GraderSpec configures one grader. Which fields apply depends on Type.
type GraderSpec struct {
	Type          string         `yaml:"type"`
	PassAt        *float64       `yaml:"pass_at"`
	Pattern       string         `yaml:"pattern"`
	Negate        bool           `yaml:"negate"`
	IgnoreCase    bool           `yaml:"ignore_case"`
	CaseSensitive *bool          `yaml:"case_sensitive"`
	Schema        map[string]any `yaml:"schema"`
	SchemaFile    string         `yaml:"schema_file"`
	Model         string         `yaml:"model"`
	Effort        string         `yaml:"effort"`
	Provider      *ProviderSpec  `yaml:"provider"`
}

// RunnerSpec tunes execution.
type RunnerSpec struct {
	Concurrency        int     `yaml:"concurrency"`
	MaxRetries         *int    `yaml:"max_retries"`
	TimeoutSeconds     float64 `yaml:"timeout_seconds"`
	RequestsPerMinute  int     `yaml:"requests_per_minute"`
	BackoffBaseSeconds float64 `yaml:"backoff_base_seconds"`
	BackoffMaxSeconds  float64 `yaml:"backoff_max_seconds"`
}

// GateSpec defines what fails the build.
type GateSpec struct {
	Threshold *float64 `yaml:"threshold"`
	// RegressionTolerance is the pass-rate drop allowed against the baseline.
	RegressionTolerance float64 `yaml:"regression_tolerance"`
}

// Suite is a parsed suite file.
type Suite struct {
	Name        string       `yaml:"name"`
	Description string       `yaml:"description"`
	Dataset     string       `yaml:"dataset"`
	Provider    ProviderSpec `yaml:"provider"`
	Model       string       `yaml:"model"`
	System      string       `yaml:"system"`
	MaxTokens   int64        `yaml:"max_tokens"`
	Effort      string       `yaml:"effort"`
	Graders     []GraderSpec `yaml:"graders"`
	Runner      RunnerSpec   `yaml:"runner"`
	Gate        GateSpec     `yaml:"gate"`

	// Dir is the suite file's directory; relative paths resolve against it.
	Dir string `yaml:"-"`
	// Hash covers the suite file's bytes.
	Hash string `yaml:"-"`
}

// Load parses, defaults and validates a suite file.
func Load(path string) (*Suite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Suite
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s.Dir = filepath.Dir(path)
	sum := sha256.Sum256(data)
	s.Hash = hex.EncodeToString(sum[:8])
	s.applyDefaults()
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

// Path resolves a suite-relative path.
func (s *Suite) Path(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(s.Dir, p)
}

func (s *Suite) applyDefaults() {
	if s.Provider.Name == "" {
		s.Provider.Name = "anthropic"
	}
	if s.Model == "" {
		s.Model = DefaultModel
	}
	if s.MaxTokens == 0 {
		s.MaxTokens = 16000
	}
	r := &s.Runner
	if r.Concurrency == 0 {
		r.Concurrency = 5
	}
	if r.MaxRetries == nil {
		n := 5
		r.MaxRetries = &n
	}
	if r.TimeoutSeconds == 0 {
		r.TimeoutSeconds = 120
	}
	if r.BackoffBaseSeconds == 0 {
		r.BackoffBaseSeconds = 1
	}
	if r.BackoffMaxSeconds == 0 {
		r.BackoffMaxSeconds = 60
	}
}

func (s *Suite) validate() error {
	var errs []error
	if s.Name == "" {
		errs = append(errs, errors.New("name is required"))
	}
	if s.Dataset == "" {
		errs = append(errs, errors.New("dataset is required"))
	}
	if len(s.Graders) == 0 {
		errs = append(errs, errors.New("at least one grader is required"))
	}
	if t := s.Gate.Threshold; t == nil {
		errs = append(errs, errors.New("gate.threshold is required"))
	} else if *t < 0 || *t > 1 {
		errs = append(errs, errors.New("gate.threshold must be between 0 and 1"))
	}
	if s.Gate.RegressionTolerance < 0 || s.Gate.RegressionTolerance > 1 {
		errs = append(errs, errors.New("gate.regression_tolerance must be between 0 and 1"))
	}
	if s.Runner.Concurrency < 1 {
		errs = append(errs, errors.New("runner.concurrency must be at least 1"))
	}
	if *s.Runner.MaxRetries < 0 {
		errs = append(errs, errors.New("runner.max_retries must not be negative"))
	}
	return errors.Join(errs...)
}
