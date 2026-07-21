package authkit_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/callvoice/callvoice/internal/authkit"
)

func TestHashAndVerify(t *testing.T) {
	h, err := authkit.HashPassword("correct horse")
	require.NoError(t, err)
	require.True(t, authkit.VerifyPassword(h, "correct horse"))
	require.False(t, authkit.VerifyPassword(h, "wrong"))
}
