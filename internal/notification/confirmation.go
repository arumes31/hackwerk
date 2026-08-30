package notification

import (
	"context"
	"errors"
	"strings"
	"time"
)

type Response string

const (
	ResponseConfirmed Response = "confirmed"
	ResponseDeclined  Response = "declined"
	ResponseCallback  Response = "callback_requested"
)

var (
	ErrConfirmationUnavailable = errors.New("notification: confirmation unavailable")
	ErrConfirmationExpired     = errors.New("notification: confirmation expired")
	ErrConfirmationRevoked     = errors.New("notification: confirmation revoked")
	ErrResponseLocked          = errors.New("notification: confirmation response locked")
	ErrResponseNoteNotAllowed  = errors.New("notification: response note requires decline")
)

type Confirmation struct {
	RequestID, AppointmentID, CustomerName, Locality string
	FormNonce                                        string
	JobNumber, JobType, VolumeM3                     string
	TokenKeyID                                       string
	TokenVersion, AppointmentVersion                 int32
	Status, Lifecycle, ConfirmationStatus            string
	Response                                         Response
	ResponseNote                                     string
	StartsAt, EndsAt, ExpiresAt                      time.Time
	TokenHash, FormNonceHash                         []byte
}

type ConfirmationStore interface {
	Lookup(context.Context, []byte) (Confirmation, error)
	Respond(context.Context, []byte, []byte, Response, string, string, time.Time) (Confirmation, error)
}

type ConfirmationService struct {
	store  ConfirmationStore
	tokens *KeyRing
	now    func() time.Time
}

func NewConfirmationService(store ConfirmationStore, tokens *KeyRing, now func() time.Time) (*ConfirmationService, error) {
	if store == nil || tokens == nil {
		return nil, errors.New("notification: confirmation store and token keyring are required")
	}
	if now == nil {
		now = time.Now
	}
	return &ConfirmationService{store: store, tokens: tokens, now: now}, nil
}

func (service *ConfirmationService) View(ctx context.Context, rawToken string) (Confirmation, error) {
	hash, err := HashRawToken(strings.TrimSpace(rawToken))
	if err != nil {
		return Confirmation{}, ErrConfirmationUnavailable
	}
	value, err := service.store.Lookup(ctx, hash)
	if err != nil {
		return Confirmation{}, ErrConfirmationUnavailable
	}
	material, err := service.tokens.Reconstruct(value.TokenKeyID, value.RequestID, value.AppointmentID, value.TokenVersion)
	if err != nil || !ConstantTimeEqual(material.Hash, value.TokenHash) || !ConstantTimeEqual(material.NonceHash, value.FormNonceHash) {
		return Confirmation{}, ErrConfirmationUnavailable
	}
	if value.Status == "revoked" {
		return Confirmation{}, ErrConfirmationRevoked
	}
	if !service.now().Before(value.ExpiresAt) {
		return Confirmation{}, ErrConfirmationExpired
	}
	if !confirmationUsable(value, service.now()) {
		return Confirmation{}, ErrConfirmationUnavailable
	}
	value.FormNonce = material.FormNonce
	return value, nil
}

func (service *ConfirmationService) Respond(ctx context.Context, rawToken, formNonce string, response Response, responseNote, requestID string) (Confirmation, error) {
	if response != ResponseConfirmed && response != ResponseDeclined && response != ResponseCallback {
		return Confirmation{}, ErrConfirmationUnavailable
	}
	responseNote = strings.TrimSpace(responseNote)
	if len([]rune(responseNote)) > 500 {
		return Confirmation{}, ErrConfirmationUnavailable
	}
	if response != ResponseDeclined && responseNote != "" {
		return Confirmation{}, ErrResponseNoteNotAllowed
	}
	tokenHash, err := HashRawToken(strings.TrimSpace(rawToken))
	if err != nil {
		return Confirmation{}, ErrConfirmationUnavailable
	}
	nonceHash, err := HashFormNonce(strings.TrimSpace(formNonce))
	if err != nil {
		return Confirmation{}, ErrConfirmationUnavailable
	}
	value, err := service.store.Respond(ctx, tokenHash, nonceHash, response, responseNote, requestID, service.now().UTC())
	if err != nil {
		return Confirmation{}, err
	}
	return value, nil
}

func confirmationUsable(value Confirmation, now time.Time) bool {
	return value.Status == "active" && value.Lifecycle == "fixed" && now.Before(value.ExpiresAt)
}
