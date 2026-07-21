package authkit

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const sessionTokenBytes = 32

// NewSessionToken returns a random opaque token and its SHA-256 hex hash for storage.
func NewSessionToken() (plain string, hash string, err error) {
	buf := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generate session token: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(buf)
	return plain, HashToken(plain), nil
}

// HashToken returns the SHA-256 hex digest of a plain session token.
func HashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// NewPendingToken creates a short-lived HMAC-signed pending-login token for userID.
func NewPendingToken(userID uuid.UUID, secret []byte, ttl time.Duration) (string, error) {
	if len(secret) < 32 {
		return "", errors.New("session secret must be at least 32 bytes")
	}
	expires := time.Now().UTC().Add(ttl).Unix()
	payload := userID.String() + "|" + strconv.FormatInt(expires, 10)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(payload + "|" + sig)), nil
}

// ParsePendingToken validates a pending-login token and returns the user ID.
func ParsePendingToken(token string, secret []byte) (uuid.UUID, error) {
	if len(secret) < 32 {
		return uuid.Nil, errors.New("session secret must be at least 32 bytes")
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return uuid.Nil, fmt.Errorf("decode pending token: %w", err)
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return uuid.Nil, errors.New("invalid pending token format")
	}
	userID, err := uuid.Parse(parts[0])
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse user id: %w", err)
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return uuid.Nil, fmt.Errorf("parse expiry: %w", err)
	}
	if time.Now().UTC().Unix() > expires {
		return uuid.Nil, errors.New("pending token expired")
	}

	payload := parts[0] + "|" + parts[1]
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return uuid.Nil, errors.New("invalid pending token signature")
	}
	return userID, nil
}
