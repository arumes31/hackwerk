package voice

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOpenAITranscriberSendsBoundedMultipartWithoutFilenamePII(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer server-secret" {
			t.Error("missing authorization")
		}
		data, _ := io.ReadAll(request.Body)
		body = string(data)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"text":"Servus aus dem Test"}`))
	}))
	defer server.Close()
	provider := &OpenAITranscriber{apiKey: "server-secret", model: "speech-model", endpoint: server.URL, client: &http.Client{Timeout: time.Second}, maxResponse: 1024}
	result, err := provider.Transcribe(context.Background(), Audio{Reader: bytes.NewReader([]byte("audio")), Filename: "kundin-private-name.webm", ContentType: "audio/webm", Size: 5}, "de", Metadata{})
	if err != nil || result.Text != "Servus aus dem Test" || !strings.Contains(body, `filename="aufnahme.webm"`) || strings.Contains(body, "private-name") {
		t.Fatalf("result/error/body=%#v/%v/%q", result, err, body)
	}
}

func TestOpenAITranscriberRejectsErrorAndOversizedResponseWithoutLeakingPayload(t *testing.T) {
	for _, test := range []struct {
		name    string
		handler http.HandlerFunc
		max     int64
	}{{"status", func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "provider says api key secret and transcript", http.StatusBadRequest)
	}, 1024}, {"oversized", func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write(bytes.Repeat([]byte("x"), 2048))
	}, 1024}} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			provider := &OpenAITranscriber{apiKey: "secret", model: "model", endpoint: server.URL, client: &http.Client{Timeout: time.Second}, maxResponse: test.max}
			_, err := provider.Transcribe(context.Background(), Audio{Reader: bytes.NewReader([]byte("audio")), Filename: "a.webm"}, "de", Metadata{})
			if !errors.Is(err, ErrProvider) || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "transcript") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestWhisperCPPTranscriberUsesGermanLocalInferenceRequest(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "" {
			t.Error("local transcriber sent authorization")
		}
		data, _ := io.ReadAll(request.Body)
		body = string(data)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"text":"Servus, das ist eine deutsche Aufnahme."}`))
	}))
	defer server.Close()
	provider := &WhisperCPPTranscriber{model: "small", endpoint: server.URL, client: &http.Client{Timeout: time.Second}, maxResponse: 1024}
	result, err := provider.Transcribe(context.Background(), Audio{Reader: bytes.NewReader([]byte("audio")), Filename: "kundin-private-name.webm"}, "de", Metadata{})
	if err != nil || result.Provider != "whisper.cpp" || result.Version != "small" || result.Text == "" {
		t.Fatalf("Transcribe() = %#v, %v", result, err)
	}
	for _, expected := range []string{`name="language"`, "de", `name="temperature"`, "0.0", `name="response_format"`, "json", `filename="aufnahme.webm"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("multipart body missing %q: %q", expected, body)
		}
	}
	if strings.Contains(body, "private-name") || strings.Contains(body, `name="model"`) {
		t.Fatalf("multipart body contains unexpected data: %q", body)
	}
}
