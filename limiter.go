package main

import (
	"sync"
	"time"
)

// limiter is a fixed-window per-client request counter. It is intentionally
// simple and in-memory: it blunts scripted abuse of a single instance, but it
// is not a distributed quota. Deployments running several replicas behind a
// load balancer should also rate limit at the edge.
type limiter struct {
	mu      sync.Mutex
	max     int
	window  time.Duration
	buckets map[string]*bucket
	swept   time.Time
}

type bucket struct {
	count int
	reset time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{
		max:     max,
		window:  window,
		buckets: make(map[string]*bucket),
		swept:   time.Now(),
	}
}

func (l *limiter) allow(key string) bool {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	// Drop expired buckets occasionally so a stream of unique clients cannot
	// grow the map without bound.
	if now.Sub(l.swept) > l.window {
		for k, b := range l.buckets {
			if now.After(b.reset) {
				delete(l.buckets, k)
			}
		}
		l.swept = now
	}

	b, ok := l.buckets[key]
	if !ok || now.After(b.reset) {
		l.buckets[key] = &bucket{count: 1, reset: now.Add(l.window)}
		return true
	}
	if b.count >= l.max {
		return false
	}
	b.count++
	return true
}
