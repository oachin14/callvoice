package csvimport_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/callvoice/callvoice/services/api/internal/csvimport"
)

func TestParseValidPhonesAndExtras(t *testing.T) {
	t.Parallel()

	csv := "phone,name,city\n+33 6 12 34 56 78,Alice,Paris\n06.11.22.33.44,Bob,Lyon\n"
	rows, errs, err := csvimport.Parse(strings.NewReader(csv))
	require.NoError(t, err)
	require.Empty(t, errs)
	require.Len(t, rows, 2)

	require.Equal(t, "+33612345678", rows[0].Phone)
	require.Equal(t, "Alice", rows[0].Payload["name"])
	require.Equal(t, "Paris", rows[0].Payload["city"])

	require.Equal(t, "+0611223344", rows[1].Phone)
	require.Equal(t, "Bob", rows[1].Payload["name"])
}

func TestParsePhoneHeaderAliases(t *testing.T) {
	t.Parallel()

	for _, header := range []string{"téléphone", "mobile", "Telephone"} {
		header := header
		t.Run(header, func(t *testing.T) {
			t.Parallel()
			csv := header + "\n0611223344\n"
			rows, errs, err := csvimport.Parse(strings.NewReader(csv))
			require.NoError(t, err)
			require.Empty(t, errs)
			require.Len(t, rows, 1)
			require.Equal(t, "+0611223344", rows[0].Phone)
		})
	}
}

func TestParseInvalidPhoneContinues(t *testing.T) {
	t.Parallel()

	csv := "phone\ninvalid\n+33612345678\n\n"
	rows, errs, err := csvimport.Parse(strings.NewReader(csv))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "+33612345678", rows[0].Phone)
	require.Len(t, errs, 1)
	require.Equal(t, 2, errs[0].Line)
	require.Equal(t, "invalid_phone", errs[0].Reason)
}

func TestParseMissingPhoneColumn(t *testing.T) {
	t.Parallel()

	_, _, err := csvimport.Parse(strings.NewReader("name,city\nAlice,Paris\n"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing phone column")
}
