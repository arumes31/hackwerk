package web

import (
	"crypto/sha256"
	"fmt"
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

func TestConfirmationRateLimiterGroupsIPv6ByPrefix(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	limiter := newConfirmationRateLimiter(2, func() time.Time { return now })
	if !limiter.Allow("2001:db8:1:2::1") || !limiter.Allow("[2001:db8:1:2:ffff::9]:443") || limiter.Allow("2001:db8:1:2:abcd::7") {
		t.Fatal("rate limit did not group one IPv6 /64")
	}
	if !limiter.Allow("2001:db8:1:3::1") {
		t.Fatal("a different IPv6 /64 must have its own budget")
	}
}

func TestConfirmationRateLimiterCapsTrackedClients(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	limiter := newConfirmationRateLimiter(5, func() time.Time { return now })
	for index := range rateLimiterMaxEntries {
		limiter.windows[sha256.Sum256([]byte(fmt.Sprintf("flood-%d", index)))] = confirmationRateWindow{started: now, count: 1}
	}
	if limiter.Allow("192.0.2.10") {
		t.Fatal("a new client was admitted beyond the hard cap")
	}
	if len(limiter.windows) != rateLimiterMaxEntries {
		t.Fatalf("tracked clients = %d", len(limiter.windows))
	}
	limiter.windows[sha256.Sum256([]byte("192.0.2.11"))] = confirmationRateWindow{started: now, count: 1}
	delete(limiter.windows, sha256.Sum256([]byte("flood-0")))
	if !limiter.Allow("192.0.2.11") {
		t.Fatal("an already tracked client lost its budget at the cap")
	}
	now = now.Add(time.Minute)
	if !limiter.Allow("192.0.2.12") || len(limiter.windows) != 1 {
		t.Fatalf("expired windows were not swept: %d tracked", len(limiter.windows))
	}
}

func TestClientRateKey(t *testing.T) {
	for input, want := range map[string]string{
		"192.0.2.1":            "192.0.2.1",
		"192.0.2.1:8080":       "192.0.2.1",
		"[2001:db8::1]:443":    "2001:db8::/64",
		"2001:db8:0:0:ffff::1": "2001:db8::/64",
		"::ffff:192.0.2.1":     "192.0.2.1",
		"not-an-address":       "not-an-address",
	} {
		if got := clientRateKey(input); got != want {
			t.Errorf("clientRateKey(%q) = %q, want %q", input, got, want)
		}
	}
}
