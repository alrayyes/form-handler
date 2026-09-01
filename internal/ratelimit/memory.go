// SPDX-License-Identifier: GPL-3.0-or-later

// Package ratelimit counts submissions per caller.
//
// It was an unexported type inside the HTTP handler, which meant the only way
// to test what happens at the limit was to drive HTTP, and the only way to
// count across two instances would have been to edit the handler. Out here it
// is neither: the handler names what it needs, and this is one thing that
// answers to it.
package ratelimit

import (
	"sync"
	"time"
)

// Memory is a fixed window per caller. Deliberately in memory and deliberately
// simple: this service has one instance and one job, and a shared store would
// be more moving parts than the problem deserves. Restarting forgives everyone,
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
// limit of zero or less turns it off, which is how a form says it does not want
// one.
func New(limit int, period time.Duration) *Memory {
	return &Memory{seen: make(map[string]*window), limit: limit, period: period}
}

// Allow reports whether this caller may submit, and counts the submission when
// it says yes. The key is whatever the caller is identified by; this package
// does not care which.
func (m *Memory) Allow(key string) bool {
	if m.limit <= 0 {
		return true
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	// Drop expired entries while we hold the lock. Without this the map grows
	// by one key per unique address, forever.
	for k, w := range m.seen {
		if now.After(w.reset) {
			delete(m.seen, k)
		}
	}

	w, ok := m.seen[key]
	if !ok || now.After(w.reset) {
		m.seen[key] = &window{count: 1, reset: now.Add(m.period)}

		return true
	}
	if w.count >= m.limit {
		return false
	}
	w.count++

	return true
}
