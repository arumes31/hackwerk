package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"example.invalid/hackplan/internal/auth"
	"example.invalid/hackplan/internal/geocode"
)

type geocodingSearcherStub struct {
	query   string
	results []geocode.Result
	err     error
}

func (stub *geocodingSearcherStub) Search(_ context.Context, query string) ([]geocode.Result, error) {
	stub.query = query
	return stub.results, stub.err
}

func TestGeocodingSearchReturnsBoundedCandidates(t *testing.T) {
	searcher := &geocodingSearcherStub{results: []geocode.Result{{
		Label: "Waldstraße 9, Unterneukirchen", Latitude: 46.72, Longitude: 15.56,
		Bounds: [4]float64{46.71, 46.73, 15.55, 15.57},
	}}}
	handler := geocodingSearch(searcher, newConfirmationRateLimiter(30, nil), slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	response := httptest.NewRecorder()
	request := geocodingRequest(t, " Waldstraße 9, Unterneukirchen ", auth.RoleDriver)

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if searcher.query != " Waldstraße 9, Unterneukirchen " {
		t.Fatalf("search query = %q", searcher.query)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
	var body struct {
		Results []geocode.Result `json:"results"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Results) != 1 || body.Results[0].Label != "Waldstraße 9, Unterneukirchen" {
		t.Fatalf("results = %#v", body.Results)
	}
}

func TestGeocodingSearchRejectsForbiddenActor(t *testing.T) {
	searcher := &geocodingSearcherStub{}
	handler := geocodingSearch(searcher, newConfirmationRateLimiter(30, nil), slog.Default())
	response := httptest.NewRecorder()
	request := geocodingRequest(t, "geheime adresse canary", auth.Role("unknown"))

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden || searcher.query != "" {
		t.Fatalf("status = %d, query = %q, body = %s", response.Code, searcher.query, response.Body.String())
	}
}

func TestGeocodingSearchRateLimitsBeforeProviderCall(t *testing.T) {
	searcher := &geocodingSearcherStub{}
	handler := geocodingSearch(searcher, newConfirmationRateLimiter(1, nil), slog.Default())
	handler.ServeHTTP(httptest.NewRecorder(), geocodingRequest(t, "erste adresse", auth.RoleAdmin))
	searcher.query = ""
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, geocodingRequest(t, "zweite adresse", auth.RoleAdmin))

	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "60" || searcher.query != "" {
		t.Fatalf("status = %d, retry = %q, query = %q", response.Code, response.Header().Get("Retry-After"), searcher.query)
	}
}

func TestGeocodingSearchDoesNotLogAddressOrProviderDetails(t *testing.T) {
	const canary = "Privatweg-Adresscanary-4711"
	searcher := &geocodingSearcherStub{err: errors.New("provider response contains " + canary)}
	var logs bytes.Buffer
	handler := geocodingSearch(searcher, newConfirmationRateLimiter(30, nil), slog.New(slog.NewTextHandler(&logs, nil)))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, geocodingRequest(t, canary, auth.RoleDriver))

	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(logs.String(), canary) || strings.Contains(response.Body.String(), canary) {
		t.Fatalf("address/provider details leaked: log=%q body=%q", logs.String(), response.Body.String())
	}
	if !strings.Contains(logs.String(), "geocoding_unavailable") {
		t.Fatalf("stable error code missing from log: %q", logs.String())
	}
}

func geocodingRequest(t *testing.T, query string, role auth.Role) *http.Request {
	t.Helper()
	body := url.Values{"query": {query}}.Encode()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.test/api/v1/geocoding/search", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	return request.WithContext(context.WithValue(request.Context(), sessionContextKey{}, auth.Session{Actor: auth.Actor{UserID: "user-id", Role: role}}))
}
