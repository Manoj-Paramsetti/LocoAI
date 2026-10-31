// Package provider defines the small interface model adapters implement.
package provider

import (
	"context"
	"fmt"
	"time"
)

// Request is one model call.
type Request struct {
	CaseID    string
	Model     string
	System    string
	Prompt    string
	MaxTokens int64
	Effort    string
	// JSONSchema, when set, constrains the response to this schema.
	JSONSchema map[string]any
}

// Response is a completed model call.
type Response struct {
	Text         string
	StopReason   string
	ServedModel  string
	FellBack     bool
	InputTokens  int64
	OutputTokens int64
}

// Provider sends one request to a model. Implementations must not retry; the
// runner owns retries so there is a single retry budget.
type Provider interface {
	Name() string
	Complete(ctx context.Context, req Request) (Response, error)
}

// RetryableError marks a transient failure. RetryAfter is zero when the
// server gave no hint.
type RetryableError struct {
	Err        error
	RetryAfter time.Duration
	RateLimit  bool
}

func (e *RetryableError) Error() string { return e.Err.Error() }
func (e *RetryableError) Unwrap() error { return e.Err }

// FatalError marks a failure every other case would hit too, such as bad
// credentials or an unknown model. It aborts the run.
type FatalError struct{ Err error }

func (e *FatalError) Error() string { return fmt.Sprintf("fatal provider error: %v", e.Err) }
func (e *FatalError) Unwrap() error { return e.Err }
