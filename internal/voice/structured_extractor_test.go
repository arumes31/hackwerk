package voice

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type structuredStub struct {
	fields Fields
	err    error
}

func (stub structuredStub) Extract(context.Context, string) (Fields, error) {
	return stub.fields, stub.err
}
func (structuredStub) Version() string { return "stub-v1" }

func TestHybridExtractorOnlyAddsSourceBackedLowConfidenceSuggestions(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Vienna")
	provider := structuredStub{fields: Fields{
		CompanyName: Field{Value: "Erfundene Firma", Source: "nicht im Text", Confidence: .99},
		Region:      Field{Value: "Innviertel", Source: "Region Innviertel", Confidence: .95},
		VolumeM3:    Field{Value: "999", Source: "80 Kubikmeter", Confidence: .99},
	}}
	fields, warnings, _ := (HybridExtractor{Rules: RuleExtractor{}, Provider: provider}).Extract(
		context.Background(), "Franz Huber, Waldweg 1, 80 Kubikmeter, drei Stunden Hackzeit, Region Innviertel", time.Now(), location,
	)
	if fields.CompanyName.Value != "" || fields.Region.Value != "Innviertel" || fields.Region.Confidence != .70 || len(fields.Region.Warnings) == 0 || fields.VolumeM3.Value != "80" || len(warnings) != 0 {
		t.Fatalf("fields/warnings=%#v/%v", fields, warnings)
	}
}

func TestHybridExtractorFallsBackToRules(t *testing.T) {
	location, _ := time.LoadLocation("Europe/Vienna")
	fields, warnings, _ := (HybridExtractor{Rules: RuleExtractor{}, Provider: structuredStub{err: ErrProvider}}).Extract(context.Background(), "Franz Huber, Waldweg 1, 80 m³, drei Stunden Hackzeit", time.Now(), location)
	if fields.VolumeM3.Value != "80" || len(warnings) == 0 {
		t.Fatalf("fields/warnings=%#v/%v", fields, warnings)
	}
}

func TestOpenAIStructuredProviderUsesStrictSchemaAndStoreFalse(t *testing.T) {
	modelFields := Fields{Region: Field{Value: "Innviertel", Source: "Innviertel", Confidence: .8, Warnings: []string{}}}
	for _, field := range fieldPointers(&modelFields) {
		if field.Warnings == nil {
			field.Warnings = []string{}
		}
	}
	encodedFields, _ := json.Marshal(modelFields)
	envelope, _ := json.Marshal(map[string]any{
		"output": []any{map[string]any{
			"type":    "message",
			"content": []any{map[string]any{"type": "output_text", "text": string(encodedFields)}},
		}},
	})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		data, _ := io.ReadAll(request.Body)
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Error(err)
		}
		if payload["store"] != false || !strings.Contains(string(data), `"strict":true`) {
			t.Errorf("request=%s", data)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write(envelope)
	}))
	defer server.Close()
	provider := &OpenAIStructuredProvider{apiKey: "secret", model: "model", endpoint: server.URL, client: &http.Client{Timeout: time.Second}, maxResponse: 64 << 10}
	fields, err := provider.Extract(context.Background(), "Innviertel")
	if err != nil || fields.Region.Value != "Innviertel" {
		t.Fatalf("fields/error=%#v/%v", fields, err)
	}
}

func TestOpenAIStructuredProviderRejectsUnknownOrOversizedOutput(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`{"output":[{"content":[{"type":"output_text","text":"{\"unknown\":true}"}]}]}`),
		[]byte(strings.Repeat("x", 2048)),
	} {
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { _, _ = response.Write(body) }))
		provider := &OpenAIStructuredProvider{apiKey: "secret", model: "model", endpoint: server.URL, client: &http.Client{Timeout: time.Second}, maxResponse: 1024}
		_, err := provider.Extract(context.Background(), "private transcript")
		server.Close()
		if !errors.Is(err, ErrProvider) || strings.Contains(err.Error(), "private transcript") {
			t.Fatalf("error=%v", err)
		}
	}
}
