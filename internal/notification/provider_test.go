package notification

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSMSWebhookSignsStaticRequestAndUsesIdempotencyKey(t *testing.T) {
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	secret := "01234567890123456789012345678901"
	var observedBody, observedSignature, observedTimestamp, observedID string
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(request.Body, 4096))
		observedBody = string(body)
		observedSignature = request.Header.Get("X-HackWerk-Signature")
		observedTimestamp = request.Header.Get("X-HackWerk-Timestamp")
		observedID = request.Header.Get("Idempotency-Key")
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"provider_id":"sms-123"}`)
	}))
	defer server.Close()
	provider := &SMSWebhookProvider{
		cfg:    SMSWebhookConfig{URL: server.URL, Secret: secret, Sender: "HackWerk", Timeout: time.Second},
		client: server.Client(), now: func() time.Time { return now },
	}
	providerID, err := provider.Send(t.Context(), Message{NotificationID: "notification", IdempotencyKey: "idempotent-1", Channel: ChannelSMS, Recipient: "+43664123456", Text: "Termin"})
	if err != nil || providerID != "sms-123" || observedID != "idempotent-1" {
		t.Fatalf("SMS result/idempotency = %q/%v/%q", providerID, err, observedID)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = io.WriteString(mac, observedTimestamp+"."+observedBody)
	if observedSignature != "sha256="+hex.EncodeToString(mac.Sum(nil)) || strings.Contains(observedBody, server.URL) {
		t.Fatalf("SMS signature/static target mismatch: %q body=%q", observedSignature, observedBody)
	}
}

func TestSMTPImplicitTLSHandshakeIsBoundedByConnectTimeout(t *testing.T) {
	provider, err := NewSMTPProvider(SMTPConfig{
		Host: "smtp.example", Port: 465, TLSMode: "implicit", FromAddress: "mail@example.test",
		ConnectTimeout: 25 * time.Millisecond, CommandTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	provider.dial = func(context.Context, string, string) (net.Conn, error) { return client, nil }
	started := time.Now()
	_, err = provider.Send(t.Context(), Message{
		NotificationID: "notification", Channel: ChannelEmail, Recipient: "kunde@example.test",
		Subject: "Termin", Text: "Text", HTML: "<p>Text</p>",
	})
	if !errors.Is(err, ErrTemporary) || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("stalled implicit TLS handshake error/duration = %v/%s", err, time.Since(started))
	}
}

func TestSMSWebhookRejectsInvalidOrUnacknowledgedResponses(t *testing.T) {
	tests := []struct {
		name, body string
	}{
		{name: "invalid JSON", body: `<html>accepted</html>`},
		{name: "missing provider reference", body: `{}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(response, test.body)
			}))
			defer server.Close()
			provider := &SMSWebhookProvider{
				cfg:    SMSWebhookConfig{URL: server.URL, Secret: strings.Repeat("x", 32), Sender: "HackWerk", Timeout: time.Second},
				client: server.Client(), now: time.Now,
			}
			_, err := provider.Send(t.Context(), Message{
				NotificationID: "notification", IdempotencyKey: "idempotency", Channel: ChannelSMS, Recipient: "+436641234567", Text: "Termin",
			})
			if !errors.Is(err, ErrTemporary) {
				t.Fatalf("Send() error = %v, want temporary", err)
			}
		})
	}
}

func TestSendberryUsesFormPostAndProviderMessageID(t *testing.T) {
	var method, contentType string
	var fields url.Values
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		method, contentType = request.Method, request.Header.Get("Content-Type")
		_ = request.ParseForm()
		fields = request.Form
		_, _ = io.WriteString(response, `{"status":"ok","ID":"AA000133","SMS_ID":"notification-id"}`)
	}))
	defer server.Close()
	provider := &SendberryProvider{
		cfg:    SendberryConfig{URL: server.URL, APIKey: strings.Repeat("k", 32), AccessName: "access", AccessPassword: "password", Sender: "SMS Inform", Timeout: time.Second},
		client: server.Client(),
	}
	providerID, err := provider.Send(t.Context(), Message{
		NotificationID: "notification-id", IdempotencyKey: "outbox-id", Channel: ChannelSMS,
		Recipient: "+43 660 123 4567", Text: "HackWerk Test",
	})
	if err != nil || providerID != "AA000133" {
		t.Fatalf("Sendberry result = %q/%v", providerID, err)
	}
	if method != http.MethodPost || !strings.HasPrefix(contentType, "application/x-www-form-urlencoded") || fields.Get("to[]") != "436601234567" || fields.Get("SMS_ID") != "notification-id" || fields.Get("response") != "JSON" {
		t.Fatalf("Sendberry request method/content/form = %q/%q/%v", method, contentType, fields)
	}
}

func TestProviderConfigurationAndSMTPMessageEncoding(t *testing.T) {
	if _, err := NewSMSWebhookProvider(SMSWebhookConfig{URL: "https://127.0.0.1/send", Secret: strings.Repeat("x", 32), Timeout: time.Second}, nil); err == nil {
		t.Fatal("loopback SMS webhook was accepted")
	}
	if _, err := NewSMTPProvider(SMTPConfig{Host: "smtp.example", Port: 587, TLSMode: "plain", FromAddress: "mail@example.test", ConnectTimeout: time.Second, CommandTimeout: time.Second}); err == nil {
		t.Fatal("SMTP without TLS was accepted")
	}
	payload, err := smtpMessage(SMTPConfig{Host: "smtp.example", FromAddress: "mail@example.test", FromName: "HackWerk", ReplyTo: "office@example.test"}, "kunde@example.test", Message{NotificationID: "safe-id", Subject: "Grüß Gott", Text: "Text", HTML: "<p>Text</p>"})
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(payload)
	for _, wanted := range []string{"multipart/alternative", "quoted-printable", "Message-ID: <safe-id@smtp.example>", "Reply-To: office@example.test"} {
		if !strings.Contains(encoded, wanted) {
			t.Fatalf("SMTP message missing %q: %s", wanted, encoded)
		}
	}
}
