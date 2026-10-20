package testkit

import (
	"context"
	"sync"
	"time"
)

type clockWaiter struct {
	due  time.Time
	done chan time.Time
}

type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*clockWaiter
}

func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start}
}

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *FakeClock) Advance(duration time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
	remaining := c.waiters[:0]
	for _, waiter := range c.waiters {
		if !waiter.due.After(c.now) {
			waiter.done <- c.now
			close(waiter.done)
			continue
		}
		remaining = append(remaining, waiter)
	}
	c.waiters = remaining
	return c.now
}

func (c *FakeClock) Set(now time.Time) {
	c.mu.Lock()
	delta := now.Sub(c.now)
	c.mu.Unlock()
	c.Advance(delta)
}

func (c *FakeClock) After(duration time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	done := make(chan time.Time, 1)
	if duration <= 0 {
		done <- c.now
		close(done)
		return done
	}
	c.waiters = append(c.waiters, &clockWaiter{due: c.now.Add(duration), done: done})
	return done
}

func (c *FakeClock) Sleep(ctx context.Context, duration time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.After(duration):
		return nil
	}
}
