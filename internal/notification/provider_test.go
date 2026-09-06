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
	"net/textproto"
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

func TestSendberryConfigurationAndSenderValidation(t *testing.T) {
	valid := SendberryConfig{URL: "https://sms.example/send", APIKey: strings.Repeat("k", 16), AccessName: "access", AccessPassword: "password", Sender: "HackWerk", Timeout: time.Second}
	provider, err := NewSendberryProvider(valid)
	if err != nil || provider.client.Timeout != time.Second {
		t.Fatalf("NewSendberryProvider(valid) = %#v, %v", provider, err)
	}
	tests := []struct {
		name   string
		change func(*SendberryConfig)
	}{
		{name: "HTTP endpoint", change: func(cfg *SendberryConfig) { cfg.URL = "http://sms.example/send" }},
		{name: "loopback endpoint", change: func(cfg *SendberryConfig) { cfg.URL = "https://127.0.0.1/send" }},
		{name: "short API key", change: func(cfg *SendberryConfig) { cfg.APIKey = "short" }},
		{name: "missing access name", change: func(cfg *SendberryConfig) { cfg.AccessName = " " }},
		{name: "missing password", change: func(cfg *SendberryConfig) { cfg.AccessPassword = " " }},
		{name: "invalid sender", change: func(cfg *SendberryConfig) { cfg.Sender = "too-long-sender" }},
		{name: "header sender", change: func(cfg *SendberryConfig) { cfg.Sender = "Hack\r\nWerk" }},
		{name: "missing timeout", change: func(cfg *SendberryConfig) { cfg.Timeout = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.change(&cfg)
			if _, err := NewSendberryProvider(cfg); err == nil {
				t.Fatal("invalid Sendberry configuration accepted")
			}
		})
	}
	for _, test := range []struct {
		value string
		valid bool
	}{
		{value: "HackWerk", valid: true},
		{value: "+436601234567", valid: true},
		{value: "123", valid: true},
		{value: "", valid: false},
		{value: "abcdefghijkl", valid: false},
		{value: "Hack\nWerk", valid: false},
	} {
		if got := validSendberrySender(test.value); got != test.valid {
			t.Fatalf("validSendberrySender(%q) = %t, want %t", test.value, got, test.valid)
		}
	}
}

func TestSendberryClassifiesProviderResponses(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		want        error
	}{
		{name: "timeout", status: http.StatusRequestTimeout, want: ErrTemporary},
		{name: "rate limited", status: http.StatusTooManyRequests, want: ErrTemporary},
		{name: "server error", status: http.StatusBadGateway, want: ErrTemporary},
		{name: "rejected", status: http.StatusBadRequest, want: ErrPermanent},
		{name: "invalid JSON", status: http.StatusOK, contentType: "text/html; charset=utf-8", body: "<html>", want: ErrTemporary},
		{name: "negative response", status: http.StatusOK, contentType: "application/json", body: `{"status":"error","ID":"x"}`, want: ErrPermanent},
		{name: "missing ID", status: http.StatusOK, contentType: "application/json", body: `{"status":"ok"}`, want: ErrPermanent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", test.contentType)
				response.WriteHeader(test.status)
				_, _ = io.WriteString(response, test.body)
			}))
			defer server.Close()
			provider := &SendberryProvider{cfg: SendberryConfig{URL: server.URL, APIKey: strings.Repeat("k", 16), AccessName: "access", AccessPassword: "password", Sender: "HackWerk"}, client: server.Client()}
			_, err := provider.Send(t.Context(), Message{NotificationID: "notification", Channel: ChannelSMS, Recipient: "+436601234567", Text: "Termin"})
			if !errors.Is(err, test.want) {
				t.Fatalf("Send() error = %v, want %v", err, test.want)
			}
		})
	}

	for value, want := range map[string]string{
		"application/json; charset=utf-8": "application/json",
		"TEXT/PLAIN":                      "text/plain",
		"text/html":                       "text/html",
		"image/svg+xml":                   "other",
	} {
		if got := safeMediaType(value); got != want {
			t.Fatalf("safeMediaType(%q) = %q, want %q", value, got, want)
		}
	}
}

