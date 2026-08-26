package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"example.invalid/hackplan/internal/outbound"
)

type DisabledTranscriber struct{}

func (DisabledTranscriber) Transcribe(context.Context, Audio, string, Metadata) (Transcript, error) {
	return Transcript{}, ErrDisabled
}

type FakeTranscriber struct{ Text string }

func (fake FakeTranscriber) Transcribe(_ context.Context, _ Audio, _ string, _ Metadata) (Transcript, error) {
	if strings.TrimSpace(fake.Text) == "" {
		return Transcript{}, ErrProvider
	}
	return Transcript{Text: fake.Text, Provider: "fake", Version: "fixture-v1", Confidence: .95}, nil
}

type OpenAITranscriber struct {
	apiKey, model, endpoint string
	client                  *http.Client
	maxResponse             int64
}

func NewOpenAITranscriber(apiKey, model string, timeout time.Duration, maxResponse int64) (*OpenAITranscriber, error) {
	if strings.TrimSpace(apiKey) == "" || strings.TrimSpace(model) == "" || timeout <= 0 || maxResponse < 1024 || maxResponse > 4<<20 {
		return nil, errors.New("voice: invalid OpenAI transcriber configuration")
	}
	return &OpenAITranscriber{apiKey: apiKey, model: model, endpoint: "https://api.openai.com/v1/audio/transcriptions", client: &http.Client{Transport: outbound.Transport(), Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, maxResponse: maxResponse}, nil
}

func (provider *OpenAITranscriber) Transcribe(ctx context.Context, audio Audio, language string, _ Metadata) (result Transcript, resultErr error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", safeAudioName(audio.Filename))
	if err != nil {
		return Transcript{}, ErrProvider
	}
	if _, err = io.Copy(part, audio.Reader); err != nil {
		return Transcript{}, ErrProvider
	}
	_ = writer.WriteField("model", provider.model)
	_ = writer.WriteField("language", language)
	_ = writer.WriteField("response_format", "json")
	if err = writer.Close(); err != nil {
		return Transcript{}, ErrProvider
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.endpoint, &body)
	if err != nil {
		return Transcript{}, ErrProvider
	}
	request.Header.Set("Authorization", "Bearer "+provider.apiKey)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Accept", "application/json")
	response, err := provider.client.Do(request)
	if err != nil {
		return Transcript{}, ErrProvider
	}
	defer func() { resultErr = errors.Join(resultErr, response.Body.Close()) }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Transcript{}, ErrProvider
	}
	limited := io.LimitReader(response.Body, provider.maxResponse+1)
	payload, err := io.ReadAll(limited)
	if err != nil || int64(len(payload)) > provider.maxResponse {
		return Transcript{}, ErrProvider
	}
	var parsed struct {
		Text string `json:"text"`
	}
	if err = json.Unmarshal(payload, &parsed); err != nil || strings.TrimSpace(parsed.Text) == "" {
		return Transcript{}, ErrProvider
	}
	return Transcript{Text: parsed.Text, Provider: "openai", Version: provider.model, Confidence: .8}, nil
}

func safeAudioName(value string) string {
	lower := strings.ToLower(value)
	for _, suffix := range []string{".webm", ".ogg", ".wav", ".mp4", ".m4a"} {
		if strings.HasSuffix(lower, suffix) {
			return "aufnahme" + suffix
		}
	}
	return fmt.Sprintf("aufnahme-%d.bin", time.Now().Unix())
}
