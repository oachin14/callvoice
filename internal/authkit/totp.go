package authkit

import (
	"fmt"
	"net/url"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const totpIssuer = "CallVoice"

// GenerateTOTPSecret returns a new base32-encoded TOTP shared secret.
func GenerateTOTPSecret() (string, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: "setup",
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", fmt.Errorf("generate totp secret: %w", err)
	}
	return key.Secret(), nil
}

// OTPAuthURL builds an otpauth:// URL for authenticator apps (issuer CallVoice).
func OTPAuthURL(secret, email string) string {
	v := url.Values{}
	v.Set("secret", secret)
	v.Set("issuer", totpIssuer)
	v.Set("algorithm", "SHA1")
	v.Set("digits", "6")
	v.Set("period", "30")
	label := url.PathEscape(totpIssuer + ":" + email)
	return "otpauth://totp/" + label + "?" + v.Encode()
}

// ValidateTOTP reports whether code is a valid TOTP for secret at the current time.
func ValidateTOTP(secret, code string) bool {
	return totp.Validate(code, secret)
}

// GenerateTOTPCode returns the current TOTP code for secret (test / seed helpers).
func GenerateTOTPCode(secret string) (string, error) {
	return totp.GenerateCode(secret, time.Now())
}
