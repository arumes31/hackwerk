package auth

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

func TestSecurityKeyRingEncryptsByDomainAndReconstructsEmailToken(t *testing.T) {
	t.Parallel()
	keys := map[string]string{
		"old": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x11}, 32)),
		"new": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x22}, 32)),
	}
	ring, err := NewSecurityKeyRing(keys, "new")
	if err != nil {
		t.Fatal(err)
	}

	raw, hash, err := ring.emailToken("verification", "user", 2)
	if err != nil {
		t.Fatal(err)
	}
	reconstructed, err := ring.ReconstructEmailToken("new", "verification", "user", 2)
	if err != nil {
		t.Fatal(err)
	}
	if raw != reconstructed || !bytes.Equal(hash, TokenHash(raw)) {
		t.Fatalf("reconstructed token mismatch")
	}
	if _, err := ring.ReconstructEmailToken("missing", "verification", "user", 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing key error = %v", err)
	}

	keyID, ciphertext, err := ring.encrypt("totp:user", []byte("secret-value"))
	if err != nil {
		t.Fatal(err)
	}
	if keyID != "new" || bytes.Contains(ciphertext, []byte("secret-value")) {
		t.Fatalf("credential was not protected")
	}
	plaintext, err := ring.decrypt(keyID, "totp:user", ciphertext)
	if err != nil || string(plaintext) != "secret-value" {
		t.Fatalf("decrypt = %q, %v", plaintext, err)
	}
	if _, err := ring.decrypt(keyID, "webauthn:user", ciphertext); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("cross-domain decrypt error = %v", err)
	}
}

func TestProfileNormalization(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		input     string
		normalize func(string) (string, error)
		want      string
		wantError bool
	}{
		{name: "email trims and lowercases domain", input: " User.Name@EXAMPLE.AT ", normalize: normalizeEmail, want: "User.Name@example.at"},
		{name: "email rejects display name", input: "Daniel <daniel@example.at>", normalize: normalizeEmail, wantError: true},
		{name: "phone converts Austrian trunk prefix", input: "0664 / 123 45 67", normalize: normalizePhone, want: "+436641234567"},
		{name: "phone converts international prefix", input: "0043 664 1234567", normalize: normalizePhone, want: "+436641234567"},
		{name: "phone rejects duplicate plus", input: "+43+6641234567", normalize: normalizePhone, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := test.normalize(test.input)
			if (err != nil) != test.wantError || got != test.want {
				t.Fatalf("normalize(%q) = %q, %v; want %q, error=%v", test.input, got, err, test.want, test.wantError)
			}
		})
	}
}

func TestDeviceLabelIsCoarseAndBounded(t *testing.T) {
	t.Parallel()
	userAgent := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/128.0.0.0 Safari/537.36 Edg/128.0.0.0"
	label := DeviceLabel(userAgent)
	if label != "Edge auf Windows" || strings.Contains(label, "128") || strings.Contains(label, "Mozilla") {
		t.Fatalf("device label = %q", label)
	}
	if got := normalizeDeviceLabel(strings.Repeat("ä", 130)); len([]rune(got)) != 120 {
		t.Fatalf("bounded label has %d runes", len([]rune(got)))
	}
}

func TestTOTPValidationAcceptsLimitedSkew(t *testing.T) {
	t.Parallel()
	// #nosec G101 -- public RFC-style deterministic test fixture, not a deployed credential.
	const sharedTestKey = "JBSWY3DPEHPK3PXP"
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	options := totp.ValidateOpts{Period: totpPeriodSeconds, Skew: 0, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}
	code, err := totp.GenerateCodeCustom(sharedTestKey, now.Add(-30*time.Second), options)
	if err != nil {
		t.Fatal(err)
	}
	step, valid := validateTOTP(sharedTestKey, code, now)
	if !valid || step != now.Add(-30*time.Second).Unix()/totpPeriodSeconds {
		t.Fatalf("skew code rejected: step=%d valid=%v", step, valid)
	}
	if _, valid := validateTOTP(sharedTestKey, "12345x", now); valid {
		t.Fatal("invalid TOTP accepted")
	}
}

func TestRecoveryCodesAreUniqueAndHashOnly(t *testing.T) {
	t.Parallel()
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != recoveryCodeCount || len(hashes) != recoveryCodeCount {
		t.Fatalf("got %d codes and %d hashes", len(codes), len(hashes))
	}
	seen := make(map[string]struct{}, recoveryCodeCount)
	for index, code := range codes {
		normalized := normalizeRecoveryCode(strings.ToLower(code))
		if len(normalized) != 12 || !bytes.Equal(hashes[index], TokenHash("recovery\x00"+normalized)) {
			t.Fatalf("invalid recovery code at %d", index)
		}
		if _, duplicate := seen[normalized]; duplicate {
			t.Fatalf("duplicate recovery code")
		}
		seen[normalized] = struct{}{}
		if bytes.Contains(hashes[index], []byte(normalized)) {
			t.Fatalf("recovery code persisted in clear text")
		}
	}
}

func TestCredentialIDRoundTripAndBounds(t *testing.T) {
	t.Parallel()
	id := bytes.Repeat([]byte{0x5a}, 32)
	decoded, err := decodeCredentialID(credentialID(id))
	if err != nil || !bytes.Equal(decoded, id) {
		t.Fatalf("credential round trip = %x, %v", decoded, err)
	}
	if _, err := decodeCredentialID(base64.RawURLEncoding.EncodeToString([]byte("short"))); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("short credential error = %v", err)
	}
}
