package csvimport

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"unicode"
)

type LeadRow struct {
	Line    int
	Phone   string
	Payload map[string]string
}

type RowError struct {
	Line   int
	Reason string
}

var phoneHeaderAliases = map[string]struct{}{
	"phone":     {},
	"téléphone": {},
	"telephone": {},
	"mobile":    {},
}

// Parse reads CSV rows, normalizes phone numbers to E.164 (+digits), and collects row errors.
func Parse(r io.Reader) ([]LeadRow, []RowError, error) {
	reader := csv.NewReader(r)
	reader.TrimLeadingSpace = true

	headers, err := reader.Read()
	if err != nil {
		return nil, nil, err
	}

	phoneIdx := -1
	for i, h := range headers {
		if _, ok := phoneHeaderAliases[normalizeHeader(h)]; ok {
			phoneIdx = i
			break
		}
	}
	if phoneIdx < 0 {
		return nil, nil, fmt.Errorf("missing phone column")
	}

	var rows []LeadRow
	var errs []RowError
	line := 2

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return rows, errs, err
		}

		if len(record) == 0 || allEmpty(record) {
			line++
			continue
		}

		phoneRaw := ""
		if phoneIdx < len(record) {
			phoneRaw = record[phoneIdx]
		}
		phone, ok := normalizePhone(phoneRaw)
		if !ok {
			errs = append(errs, RowError{Line: line, Reason: "invalid_phone"})
			line++
			continue
		}

		payload := make(map[string]string)
		for i, h := range headers {
			if i == phoneIdx {
				continue
			}
			key := strings.TrimSpace(h)
			if key == "" {
				continue
			}
			val := ""
			if i < len(record) {
				val = strings.TrimSpace(record[i])
			}
			if val != "" {
				payload[key] = val
			}
		}

		rows = append(rows, LeadRow{
			Line:    line,
			Phone:   phone,
			Payload: payload,
		})
		line++
	}

	return rows, errs, nil
}

func normalizeHeader(h string) string {
	return strings.ToLower(strings.TrimSpace(h))
}

func normalizePhone(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}

	var digits strings.Builder
	for _, r := range raw {
		if unicode.IsDigit(r) {
			digits.WriteRune(r)
		}
	}
	d := digits.String()
	if len(d) < 8 || len(d) > 15 {
		return "", false
	}
	return "+" + d, true
}

func allEmpty(record []string) bool {
	for _, v := range record {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}