func TestSendberryRejectsInvalidMessages(t *testing.T) {
	provider := &SendberryProvider{}
	for _, test := range []struct {
		name    string
		message Message
	}{
		{name: "wrong channel", message: Message{Channel: ChannelEmail}},
		{name: "invalid recipient", message: Message{Channel: ChannelSMS, Recipient: "0660", Text: "Termin"}},
		{name: "empty text", message: Message{Channel: ChannelSMS, Recipient: "+436601234567"}},
		{name: "long text", message: Message{Channel: ChannelSMS, Recipient: "+436601234567", Text: strings.Repeat("x", 1601)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := provider.Send(t.Context(), test.message); !errors.Is(err, ErrPermanent) {
				t.Fatalf("Send() error = %v, want permanent", err)
			}
		})
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

func TestSMTPRejectsUnsafeMessagesAndClassifiesFailures(t *testing.T) {
	t.Parallel()
	config := SMTPConfig{Host: "smtp.example", Port: 587, TLSMode: "starttls", FromAddress: "mail@example.test", ConnectTimeout: time.Second, CommandTimeout: time.Second}
	provider, err := NewSMTPProvider(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		message Message
		want    error
	}{
		{name: "wrong channel", message: Message{Channel: ChannelSMS}, want: ErrPermanent},
		{name: "invalid recipient", message: Message{Channel: ChannelEmail, Recipient: "invalid", Subject: "Termin"}, want: ErrPermanent},
		{name: "header injection", message: Message{Channel: ChannelEmail, Recipient: "kunde@example.test", Subject: "Termin\r\nBcc: attacker@example.test"}, want: ErrPermanent},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := provider.Send(t.Context(), test.message); !errors.Is(err, test.want) {
				t.Fatalf("Send() error = %v, want %v", err, test.want)
			}
		})
	}
	provider.dial = func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("network unavailable") }
	if _, err := provider.Send(t.Context(), Message{Channel: ChannelEmail, Recipient: "kunde@example.test", Subject: "Termin", Text: "Text", HTML: "<p>Text</p>"}); !errors.Is(err, ErrTemporary) {
		t.Fatalf("connection failure error = %v", err)
	}
	if !errors.Is(classifySMTP(&textproto.Error{Code: 550, Msg: "rejected"}), ErrPermanent) || !errors.Is(classifySMTP(errors.New("network unavailable")), ErrTemporary) {
		t.Fatal("SMTP error classification is incorrect")
	}
	for _, invalid := range []SMTPConfig{
		{Host: "", Port: 587, TLSMode: "starttls", FromAddress: "mail@example.test", ConnectTimeout: time.Second, CommandTimeout: time.Second},
		{Host: "smtp.example", Port: 0, TLSMode: "starttls", FromAddress: "mail@example.test", ConnectTimeout: time.Second, CommandTimeout: time.Second},
		{Host: "smtp.example", Port: 587, TLSMode: "starttls", FromAddress: "invalid", ConnectTimeout: time.Second, CommandTimeout: time.Second},
		{Host: "smtp.example", Port: 587, TLSMode: "starttls", FromAddress: "mail@example.test", ReplyTo: "invalid", ConnectTimeout: time.Second, CommandTimeout: time.Second},
	} {
		if _, err := NewSMTPProvider(invalid); err == nil {
			t.Fatalf("invalid SMTP config accepted: %#v", invalid)
		}
	}
}

func TestSMSWebhookConfigurationAndStatusClassification(t *testing.T) {
	t.Parallel()
	provider, err := NewSMSWebhookProvider(SMSWebhookConfig{URL: "https://sms.example/send", Secret: strings.Repeat("x", 32), Sender: "HackWerk", Timeout: time.Second}, nil)
	if err != nil || provider.now == nil || provider.client.Timeout != time.Second {
		t.Fatalf("NewSMSWebhookProvider(valid) = %#v / %v", provider, err)
	}
	for _, config := range []SMSWebhookConfig{
		{URL: "http://sms.example/send", Secret: strings.Repeat("x", 32), Timeout: time.Second},
		{URL: "https://localhost/send", Secret: strings.Repeat("x", 32), Timeout: time.Second},
		{URL: "https://sms.example/send", Secret: "short", Timeout: time.Second},
		{URL: "https://sms.example/send", Secret: strings.Repeat("x", 32)},
	} {
		if _, err := NewSMSWebhookProvider(config, time.Now); err == nil {
			t.Fatalf("invalid SMS config accepted: %#v", config)
		}
	}
	if !loopbackHost("localhost") || !loopbackHost("0.0.0.0") || loopbackHost("sms.example") {
		t.Fatal("loopback host classification is incorrect")
	}
	for _, test := range []struct {
		status int
		want   error
	}{
		{status: http.StatusTooManyRequests, want: ErrTemporary},
		{status: http.StatusBadRequest, want: ErrPermanent},
	} {
		server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(test.status) }))
		provider := &SMSWebhookProvider{cfg: SMSWebhookConfig{URL: server.URL, Secret: strings.Repeat("x", 32), Timeout: time.Second}, client: server.Client(), now: time.Now}
		_, err := provider.Send(t.Context(), Message{Channel: ChannelSMS, Recipient: "+43664123456", Text: "Termin"})
		server.Close()
		if !errors.Is(err, test.want) {
			t.Fatalf("status %d error = %v, want %v", test.status, err, test.want)
		}
	}
}
