package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const argon2Version = 19

// PasswordParameters are stored in every PHC hash and can be upgraded on login.
type PasswordParameters struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
	MinLength   int
}

// PasswordHasher creates and verifies versioned Argon2id hashes.
type PasswordHasher struct {
	parameters PasswordParameters
}

// NewPasswordHasher validates password hashing parameters.
func NewPasswordHasher(parameters PasswordParameters) (PasswordHasher, error) {
	if parameters.MemoryKiB < 8 || parameters.Iterations < 1 || parameters.Parallelism < 1 ||
		parameters.SaltLength < 16 || parameters.KeyLength < 16 || parameters.MinLength < 12 {
		return PasswordHasher{}, errors.New("auth: invalid password hashing parameters")
	}
	return PasswordHasher{parameters: parameters}, nil
}

// ValidatePassword applies a length-focused policy without brittle composition rules.
func (hasher PasswordHasher) ValidatePassword(password string) error {
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < hasher.parameters.MinLength || len(password) > 1024 {
		return ErrWeakPassword
	}
	normalized := strings.ToLower(strings.TrimSpace(password))
	if normalized == "passwort123456" || normalized == "hackplan123456" || normalized == "hackwerk123456" || normalized == strings.Repeat("1", hasher.parameters.MinLength) {
		return ErrWeakPassword
	}
	return nil
}

// Hash returns a PHC-formatted Argon2id hash with a fresh random salt.
func (hasher PasswordHasher) Hash(password string) (string, error) {
	if err := hasher.ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, hasher.parameters.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generating password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, hasher.parameters.Iterations, hasher.parameters.MemoryKiB, hasher.parameters.Parallelism, hasher.parameters.KeyLength)
	encoding := base64.RawStdEncoding
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2Version,
		hasher.parameters.MemoryKiB,
		hasher.parameters.Iterations,
		hasher.parameters.Parallelism,
		encoding.EncodeToString(salt),
		encoding.EncodeToString(key),
	), nil
}

// Verify compares in constant time and reports whether the hash needs current parameters.
func (hasher PasswordHasher) Verify(password string, encodedHash string) (valid bool, needsRehash bool, err error) {
	parameters, salt, expected, err := parsePasswordHash(encodedHash)
	if err != nil {
		return false, false, err
	}
	actual := argon2.IDKey([]byte(password), salt, parameters.Iterations, parameters.MemoryKiB, parameters.Parallelism, uint32(len(expected)))
	valid = subtle.ConstantTimeCompare(actual, expected) == 1
	needsRehash = valid && !sameHashParameters(parameters, hasher.parameters)
	return valid, needsRehash, nil
}

func sameHashParameters(left PasswordParameters, right PasswordParameters) bool {
	return left.MemoryKiB == right.MemoryKiB &&
		left.Iterations == right.Iterations &&
		left.Parallelism == right.Parallelism &&
		left.SaltLength == right.SaltLength &&
		left.KeyLength == right.KeyLength
}

func parsePasswordHash(encodedHash string) (PasswordParameters, []byte, []byte, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return PasswordParameters{}, nil, nil, errors.New("auth: malformed password hash")
	}
	var parameters PasswordParameters
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &parameters.MemoryKiB, &parameters.Iterations, &parameters.Parallelism); err != nil {
		return PasswordParameters{}, nil, nil, errors.New("auth: malformed password parameters")
	}
	encoding := base64.RawStdEncoding
	salt, err := encoding.DecodeString(parts[4])
	if err != nil {
		return PasswordParameters{}, nil, nil, errors.New("auth: malformed password salt")
	}
	key, err := encoding.DecodeString(parts[5])
	if err != nil {
		return PasswordParameters{}, nil, nil, errors.New("auth: malformed password key")
	}
	parameters.SaltLength = uint32(len(salt))
	parameters.KeyLength = uint32(len(key))
	parameters.MinLength = 0
	if parameters.MemoryKiB < 8 || parameters.Iterations < 1 || parameters.Parallelism < 1 || len(salt) < 16 || len(key) < 16 {
		return PasswordParameters{}, nil, nil, errors.New("auth: invalid password hash parameters")
	}
	return parameters, salt, key, nil
}
