package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrTokenEntropyTooLow = errors.New("token entropy must be at least 128 bits")
	ErrTokenRequired      = errors.New("token is required")
	ErrPepperRequired     = errors.New("pepper is required")
	ErrMalformedHash      = errors.New("malformed token hash")
)

const minTokenBytes = 16

func GenerateToken(prefix string, randomBytes int) (string, error) {
	if randomBytes < minTokenBytes {
		return "", ErrTokenEntropyTooLow
	}
	buf := make([]byte, randomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate token randomness: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(buf)
	if prefix == "" {
		return secret, nil
	}
	return prefix + "_" + secret, nil
}

func HashToken(raw string, pepper string) (string, error) {
	if raw == "" {
		return "", ErrTokenRequired
	}
	if pepper == "" {
		return "", ErrPepperRequired
	}
	sum := sha256.Sum256([]byte(pepper + ":" + raw))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func VerifyToken(raw string, storedHash string, pepper string) (bool, error) {
	if !strings.HasPrefix(storedHash, "sha256:") {
		return false, ErrMalformedHash
	}
	expectedHex := strings.TrimPrefix(storedHash, "sha256:")
	expected, err := hex.DecodeString(expectedHex)
	if err != nil || len(expected) != sha256.Size {
		return false, ErrMalformedHash
	}
	actualHash, err := HashToken(raw, pepper)
	if err != nil {
		return false, err
	}
	actual, err := hex.DecodeString(strings.TrimPrefix(actualHash, "sha256:"))
	if err != nil || len(actual) != sha256.Size {
		return false, ErrMalformedHash
	}
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}
