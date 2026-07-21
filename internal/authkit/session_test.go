package authkit_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/callvoice/callvoice/internal/authkit"
)

func TestSessionTokenHashed(t *testing.T) {
	plain, hash, err := authkit.NewSessionToken()
	require.NoError(t, err)
	require.NotEqual(t, plain, hash)
	require.Equal(t, authkit.HashToken(plain), hash)
}

func TestPendingTokenRoundTrip(t *testing.T) {
	secret := []byte("dev-session-secret-change-me-32b!!")
	userID := uuid.New()
	token, err := authkit.NewPendingToken(userID, secret, 5*time.Minute)
	require.NoError(t, err)

	got, err := authkit.ParsePendingToken(token, secret)
	require.NoError(t, err)
	require.Equal(t, userID, got)

	_, err = authkit.ParsePendingToken(token, []byte("wrong-secret-that-is-long-enough!!"))
	require.Error(t, err)
}
