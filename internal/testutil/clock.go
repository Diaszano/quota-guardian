package testutil

import (
	"sync"
	"time"

	"github.com/Diaszano/quota-guardian/internal/ports"
)

type ManualClock struct {
	mu      sync.Mutex
	now     time.Time
	timers  map[*manualTimer]struct{}
	created chan struct{}
}

type manualTimer struct {
	clock   *ManualClock
	due     time.Time
	c       chan time.Time
	stopped bool
	fired   bool
}

func NewManualClock(now time.Time) *ManualClock {
	return &ManualClock{now: now, timers: make(map[*manualTimer]struct{}), created: make(chan struct{}, 64)}
}

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) NewTimer(d time.Duration) ports.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &manualTimer{clock: c, due: c.now.Add(d), c: make(chan time.Time, 1)}
	select {
	case c.created <- struct{}{}:
	default:
	}
	if d <= 0 {
		t.fired = true
		t.c <- c.now
	} else {
		c.timers[t] = struct{}{}
	}
	return t
}

func (c *ManualClock) TimerCreated() <-chan struct{} { return c.created }

func (c *ManualClock) PendingTimers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

func (c *ManualClock) Advance(d time.Duration) { c.Set(c.Now().Add(d)) }

func (c *ManualClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
	for t := range c.timers {
		if !now.Before(t.due) {
			t.fired = true
			t.c <- now
			delete(c.timers, t)
		}
	}
}

func (t *manualTimer) C() <-chan time.Time { return t.c }

func (t *manualTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	delete(t.clock.timers, t)
	return true
}
