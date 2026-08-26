// Package observability exposes bounded, PII-free operational metrics.
package observability

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Count struct {
	Kind, Status string
	Total        int64
}

type Snapshot struct {
	DBMax, DBTotal, DBAcquired, DBIdle int32
	OutboxPending, OutboxAttempts      int64
	OutboxOldestSeconds                float64
	ActiveSessions                     int64
	PlanningRunsRecent                 int64
	PlanningCandidatesRecent           int64
	WorkerHeartbeat                    time.Time
	WorkerHealthy                      bool
	Notifications, Voice               []Count
}

type Source interface {
	Collect(context.Context) (Snapshot, error)
}

type counterKey struct{ route, method, status string }

// Registry stores only bounded route/status aggregates and no user-controlled labels.
type Registry struct {
	mu                 sync.RWMutex
	httpRequests       map[counterKey]uint64
	httpDurationSum    map[counterKey]float64
	planningRuns       uint64
	planningSeconds    float64
	planningCandidates uint64
	planningFallbacks  uint64
	voiceRequests      map[string]uint64
	voiceSeconds       map[string]float64
	source             Source
	timeout            time.Duration
	version, commit    string
	features           map[string]bool
}

func New(source Source, timeout time.Duration, version, commit string, features map[string]bool) *Registry {
	copyFeatures := make(map[string]bool, len(features))
	for name, enabled := range features {
		copyFeatures[name] = enabled
	}
	return &Registry{
		httpRequests: make(map[counterKey]uint64), httpDurationSum: make(map[counterKey]float64),
		voiceRequests: make(map[string]uint64), voiceSeconds: make(map[string]float64),
		source: source, timeout: timeout, version: version, commit: commit, features: copyFeatures,
	}
}

func (registry *Registry) ObserveHTTP(route, method string, status int, duration time.Duration) {
	key := counterKey{route: boundedRoute(route), method: boundedMethod(method), status: strconv.Itoa(status)}
	registry.mu.Lock()
	registry.httpRequests[key]++
	registry.httpDurationSum[key] += duration.Seconds()
	registry.mu.Unlock()
}

func (registry *Registry) ObservePlanning(duration time.Duration, candidates int, fallback bool) {
	registry.mu.Lock()
	registry.planningRuns++
	registry.planningSeconds += duration.Seconds()
	if candidates > 0 {
		registry.planningCandidates += uint64(candidates)
	}
	if fallback {
		registry.planningFallbacks++
	}
	registry.mu.Unlock()
}

func (registry *Registry) ObserveVoice(status string, duration time.Duration) {
	status = boundedStatus(status, []string{"needs_review", "failed", "rate_limited", "rejected"})
	registry.mu.Lock()
	registry.voiceRequests[status]++
	registry.voiceSeconds[status] += duration.Seconds()
	registry.mu.Unlock()
}

func (registry *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		ctx, cancel := context.WithTimeout(request.Context(), registry.timeout)
		defer cancel()
		snapshot := Snapshot{}
		if registry.source != nil {
			var err error
			snapshot, err = registry.source.Collect(ctx)
			if err != nil {
				http.Error(response, "# HackWerk metrics temporarily unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		registry.write(response, snapshot)
	})
}

func (registry *Registry) write(output io.Writer, snapshot Snapshot) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	_, _ = fmt.Fprintf(output, "hackwerk_build_info{version=%q,commit=%q} 1\n", metricQuote(registry.version), metricQuote(registry.commit))
	featureNames := make([]string, 0, len(registry.features))
	for name := range registry.features {
		featureNames = append(featureNames, name)
	}
	sort.Strings(featureNames)
	for _, name := range featureNames {
		_, _ = fmt.Fprintf(output, "hackwerk_feature_enabled{feature=%q} %d\n", name, boolInt(registry.features[name]))
	}
	keys := make([]counterKey, 0, len(registry.httpRequests))
	for key := range registry.httpRequests {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j]) })
	for _, key := range keys {
		labels := fmt.Sprintf("route=%q,method=%q,status=%q", metricQuote(key.route), key.method, key.status)
		_, _ = fmt.Fprintf(output, "hackwerk_http_requests_total{%s} %d\n", labels, registry.httpRequests[key])
		_, _ = fmt.Fprintf(output, "hackwerk_http_request_duration_seconds_sum{%s} %g\n", labels, registry.httpDurationSum[key])
	}
	_, _ = fmt.Fprintf(output, "hackwerk_db_connections{state=\"max\"} %d\nhackwerk_db_connections{state=\"total\"} %d\nhackwerk_db_connections{state=\"acquired\"} %d\nhackwerk_db_connections{state=\"idle\"} %d\n", snapshot.DBMax, snapshot.DBTotal, snapshot.DBAcquired, snapshot.DBIdle)
	_, _ = fmt.Fprintf(output, "hackwerk_outbox_pending %d\nhackwerk_outbox_oldest_seconds %g\nhackwerk_outbox_attempts_total %d\nhackwerk_sessions_active %d\n", snapshot.OutboxPending, snapshot.OutboxOldestSeconds, snapshot.OutboxAttempts, snapshot.ActiveSessions)
	_, _ = fmt.Fprintf(output, "hackwerk_worker_healthy %d\nhackwerk_worker_last_heartbeat_timestamp_seconds %d\n", boolInt(snapshot.WorkerHealthy), unixOrZero(snapshot.WorkerHeartbeat))
	_, _ = fmt.Fprintf(output, "hackwerk_planning_runs_total %d\nhackwerk_planning_duration_seconds_sum %g\nhackwerk_planning_candidates_total %d\nhackwerk_planning_fallbacks_total %d\nhackwerk_planning_runs_recent %d\nhackwerk_planning_candidates_recent %d\n", registry.planningRuns, registry.planningSeconds, registry.planningCandidates, registry.planningFallbacks, snapshot.PlanningRunsRecent, snapshot.PlanningCandidatesRecent)
	for status, count := range registry.voiceRequests {
		_, _ = fmt.Fprintf(output, "hackwerk_voice_requests_total{status=%q} %d\nhackwerk_voice_duration_seconds_sum{status=%q} %g\n", status, count, status, registry.voiceSeconds[status])
	}
	for _, count := range snapshot.Notifications {
		_, _ = fmt.Fprintf(output, "hackwerk_notifications{channel=%q,status=%q} %d\n", metricQuote(count.Kind), metricQuote(count.Status), count.Total)
	}
	for _, count := range snapshot.Voice {
		_, _ = fmt.Fprintf(output, "hackwerk_voice_drafts{status=%q} %d\n", metricQuote(count.Status), count.Total)
	}
}

func boundedRoute(route string) string {
	if route == "" || len(route) > 160 || strings.ContainsAny(route, "\r\n") {
		return "unmatched"
	}
	return route
}
func boundedMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions:
		return method
	}
	return "OTHER"
}
func boundedStatus(status string, allowed []string) string {
	for _, value := range allowed {
		if status == value {
			return status
		}
	}
	return "other"
}
func metricQuote(value string) string {
	return strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\"", "\\\"").Replace(value)
}
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
func unixOrZero(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().Unix()
}
