package web

import (
	"crypto/sha256"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	// rateLimiterSweepThreshold starts pruning expired windows once the map
	// holds this many clients; sweeps run at most once per sweep interval so a
	// flood cannot force a full scan under the lock on every request.
	rateLimiterSweepThreshold = 4096
	rateLimiterSweepInterval  = time.Second
	// rateLimiterMaxEntries is a hard memory cap. New clients are refused
	// while it is reached; already tracked clients keep their normal budget.
	rateLimiterMaxEntries = 65536
)

type confirmationRateWindow struct {
	started time.Time
	count   int
}

type confirmationRateLimiter struct {
	mu        sync.Mutex
	windows   map[[sha256.Size]byte]confirmationRateWindow
	limit     int
	now       func() time.Time
	lastSweep time.Time
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
	key := sha256.Sum256([]byte(clientRateKey(remoteAddress)))
	now := limiter.now().UTC()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	window, tracked := limiter.windows[key]
	if !tracked && len(limiter.windows) >= rateLimiterSweepThreshold && now.Sub(limiter.lastSweep) >= rateLimiterSweepInterval {
		limiter.lastSweep = now
		for existingKey, existingWindow := range limiter.windows {
			if now.Sub(existingWindow.started) >= time.Minute {
				delete(limiter.windows, existingKey)
			}
		}
	}
	if !tracked && len(limiter.windows) >= rateLimiterMaxEntries {
		return false
	}
	if window.started.IsZero() || now.Sub(window.started) >= time.Minute {
		window = confirmationRateWindow{started: now}
	}
	window.count++
	limiter.windows[key] = window
	return window.count <= limiter.limit
}

// clientRateKey normalizes a client address for rate limiting. IPv6 clients
// are grouped by their /64 prefix because a single subscriber usually controls
// the whole prefix and could otherwise rotate addresses for fresh budgets.
func clientRateKey(remoteAddress string) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddress))
	if err != nil {
		host = strings.TrimSpace(remoteAddress)
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return host
	}
	if ip.To4() != nil {
		return ip.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}
