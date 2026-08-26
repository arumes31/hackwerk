package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type StructuredProvider interface {
	Extract(context.Context, string) (Fields, error)
	Version() string
}

type HybridExtractor struct {
	Rules    RuleExtractor
	Provider StructuredProvider
}

func (extractor HybridExtractor) Version() string {
	if extractor.Provider == nil {
		return extractor.Rules.Version()
	}
	return extractor.Rules.Version() + "+" + extractor.Provider.Version()
}

func (extractor HybridExtractor) Extract(ctx context.Context, transcript string, recordedAt time.Time, location *time.Location) (Fields, []string, float64) {
	fields, warnings, _ := extractor.Rules.Extract(ctx, transcript, recordedAt, location)
	if extractor.Provider == nil {
		return fields, warnings, averageConfidence(fields)
	}
	proposed, err := extractor.Provider.Extract(ctx, transcript)
	if err != nil {
		warnings = append(warnings, "Strukturierungsprovider nicht verfügbar; deterministische Erkennung verwendet")
		return fields, warnings, averageConfidence(fields)
	}
	mergeModelFields(transcript, &fields, proposed)
	return fields, warnings, averageConfidence(fields)
}

type OpenAIStructuredProvider struct {
	apiKey, model, endpoint string
	client                  *http.Client
	maxResponse             int64
}

func NewOpenAIStructuredProvider(apiKey, model string, timeout time.Duration, maxResponse int64) (*OpenAIStructuredProvider, error) {
	if strings.TrimSpace(apiKey) == "" || strings.TrimSpace(model) == "" || timeout <= 0 || maxResponse < 1024 || maxResponse > 4<<20 {
		return nil, errors.New("voice: invalid structured extractor configuration")
	}
	return &OpenAIStructuredProvider{apiKey: apiKey, model: model, endpoint: "https://api.openai.com/v1/responses", client: &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, maxResponse: maxResponse}, nil
}

func (provider *OpenAIStructuredProvider) Version() string { return "openai-" + provider.model }

func (provider *OpenAIStructuredProvider) Extract(ctx context.Context, transcript string) (result Fields, resultErr error) {
	requestBody := map[string]any{
		"model": provider.model, "store": false,
		"input": []map[string]any{
			{"role": "system", "content": []map[string]string{{"type": "input_text", "text": "Extrahiere nur ausdrücklich im österreichisch-deutschen Transkript vorhandene Kundendaten und Hackauftragsdaten. Unbekannte Werte bleiben leer. Quelle muss ein wörtlicher Ausschnitt des Transkripts sein."}}},
			{"role": "user", "content": []map[string]string{{"type": "input_text", "text": transcript}}},
		},
		"text": map[string]any{"format": map[string]any{"type": "json_schema", "name": "hackwerk_voice_fields", "strict": true, "schema": voiceFieldsSchema()}},
	}
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return Fields{}, ErrProvider
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.endpoint, bytes.NewReader(payload))
	if err != nil {
		return Fields{}, ErrProvider
	}
	request.Header.Set("Authorization", "Bearer "+provider.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := provider.client.Do(request)
	if err != nil {
		return Fields{}, ErrProvider
	}
	defer func() { resultErr = errors.Join(resultErr, response.Body.Close()) }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Fields{}, ErrProvider
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, provider.maxResponse+1))
	if err != nil || int64(len(data)) > provider.maxResponse {
		return Fields{}, ErrProvider
	}
	var envelope struct {
		Output []struct {
			Type    string                        `json:"type"`
			Content []struct{ Type, Text string } `json:"content"`
		} `json:"output"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil {
		return Fields{}, ErrProvider
	}
	var output string
	for _, item := range envelope.Output {
		for _, content := range item.Content {
			if content.Type == "output_text" {
				output = content.Text
				break
			}
		}
	}
	if output == "" || len(output) > 64<<10 {
		return Fields{}, ErrProvider
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.DisallowUnknownFields()
	var fields Fields
	if err = decoder.Decode(&fields); err != nil || !validModelFields(fields) {
		return Fields{}, ErrProvider
	}
	return fields, nil
}

func voiceFieldsSchema() map[string]any {
	names := []string{"first_name", "last_name", "company_name", "address_freeform", "phone_raw", "email", "volume_m3", "estimated_hack_minutes", "estimated_transport_minutes", "transport_trip_count", "transport_mode", "preference_text", "preferred_start_date", "preferred_end_date", "urgency", "region", "note"}
	fieldSchema := map[string]any{"type": "object", "additionalProperties": false, "required": []string{"value", "source", "confidence", "warnings"}, "properties": map[string]any{"value": map[string]any{"type": "string", "maxLength": 1000}, "source": map[string]any{"type": "string", "maxLength": 500}, "confidence": map[string]any{"type": "number", "minimum": 0, "maximum": 1}, "warnings": map[string]any{"type": "array", "maxItems": 10, "items": map[string]any{"type": "string", "maxLength": 200}}}}
	properties := make(map[string]any, len(names))
	for _, name := range names {
		properties[name] = fieldSchema
	}
	return map[string]any{"type": "object", "additionalProperties": false, "required": names, "properties": properties}
}

func validModelFields(fields Fields) bool {
	for _, field := range fieldPointers(&fields) {
		if field.Confidence < 0 || field.Confidence > 1 || len(field.Value) > 1000 || len(field.Source) > 500 || len(field.Warnings) > 10 {
			return false
		}
		for _, warning := range field.Warnings {
			if len(warning) > 200 {
				return false
			}
		}
	}
	return true
}

func mergeModelFields(transcript string, target *Fields, proposed Fields) {
	targets, proposals := fieldPointers(target), fieldPointers(&proposed)
	transcriptLower := strings.ToLower(transcript)
	for index, suggestion := range proposals {
		if targets[index].Value != "" || strings.TrimSpace(suggestion.Value) == "" || strings.TrimSpace(suggestion.Source) == "" || !strings.Contains(transcriptLower, strings.ToLower(strings.TrimSpace(suggestion.Source))) {
			continue
		}
		value := strings.TrimSpace(suggestion.Value)
		source := strings.TrimSpace(suggestion.Source)
		confidence := min(suggestion.Confidence, .70)
		*targets[index] = Field{Value: value, Source: source, Confidence: confidence, Warnings: append([]string{"Vom Modell vorgeschlagen – ausdrücklich prüfen"}, suggestion.Warnings...)}
	}
}

func fieldPointers(fields *Fields) []*Field {
	return []*Field{&fields.FirstName, &fields.LastName, &fields.CompanyName, &fields.AddressFreeform, &fields.PhoneRaw, &fields.Email, &fields.VolumeM3, &fields.EstimatedHackMinutes, &fields.EstimatedTransportMinutes, &fields.TransportTripCount, &fields.TransportMode, &fields.PreferenceText, &fields.PreferredStartDate, &fields.PreferredEndDate, &fields.Urgency, &fields.Region, &fields.Note}
}
