package web

import (
	"testing"
	"time"
)

func TestConfirmationRateLimiterHashesAddressAndResets(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	limiter := newConfirmationRateLimiter(2, func() time.Time { return now })
	if !limiter.Allow("192.0.2.1:1234") || !limiter.Allow("192.0.2.1:9876") || limiter.Allow("192.0.2.1:1111") {
		t.Fatal("rate limit did not apply per source address")
	}
	now = now.Add(time.Minute)
	if !limiter.Allow("192.0.2.1:1234") {
		t.Fatal("rate limit did not reset")
	}
}
