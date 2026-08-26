package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

type SendberryConfig struct {
	URL, APIKey, AccessName, AccessPassword, Sender string
	Timeout                                         time.Duration
}

type SendberryProvider struct {
	cfg    SendberryConfig
	client *http.Client
}

func NewSendberryProvider(cfg SendberryConfig) (*SendberryProvider, error) {
	endpoint, err := url.Parse(cfg.URL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || loopbackHost(endpoint.Hostname()) ||
		len(cfg.APIKey) < 16 || strings.TrimSpace(cfg.AccessName) == "" || strings.TrimSpace(cfg.AccessPassword) == "" ||
		!validSendberrySender(cfg.Sender) || cfg.Timeout <= 0 {
		return nil, errors.New("notification: invalid Sendberry configuration")
	}
	return &SendberryProvider{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

func (provider *SendberryProvider) Send(ctx context.Context, message Message) (string, error) {
	if message.Channel != ChannelSMS {
		return "", fmt.Errorf("%w: wrong channel", ErrPermanent)
	}
	recipient, ok := normalizeE164(message.Recipient)
	if !ok || strings.TrimSpace(message.Text) == "" || len(message.Text) > 1600 {
		return "", fmt.Errorf("%w: invalid SMS message", ErrPermanent)
	}
	form := url.Values{
		"key": {provider.cfg.APIKey}, "name": {provider.cfg.AccessName}, "password": {provider.cfg.AccessPassword},
		"from": {provider.cfg.Sender}, "to[]": {recipient}, "content": {message.Text},
		"SMS_ID": {message.NotificationID}, "response": {"JSON"},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.cfg.URL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("%w: Sendberry request", ErrPermanent)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := provider.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("%w: Sendberry unavailable", ErrTemporary)
	}
	defer func() { _ = response.Body.Close() }()
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, 4097))
	if readErr != nil || len(payload) > 4096 {
		return "", fmt.Errorf("%w: Sendberry response invalid (status=%d)", ErrTemporary, response.StatusCode)
	}
	if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return "", fmt.Errorf("%w: Sendberry unavailable", ErrTemporary)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("%w: Sendberry rejected", ErrPermanent)
	}
	var result struct {
		Status string `json:"status"`
		ID     string `json:"ID"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return "", fmt.Errorf("%w: Sendberry response invalid (status=%d, content_type=%s)", ErrTemporary, response.StatusCode, safeMediaType(response.Header.Get("Content-Type")))
	}
	if !strings.EqualFold(result.Status, "ok") || strings.TrimSpace(result.ID) == "" {
		return "", fmt.Errorf("%w: Sendberry rejected", ErrPermanent)
	}
	return strings.TrimSpace(result.ID), nil
}

func safeMediaType(value string) string {
	value = strings.ToLower(strings.TrimSpace(strings.SplitN(value, ";", 2)[0]))
	if value == "application/json" || value == "text/plain" || value == "text/html" {
		return value
	}
	return "other"
}

func normalizeE164(value string) (string, bool) {
	value = strings.TrimSpace(value)
	var result strings.Builder
	for index, valueRune := range value {
		if valueRune >= '0' && valueRune <= '9' {
			result.WriteRune(valueRune)
			continue
		}
		if valueRune == '+' && index == 0 {
			continue
		}
		if valueRune != ' ' && valueRune != '-' && valueRune != '(' && valueRune != ')' {
			return "", false
		}
	}
	digits := result.String()
	return digits, len(digits) >= 8 && len(digits) <= 15 && digits[0] != '0'
}

func validSendberrySender(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return false
	}
	_, numeric := normalizeE164(value)
	if numeric {
		return len(strings.TrimPrefix(value, "+")) <= 15
	}
	return utf8.RuneCountInString(value) <= 11
}
