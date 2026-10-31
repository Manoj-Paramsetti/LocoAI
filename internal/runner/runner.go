// Package runner executes cases: concurrency, retries, rate limits, timeouts.
package runner

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/Manoj-Paramsetti/LocoAI/internal/config"
	"github.com/Manoj-Paramsetti/LocoAI/internal/grader"
	"github.com/Manoj-Paramsetti/LocoAI/internal/model"
	"github.com/Manoj-Paramsetti/LocoAI/internal/provider"
)

// Client wraps a provider with the suite's reliability policy. Concurrency
// slots are held per attempt, so a request waiting out a backoff does not
// starve the others.
type Client struct {
	provider provider.Provider
	spec     config.RunnerSpec
	slots    chan struct{}
	limiter  *limiter
	// Sleep is replaceable in tests.
	Sleep func(ctx context.Context, d time.Duration) error
}

// NewClient builds a Client.
func NewClient(p provider.Provider, spec config.RunnerSpec) *Client {
	return &Client{
		provider: p,
		spec:     spec,
		slots:    make(chan struct{}, spec.Concurrency),
		limiter:  newLimiter(spec.RequestsPerMinute),
		Sleep:    sleep,
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Complete sends req, retrying transient failures. It returns the number of
// attempts made alongside the outcome.
func (c *Client) Complete(ctx context.Context, req provider.Request) (provider.Response, int, error) {
	for attempt := 1; ; attempt++ {
		resp, err := c.attempt(ctx, req)
		if err == nil {
			return resp, attempt, nil
		}
		var retry *provider.RetryableError
		if !errors.As(err, &retry) || attempt > *c.spec.MaxRetries || ctx.Err() != nil {
			return provider.Response{}, attempt, err
		}
		delay := c.backoff(attempt)
		if retry.RetryAfter > delay {
			delay = retry.RetryAfter
		}
		if retry.RateLimit {
			// A 429 applies to the whole account, so every worker pauses.
			c.limiter.pause(delay)
		}
		if err := c.Sleep(ctx, delay); err != nil {
			return provider.Response{}, attempt, err
		}
	}
}

func (c *Client) attempt(ctx context.Context, req provider.Request) (provider.Response, error) {
	if err := c.limiter.wait(ctx, c.Sleep); err != nil {
		return provider.Response{}, err
	}
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	case <-ctx.Done():
		return provider.Response{}, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.spec.TimeoutSeconds*float64(time.Second)))
	defer cancel()
	return c.provider.Complete(ctx, req)
}

// backoff is exponential with full jitter on the upper half.
func (c *Client) backoff(attempt int) time.Duration {
	limit := c.spec.BackoffMaxSeconds
	d := c.spec.BackoffBaseSeconds
	for i := 1; i < attempt && d < limit; i++ {
		d *= 2
	}
	if d > limit {
		d = limit
	}
	return time.Duration((d/2 + rand.Float64()*d/2) * float64(time.Second))
}

// Caller adapts Complete for graders.
func (c *Client) Caller() grader.Caller {
	return func(ctx context.Context, req provider.Request) (provider.Response, error) {
		resp, _, err := c.Complete(ctx, req)
		return resp, err
	}
}

// Execute runs every case. Results keep dataset order. A FatalError stops the
// run: finishing would only record the same failure for every case.
func Execute(ctx context.Context, s *config.Suite, cases []model.Case, c *Client, graders []grader.Bound) ([]model.CaseResult, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	results := make([]model.CaseResult, len(cases))
	var wg sync.WaitGroup
	for i, tc := range cases {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := runCase(ctx, s, tc, c, graders)
			results[i] = r
			var fatal *provider.FatalError
			if errors.As(err, &fatal) {
				cancel(err)
			}
		}()
	}
	wg.Wait()
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	return results, nil
}

func runCase(ctx context.Context, s *config.Suite, tc model.Case, c *Client, graders []grader.Bound) (model.CaseResult, error) {
	r := model.CaseResult{ID: tc.ID, Hash: tc.Hash(), Input: tc.Input, Scores: []model.GraderScore{}}
	system := s.System
	if tc.System != "" {
		system = tc.System
	}
	start := time.Now()
	resp, attempts, err := c.Complete(ctx, provider.Request{
		CaseID: tc.ID, Model: s.Model, System: system, Prompt: tc.Input,
		MaxTokens: s.MaxTokens, Effort: s.Effort,
	})
	r.LatencyMS = time.Since(start).Milliseconds()
	r.Attempts = attempts
	if err != nil {
		r.Error = err.Error()
		return r, err
	}
	r.Output, r.StopReason, r.ServedModel, r.FellBack = resp.Text, resp.StopReason, resp.ServedModel, resp.FellBack
	r.InputTokens, r.OutputTokens = resp.InputTokens, resp.OutputTokens

	r.Passed = true
	var total float64
	for _, g := range graders {
		res, err := g.Grade(ctx, tc, resp.Text)
		if err != nil {
			r.Scores = append(r.Scores, model.GraderScore{Grader: g.Name(), Error: err.Error()})
			r.Error = fmt.Sprintf("%s: %v", g.Name(), err)
			r.Passed = false
			var fatal *provider.FatalError
			if errors.As(err, &fatal) {
				return r, err
			}
			continue
		}
		passed := res.Score >= g.PassAt
		r.Scores = append(r.Scores, model.GraderScore{Grader: g.Name(), Score: res.Score, Passed: passed, Reason: res.Reason})
		r.Passed = r.Passed && passed
		total += res.Score
	}
	r.Score = total / float64(len(graders))
	return r, nil
}
