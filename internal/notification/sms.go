package notification

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"example.invalid/hackplan/internal/outbound"
)

type SMSWebhookConfig struct {
	URL, Secret, Sender string
	Timeout             time.Duration
}

type SMSWebhookProvider struct {
	cfg    SMSWebhookConfig
	client *http.Client
	now    func() time.Time
}

func NewSMSWebhookProvider(cfg SMSWebhookConfig, now func() time.Time) (*SMSWebhookProvider, error) {
	parsed, err := url.Parse(cfg.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || loopbackHost(parsed.Hostname()) || len(cfg.Secret) < 32 || cfg.Timeout <= 0 {
		return nil, errors.New("notification: invalid SMS webhook configuration")
	}
	if now == nil {
		now = time.Now
	}
	return &SMSWebhookProvider{cfg: cfg, now: now, client: &http.Client{Transport: outbound.Transport(), Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

func (provider *SMSWebhookProvider) Send(ctx context.Context, message Message) (string, error) {
	if message.Channel != ChannelSMS {
		return "", fmt.Errorf("%w: wrong channel", ErrPermanent)
	}
	body, err := json.Marshal(map[string]string{
		"to": message.Recipient, "text": message.Text, "message_id": message.NotificationID,
		"idempotency_key": message.IdempotencyKey, "sender": provider.cfg.Sender,
	})
	if err != nil {
		return "", fmt.Errorf("%w: SMS payload", ErrPermanent)
	}
	timestamp := strconv.FormatInt(provider.now().UTC().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(provider.cfg.Secret))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("%w: SMS request", ErrPermanent)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-HackWerk-Timestamp", timestamp)
	request.Header.Set("X-HackWerk-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	request.Header.Set("Idempotency-Key", message.IdempotencyKey)
	response, err := provider.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("%w: SMS unavailable", ErrTemporary)
	}
	defer func() { _ = response.Body.Close() }()
	limited, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(limited) > 4096 {
		return "", fmt.Errorf("%w: SMS response invalid", ErrTemporary)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusRequestTimeout || response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
			return "", fmt.Errorf("%w: SMS status", ErrTemporary)
		}
		return "", fmt.Errorf("%w: SMS rejected", ErrPermanent)
	}
	var result struct {
		ProviderID string `json:"provider_id"`
	}
	if err := json.Unmarshal(limited, &result); err != nil || strings.TrimSpace(result.ProviderID) == "" {
		return "", fmt.Errorf("%w: SMS response invalid", ErrTemporary)
	}
	return strings.TrimSpace(result.ProviderID), nil
}
