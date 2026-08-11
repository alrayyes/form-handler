// SPDX-License-Identifier: GPL-3.0-or-later

// Package ratelimit counts submissions per caller.
//
// An adapter implementing usecase.RateLimiter. It was an unexported type inside
// the HTTP handler, which meant the only way to test what happens at the limit
// was to drive HTTP, and the only way to share a count between instances would
// have been to edit the handler. Out here it is neither.
package ratelimit

import (
	"sync"
	"time"
)

// Memory is a fixed window per client. Deliberately in memory and deliberately
// simple: this service has one instance and one job, and a shared store would be
// more moving parts than the problem deserves. Restarting forgives everyone,
// which is an acceptable trade for a contact form.
type Memory struct {
	mu     sync.Mutex
	seen   map[string]*window
	limit  int
	period time.Duration
}

type window struct {
	count int
	reset time.Time
}

// New builds a limiter allowing limit submissions per caller per period. A
// limit of zero or less turns it off.
func New(limit int, period time.Duration) *Memory {
	return &Memory{seen: make(map[string]*window), limit: limit, period: period}
}

// Allow reports whether this caller may submit again, and counts the attempt
// if so.
func (l *Memory) Allow(key string) bool {
	if l.limit <= 0 {
		return true
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	// Drop expired entries while we hold the lock. Without this the map grows
	// by one key per unique address, forever.
	for k, w := range l.seen {
		if now.After(w.reset) {
			delete(l.seen, k)
		}
	}

	w, ok := l.seen[key]
	if !ok || now.After(w.reset) {
		l.seen[key] = &window{count: 1, reset: now.Add(l.period)}
		return true
	}
	if w.count >= l.limit {
		return false
	}
	w.count++
	return true
}
