package authkit_test

import (
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/require"

	"github.com/callvoice/callvoice/internal/authkit"
)

func TestTOTPValidate(t *testing.T) {
	secret := "JBSWY3DPEHPK3PXP"
	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	require.True(t, authkit.ValidateTOTP(secret, code))
	require.False(t, authkit.ValidateTOTP(secret, "000000"))
}

func TestGenerateTOTPSecret(t *testing.T) {
	secret, err := authkit.GenerateTOTPSecret()
	require.NoError(t, err)
	require.NotEmpty(t, secret)

	code, err := totp.GenerateCode(secret, time.Now())
	require.NoError(t, err)
	require.True(t, authkit.ValidateTOTP(secret, code))
}

func TestOTPAuthURL(t *testing.T) {
	url := authkit.OTPAuthURL("JBSWY3DPEHPK3PXP", "admin@callvoice.local")
	require.True(t, strings.HasPrefix(url, "otpauth://totp/"))
	require.Contains(t, url, "secret=JBSWY3DPEHPK3PXP")
	require.Contains(t, url, "issuer=CallVoice")
	require.Contains(t, url, "admin@callvoice.local")
}
