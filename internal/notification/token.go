// Package notification implements secure customer confirmations and outbound delivery.
package notification

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
)

const DevelopmentKeyID = "development-v1"

var ErrInvalidToken = errors.New("notification: invalid token")

type KeyRing struct {
	current string
	keys    map[string][]byte
}

type TokenMaterial struct {
	Raw, FormNonce  string
	Hash, NonceHash []byte
	KeyID           string
}

func NewKeyRing(encoded map[string]string, current string) (*KeyRing, error) {
	if current == "" || len(encoded) == 0 {
		return nil, errors.New("notification: token keyring is required")
	}
	keys := make(map[string][]byte, len(encoded))
	for id, value := range encoded {
		key, err := base64.StdEncoding.DecodeString(value)
		if err != nil || len(key) < 32 || id == "" {
			return nil, errors.New("notification: invalid token keyring")
		}
		keys[id] = key
	}
	if _, ok := keys[current]; !ok {
		return nil, errors.New("notification: current token key is missing")
	}
	return &KeyRing{current: current, keys: keys}, nil
}

func DevelopmentKeyRing() *KeyRing {
	ring, err := NewKeyRing(map[string]string{DevelopmentKeyID: "ZGV2ZWxvcG1lbnQtb25seS1oYWNrd2Vyay10b2tlbi1rZXk="}, DevelopmentKeyID)
	if err != nil {
		panic(err)
	}
	return ring
}

func (ring *KeyRing) Issue(requestID, appointmentID string, version int32) (TokenMaterial, error) {
	return ring.derive(ring.current, requestID, appointmentID, version)
}

func (ring *KeyRing) Reconstruct(keyID, requestID, appointmentID string, version int32) (TokenMaterial, error) {
	return ring.derive(keyID, requestID, appointmentID, version)
}

func (ring *KeyRing) derive(keyID, requestID, appointmentID string, version int32) (TokenMaterial, error) {
	key, ok := ring.keys[keyID]
	if !ok || requestID == "" || appointmentID == "" || version < 1 {
		return TokenMaterial{}, ErrInvalidToken
	}
	rawBytes := digest(key, "confirmation:v1", requestID, appointmentID, strconv.FormatInt(int64(version), 10))
	nonceBytes := digest(key, "confirmation-form:v1", base64.RawURLEncoding.EncodeToString(rawBytes))
	raw := base64.RawURLEncoding.EncodeToString(rawBytes)
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	hash := sha256.Sum256(rawBytes)
	nonceHash := sha256.Sum256(nonceBytes)
	return TokenMaterial{Raw: raw, FormNonce: nonce, Hash: hash[:], NonceHash: nonceHash[:], KeyID: keyID}, nil
}

func HashRawToken(raw string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) != sha256.Size {
		return nil, ErrInvalidToken
	}
	hash := sha256.Sum256(decoded)
	return hash[:], nil
}

func HashFormNonce(raw string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) != sha256.Size {
		return nil, ErrInvalidToken
	}
	hash := sha256.Sum256(decoded)
	return hash[:], nil
}

func ConstantTimeEqual(left, right []byte) bool {
	return len(left) == sha256.Size && len(right) == sha256.Size && subtle.ConstantTimeCompare(left, right) == 1
}

func digest(key []byte, values ...string) []byte {
	mac := hmac.New(sha256.New, key)
	for _, value := range values {
		_, _ = fmt.Fprintf(mac, "%d:", len(value))
		_, _ = mac.Write([]byte(value))
	}
	return mac.Sum(nil)
}
