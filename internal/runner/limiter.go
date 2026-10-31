package runner

import (
	"context"
	"sync"
	"time"
)

// limiter spaces requests evenly and holds them all during a rate-limit pause.
type limiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func newLimiter(perMinute int) *limiter {
	l := &limiter{}
	if perMinute > 0 {
		l.interval = time.Minute / time.Duration(perMinute)
	}
	return l
}

func (l *limiter) wait(ctx context.Context, sleep func(context.Context, time.Duration) error) error {
	l.mu.Lock()
	now := time.Now()
	at := l.next
	if at.Before(now) {
		at = now
	}
	l.next = at.Add(l.interval)
	l.mu.Unlock()
	if d := at.Sub(now); d > 0 {
		return sleep(ctx, d)
	}
	return nil
}

func (l *limiter) pause(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if until := time.Now().Add(d); until.After(l.next) {
		l.next = until
	}
}
