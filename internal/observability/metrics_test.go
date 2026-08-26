package observability

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type metricsSource struct{}

func (metricsSource) Collect(context.Context) (Snapshot, error) {
	return Snapshot{WorkerHealthy: true, Notifications: []Count{{Kind: "sms", Status: "failed", Total: 2}}, Voice: []Count{{Status: "needs_review", Total: 1}}}, nil
}

func TestMetricsUseOnlyBoundedLabelsAndNoCanaries(t *testing.T) {
	registry := New(metricsSource{}, time.Second, "test", "commit", map[string]bool{"sms": true})
	registry.ObserveHTTP("/feeds/{calendarFeedToken}/calendar.ics", http.MethodGet, http.StatusOK, 10*time.Millisecond)
	registry.ObserveVoice("needs_review", 20*time.Millisecond)
	registry.ObservePlanning(30*time.Millisecond, 3, true)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://metrics.internal/metrics?phone=canary@example.test", nil)
	response := httptest.NewRecorder()
	registry.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	value := response.Body.String()
	for _, forbidden := range []string{"canary@example.test", "calendarFeedToken=", "phone="} {
		if strings.Contains(value, forbidden) {
			t.Fatalf("metrics leaked %q: %s", forbidden, value)
		}
	}
	for _, expected := range []string{"hackwerk_http_requests_total", "/feeds/{calendarFeedToken}/calendar.ics", "hackwerk_worker_healthy 1", "hackwerk_notifications", "hackwerk_planning_candidates_total 3"} {
		if !strings.Contains(value, expected) {
			t.Fatalf("metrics missing %q: %s", expected, value)
		}
	}
	var duplicate bytes.Buffer
	registry.write(&duplicate, Snapshot{})
}
