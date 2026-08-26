package geocode

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestClientSearchUsesBoundedStaticProviderRequestAndCache(t *testing.T) {
	client, err := New(Config{
		SearchURL: "https://geocoder.example/search", CountryCodes: []string{"at"}, Timeout: time.Second,
		MaxResponseSize: 4096, MaxResults: 2, MinInterval: 0, CacheTTL: time.Hour, CacheEntries: 8, UserAgent: "HackWerk/test",
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client.httpClient.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		query := request.URL.Query()
		if request.URL.Host != "geocoder.example" || request.URL.Path != "/search" || query.Get("q") != "Waldstraße 9" ||
			query.Get("format") != "jsonv2" || query.Get("limit") != "2" || query.Get("countrycodes") != "at" ||
			request.Header.Get("User-Agent") != "HackWerk/test" {
			t.Fatalf("unexpected provider request %s headers=%v", request.URL.String(), request.Header)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(
			`[{"display_name":"Waldstraße 9, Österreich","lat":"48.2001","lon":"14.2002","boundingbox":["48.19","48.21","14.19","14.21"]}]`,
		))}, nil
	})
	for range 2 {
		results, searchErr := client.Search(context.Background(), "  Waldstraße   9 ")
		if searchErr != nil {
			t.Fatal(searchErr)
		}
		if len(results) != 1 || results[0].Label != "Waldstraße 9, Österreich" || results[0].Bounds[0] != 48.19 {
			t.Fatalf("results=%+v", results)
		}
	}
	if calls != 1 {
		t.Fatalf("provider calls=%d, want 1 cached call", calls)
	}
}

func TestClientSearchRejectsInvalidInputAndUntrustedResponses(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		contentType string
		body        string
		wantErr     error
		wantResults int
	}{
		{name: "short query", query: "ab", wantErr: ErrInvalidQuery},
		{name: "wrong content type", query: "Linz", contentType: "text/html", body: "<html>", wantErr: ErrUpstream},
		{name: "invalid coordinate skipped", query: "Linz", contentType: "application/json", body: `[{"display_name":"Ungültig","lat":"999","lon":"14"}]`, wantResults: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := New(Config{SearchURL: "https://geocoder.example/search", Timeout: time.Second, MaxResponseSize: 4096, MaxResults: 3, CacheTTL: time.Hour, CacheEntries: 8, UserAgent: "HackWerk/test"})
			if err != nil {
				t.Fatal(err)
			}
			client.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {test.contentType}}, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})
			results, searchErr := client.Search(t.Context(), test.query)
			if test.wantErr != nil && !errors.Is(searchErr, test.wantErr) {
				t.Fatalf("Search() error=%v, want %v", searchErr, test.wantErr)
			}
			if test.wantErr == nil && (searchErr != nil || len(results) != test.wantResults) {
				t.Fatalf("Search()=%+v, %v", results, searchErr)
			}
		})
	}
}
