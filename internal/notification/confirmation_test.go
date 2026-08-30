package notification

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type confirmationStoreStub struct {
	value        Confirmation
	lookupErr    error
	respondErr   error
	response     Response
	responseNote string
	requestID    string
	tokenHash    []byte
	nonceHash    []byte
	respondedAt  time.Time
}

func (store *confirmationStoreStub) Lookup(_ context.Context, hash []byte) (Confirmation, error) {
	store.tokenHash = append([]byte(nil), hash...)
	return store.value, store.lookupErr
}

func (store *confirmationStoreStub) Respond(_ context.Context, tokenHash, nonceHash []byte, response Response, responseNote, requestID string, at time.Time) (Confirmation, error) {
	store.tokenHash = append([]byte(nil), tokenHash...)
	store.nonceHash = append([]byte(nil), nonceHash...)
	store.response, store.requestID, store.respondedAt = response, requestID, at
	store.responseNote = responseNote
	return store.value, store.respondErr
}

func TestConfirmationServiceViewsOnlyActiveVerifiedTokens(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC)
	ring := DevelopmentKeyRing()
	material, err := ring.Issue("request-1", "appointment-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	store := &confirmationStoreStub{value: Confirmation{
		RequestID: "request-1", AppointmentID: "appointment-1", TokenKeyID: material.KeyID, TokenVersion: 2,
		Status: "active", Lifecycle: "fixed", ExpiresAt: now.Add(time.Hour), TokenHash: material.Hash, FormNonceHash: material.NonceHash,
	}}
	service, err := NewConfirmationService(store, ring, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	value, err := service.View(t.Context(), "  "+material.Raw+"  ")
	if err != nil || value.FormNonce != material.FormNonce || !ConstantTimeEqual(store.tokenHash, material.Hash) {
		t.Fatalf("View() value/error/hash = %#v / %v / %x", value, err, store.tokenHash)
	}

	for _, test := range []struct {
		name   string
		mutate func(*Confirmation)
		token  string
		want   error
	}{
		{name: "malformed token", token: "not-a-token", want: ErrConfirmationUnavailable},
		{name: "lookup error", mutate: func(_ *Confirmation) { store.lookupErr = errors.New("not found") }, token: material.Raw, want: ErrConfirmationUnavailable},
		{name: "inactive confirmation", mutate: func(value *Confirmation) { value.Status = "revoked" }, token: material.Raw, want: ErrConfirmationRevoked},
		{name: "expired confirmation", mutate: func(value *Confirmation) { value.ExpiresAt = now }, token: material.Raw, want: ErrConfirmationExpired},
		{name: "different token material", mutate: func(value *Confirmation) { value.TokenHash = make([]byte, 32) }, token: material.Raw, want: ErrConfirmationUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			store.lookupErr = nil
			store.value = Confirmation{RequestID: "request-1", AppointmentID: "appointment-1", TokenKeyID: material.KeyID, TokenVersion: 2, Status: "active", Lifecycle: "fixed", ExpiresAt: now.Add(time.Hour), TokenHash: material.Hash, FormNonceHash: material.NonceHash}
			if test.mutate != nil {
				test.mutate(&store.value)
			}
			if _, err := service.View(t.Context(), test.token); !errors.Is(err, test.want) {
				t.Fatalf("View() error = %v", err)
			}
		})
	}
}

func TestConfirmationServiceRespondsWithValidatedTokenAndNonce(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC)
	ring := DevelopmentKeyRing()
	material, err := ring.Issue("request-1", "appointment-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	store := &confirmationStoreStub{value: Confirmation{RequestID: "request-1", Response: ResponseConfirmed}}
	service, _ := NewConfirmationService(store, ring, func() time.Time { return now })
	value, err := service.Respond(t.Context(), material.Raw, material.FormNonce, ResponseConfirmed, "", "request-2")
	if err != nil || value.Response != ResponseConfirmed || store.response != ResponseConfirmed || store.requestID != "request-2" || !store.respondedAt.Equal(now) || len(store.tokenHash) != 32 || len(store.nonceHash) != 32 {
		t.Fatalf("Respond() value/store/error = %#v / %#v / %v", value, store, err)
	}
	store.respondErr = nil
	if _, err := service.Respond(t.Context(), material.Raw, material.FormNonce, ResponseDeclined, "  Bitte vormittags  ", "request-note"); err != nil || store.responseNote != "Bitte vormittags" {
		t.Fatalf("decline response note = %q / %v", store.responseNote, err)
	}
	store.responseNote = ""
	if _, err := service.Respond(t.Context(), material.Raw, material.FormNonce, ResponseConfirmed, "nicht erlaubt", "request"); !errors.Is(err, ErrResponseNoteNotAllowed) || store.responseNote != "" {
		t.Fatalf("confirmed response accepted note: %q / %v", store.responseNote, err)
	}
	if _, err := service.Respond(t.Context(), material.Raw, material.FormNonce, ResponseDeclined, strings.Repeat("x", 501), "request"); !errors.Is(err, ErrConfirmationUnavailable) {
		t.Fatalf("oversized response note accepted: %v", err)
	}
	for _, test := range []struct {
		name     string
		token    string
		nonce    string
		response Response
	}{
		{name: "unsupported response", token: material.Raw, nonce: material.FormNonce, response: "accepted"},
		{name: "invalid token", token: "invalid", nonce: material.FormNonce, response: ResponseDeclined},
		{name: "invalid nonce", token: material.Raw, nonce: "invalid", response: ResponseCallback},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Respond(t.Context(), test.token, test.nonce, test.response, "", "request"); !errors.Is(err, ErrConfirmationUnavailable) {
				t.Fatalf("Respond() error = %v", err)
			}
		})
	}
	store.respondErr = ErrResponseLocked
	if _, err := service.Respond(t.Context(), material.Raw, material.FormNonce, ResponseDeclined, "", "request"); !errors.Is(err, ErrResponseLocked) {
		t.Fatalf("Respond() store error = %v", err)
	}
	if _, err := NewConfirmationService(nil, ring, nil); err == nil {
		t.Fatal("NewConfirmationService accepted nil store")
	}
}
