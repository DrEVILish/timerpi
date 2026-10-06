package routes

// ratelimit.go — a fixed-window counter per key (client IP, usually), for
// the open endpoints anyone on the network can hit: audience device
// minting, screen registration, client error reports.

import (
	"sync"
	"time"
)

type windowLimiter struct {
	mu     sync.Mutex
	max    int           // requests per window per key
	window time.Duration //
	bound  int           // map size beyond which it resets (memory cap)
	m      map[string]windowCount
}

type windowCount struct {
	start time.Time
	n     int
}

// allow spends one request from key's budget.
func (l *windowLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil || len(l.m) > l.bound {
		l.m = map[string]windowCount{}
	}
	w := l.m[key]
	if now.Sub(w.start) > l.window {
		w = windowCount{start: now}
	}
	if w.n >= l.max {
		return false
	}
	w.n++
	l.m[key] = w
	return true
}

// setMax changes the budget and forgets spent counts (tests).
func (l *windowLimiter) setMax(n int) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	old := l.max
	l.max = n
	l.m = nil
	return old
}
