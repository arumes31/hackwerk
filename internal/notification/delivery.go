package notification

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Channel string

const (
	ChannelEmail Channel = "email"
	ChannelSMS   Channel = "sms"
)

var (
	ErrPermanent         = errors.New("notification: permanent provider failure")
	ErrTemporary         = errors.New("notification: temporary provider failure")
	ErrDeliveryUncertain = errors.New("notification: delivery outcome requires reconciliation")
)

type Message struct {
	NotificationID, IdempotencyKey string
	Channel                        Channel
	Recipient, Subject             string
	Text, HTML                     string
}

type Provider interface {
	Send(context.Context, Message) (string, error)
}

func Backoff(attempt int, idempotencyKey string) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	exponent := attempt - 1
	if exponent > 11 {
		exponent = 11
	}
	base := time.Second * time.Duration(1<<exponent)
	digest := sha256.Sum256([]byte(idempotencyKey + ":" + strconv.Itoa(attempt)))
	jitter := time.Duration(binary.BigEndian.Uint16(digest[:2])%1000) * time.Millisecond
	result := base + jitter
	if result > time.Hour {
		return time.Hour
	}
	return result
}

func MaskRecipient(value string, channel Channel) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "nicht verfügbar"
	}
	if channel == ChannelEmail {
		parts := strings.SplitN(value, "@", 2)
		if len(parts) != 2 || len(parts[0]) == 0 {
			return "***"
		}
		return string([]rune(parts[0])[0]) + "***@" + parts[1]
	}
	digits := make([]rune, 0, len(value))
	for _, valueRune := range value {
		if valueRune >= '0' && valueRune <= '9' {
			digits = append(digits, valueRune)
		}
	}
	if len(digits) <= 3 {
		return "***"
	}
	return "***" + string(digits[len(digits)-3:])
}

type FakeProvider struct {
	mu         sync.Mutex
	deliveries []Message
	err        error
}

func NewFakeProvider(sendErr error) *FakeProvider { return &FakeProvider{err: sendErr} }

func (provider *FakeProvider) Send(_ context.Context, message Message) (string, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.err != nil {
		return "", provider.err
	}
	provider.deliveries = append(provider.deliveries, message)
	return "fake-" + message.NotificationID, nil
}

func (provider *FakeProvider) Deliveries() []Message {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return append([]Message(nil), provider.deliveries...)
}
