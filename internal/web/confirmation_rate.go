package web

import (
	"crypto/sha256"
	"net"
	"strings"
	"sync"
	"time"
)

type confirmationRateWindow struct {
	started time.Time
	count   int
}

type confirmationRateLimiter struct {
	mu      sync.Mutex
	windows map[[sha256.Size]byte]confirmationRateWindow
	limit   int
	now     func() time.Time
}

func newConfirmationRateLimiter(limit int, now func() time.Time) *confirmationRateLimiter {
	if limit < 1 {
		limit = 30
	}
	if now == nil {
		now = time.Now
	}
	return &confirmationRateLimiter{windows: make(map[[sha256.Size]byte]confirmationRateWindow), limit: limit, now: now}
}

func (limiter *confirmationRateLimiter) Allow(remoteAddress string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddress))
	if err != nil {
		host = strings.TrimSpace(remoteAddress)
	}
	key := sha256.Sum256([]byte(host))
	now := limiter.now().UTC()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	window := limiter.windows[key]
	if window.started.IsZero() || now.Sub(window.started) >= time.Minute {
		window = confirmationRateWindow{started: now}
	}
	window.count++
	limiter.windows[key] = window
	if len(limiter.windows) > 4096 {
		for existingKey, existingWindow := range limiter.windows {
			if now.Sub(existingWindow.started) >= time.Minute {
				delete(limiter.windows, existingKey)
			}
		}
	}
	return window.count <= limiter.limit
}
