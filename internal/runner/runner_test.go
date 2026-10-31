package runner

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Manoj-Paramsetti/LocoAI/internal/config"
	"github.com/Manoj-Paramsetti/LocoAI/internal/grader"
	"github.com/Manoj-Paramsetti/LocoAI/internal/model"
	"github.com/Manoj-Paramsetti/LocoAI/internal/provider"
)

type stub struct {
	fn func(n int64, req provider.Request) (provider.Response, error)
	n  atomic.Int64
}

func (s *stub) Name() string { return "stub" }
func (s *stub) Complete(ctx context.Context, r provider.Request) (provider.Response, error) {
	return s.fn(s.n.Add(1), r)
}

func spec(retries, concurrency int) config.RunnerSpec {
	return config.RunnerSpec{Concurrency: concurrency, MaxRetries: &retries, TimeoutSeconds: 5, BackoffBaseSeconds: 1, BackoffMaxSeconds: 8}
}

func client(p provider.Provider, s config.RunnerSpec) (*Client, *[]time.Duration) {
	c := NewClient(p, s)
	var mu sync.Mutex
	delays := &[]time.Duration{}
	c.Sleep = func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		*delays = append(*delays, d)
		return nil
	}
	return c, delays
}

func TestRetriesThenSucceeds(t *testing.T) {
	p := &stub{fn: func(n int64, _ provider.Request) (provider.Response, error) {
		if n < 3 {
			return provider.Response{}, &provider.RetryableError{Err: errors.New("503")}
		}
		return provider.Response{Text: "ok"}, nil
	}}
	c, delays := client(p, spec(5, 1))
	resp, attempts, err := c.Complete(context.Background(), provider.Request{})
	if err != nil || resp.Text != "ok" || attempts != 3 || len(*delays) != 2 {
		t.Fatalf("resp=%+v attempts=%d err=%v delays=%v", resp, attempts, err, *delays)
	}
}

func TestRetryAfterExtendsBackoff(t *testing.T) {
	p := &stub{fn: func(n int64, _ provider.Request) (provider.Response, error) {
		if n == 1 {
			return provider.Response{}, &provider.RetryableError{Err: errors.New("429"), RetryAfter: 30 * time.Second}
		}
		return provider.Response{}, nil
	}}
	c, delays := client(p, spec(1, 1))
	if _, _, err := c.Complete(context.Background(), provider.Request{}); err != nil {
		t.Fatal(err)
	}
	if (*delays)[0] != 30*time.Second {
		t.Fatalf("delay = %v", (*delays)[0])
	}
}

func TestRetryBudgetIsBounded(t *testing.T) {
	p := &stub{fn: func(int64, provider.Request) (provider.Response, error) {
		return provider.Response{}, &provider.RetryableError{Err: errors.New("503")}
	}}
	c, _ := client(p, spec(2, 1))
	_, attempts, err := c.Complete(context.Background(), provider.Request{})
	if err == nil || attempts != 3 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}

func TestPlainErrorsAreNotRetried(t *testing.T) {
	p := &stub{fn: func(int64, provider.Request) (provider.Response, error) {
		return provider.Response{}, errors.New("400")
	}}
	c, _ := client(p, spec(5, 1))
	if _, attempts, err := c.Complete(context.Background(), provider.Request{}); err == nil || attempts != 1 {
		t.Fatalf("attempts=%d err=%v", attempts, err)
	}
}

func TestBackoffDoublesAndCaps(t *testing.T) {
	c, _ := client(&stub{}, spec(0, 1))
	for attempt, top := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 9: 8 * time.Second} {
		if d := c.backoff(attempt); d < top/2 || d > top {
			t.Errorf("attempt %d: %v outside [%v, %v]", attempt, d, top/2, top)
		}
	}
}

func suite() *config.Suite {
	return &config.Suite{Model: "m", MaxTokens: 10, System: "suite system"}
}

type fixed struct{ score float64 }

func (f fixed) Name() string              { return "fixed" }
func (f fixed) Validate(model.Case) error { return nil }
func (f fixed) Grade(_ context.Context, _ model.Case, o string) (grader.Result, error) {
	if o == "grader-error" {
		return grader.Result{}, errors.New("no verdict")
	}
	return grader.Result{Score: f.score, Reason: "r"}, nil
}

func TestExecuteHonoursConcurrencyAndOrder(t *testing.T) {
	var active, peak atomic.Int64
	p := &stub{fn: func(_ int64, r provider.Request) (provider.Response, error) {
		now := active.Add(1)
		for {
			old := peak.Load()
			if now <= old || peak.CompareAndSwap(old, now) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		active.Add(-1)
		return provider.Response{Text: r.CaseID}, nil
	}}
	cases := make([]model.Case, 30)
	for i := range cases {
		cases[i].ID = string(rune('a' + i))
	}
	c, _ := client(p, spec(0, 3))
	got, err := Execute(context.Background(), suite(), cases, c, []grader.Bound{{Grader: fixed{1}, PassAt: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if peak.Load() > 3 {
		t.Fatalf("peak concurrency %d", peak.Load())
	}
	for i, r := range got {
		if r.ID != cases[i].ID || r.Output != cases[i].ID || !r.Passed {
			t.Fatalf("result %d out of order: %+v", i, r)
		}
	}
}

func TestExecuteRecordsCaseFailures(t *testing.T) {
	p := &stub{fn: func(_ int64, r provider.Request) (provider.Response, error) {
		switch r.CaseID {
		case "model-error":
			return provider.Response{}, errors.New("rejected")
		case "grader-error":
			return provider.Response{Text: "grader-error"}, nil
		}
		if r.System != "case system" && r.CaseID == "own-system" {
			t.Errorf("case system not applied: %q", r.System)
		}
		return provider.Response{Text: "fine"}, nil
	}}
	cases := []model.Case{{ID: "model-error"}, {ID: "grader-error"}, {ID: "own-system", System: "case system"}}
	c, _ := client(p, spec(0, 2))
	got, err := Execute(context.Background(), suite(), cases, c, []grader.Bound{{Grader: fixed{0.5}, PassAt: 0.9}})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Error == "" || got[0].Passed {
		t.Errorf("model error: %+v", got[0])
	}
	if got[1].Error == "" || got[1].Passed {
		t.Errorf("grader error: %+v", got[1])
	}
	if got[2].Error != "" || got[2].Passed || got[2].Score != 0.5 {
		t.Errorf("below pass mark: %+v", got[2])
	}
	if s := model.Summarize(got); s.Errored != 2 || s.Failed != 1 || s.PassRate != 0 {
		t.Errorf("summary: %+v", s)
	}
}

func TestExecuteAbortsOnFatal(t *testing.T) {
	p := &stub{fn: func(int64, provider.Request) (provider.Response, error) {
		return provider.Response{}, &provider.FatalError{Err: errors.New("401")}
	}}
	c, _ := client(p, spec(5, 1))
	_, err := Execute(context.Background(), suite(), []model.Case{{ID: "a"}, {ID: "b"}}, c, nil)
	var fatal *provider.FatalError
	if !errors.As(err, &fatal) {
		t.Fatalf("err = %v", err)
	}
}
