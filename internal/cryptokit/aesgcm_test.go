package cryptokit_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/callvoice/callvoice/internal/cryptokit"
)

func TestEncryptDecrypt(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	ct, err := cryptokit.Encrypt(key, []byte("sip-secret"))
	require.NoError(t, err)
	pt, err := cryptokit.Decrypt(key, ct)
	require.NoError(t, err)
	require.Equal(t, "sip-secret", string(pt))
}

func TestEncryptProducesDifferentCiphertexts(t *testing.T) {
	key := bytes.Repeat([]byte{2}, 32)
	ct1, err := cryptokit.Encrypt(key, []byte("same"))
	require.NoError(t, err)
	ct2, err := cryptokit.Encrypt(key, []byte("same"))
	require.NoError(t, err)
	require.NotEqual(t, ct1, ct2)
}

func TestDecryptRejectsTamperedBlob(t *testing.T) {
	key := bytes.Repeat([]byte{3}, 32)
	ct, err := cryptokit.Encrypt(key, []byte("secret"))
	require.NoError(t, err)
	ct[len(ct)-1] ^= 0xff
	_, err = cryptokit.Decrypt(key, ct)
	require.Error(t, err)
}

func TestParseKeyHexAndRaw(t *testing.T) {
	raw := bytes.Repeat([]byte{0xab}, 32)
	got, err := cryptokit.ParseKey(string(raw))
	require.NoError(t, err)
	require.Equal(t, raw, got)

	hexKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	got, err = cryptokit.ParseKey(hexKey)
	require.NoError(t, err)
	require.Len(t, got, 32)

	_, err = cryptokit.ParseKey("too-short")
	require.Error(t, err)
}
