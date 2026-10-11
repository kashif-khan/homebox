package fieldtypes

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalize_Legacy(t *testing.T) {
	v, err := Normalize(Input{Type: "text", TextValue: "hello"})
	require.NoError(t, err)
	assert.Equal(t, "hello", v.TextValue)

	v, err = Normalize(Input{Type: "number", NumberValue: 7})
	require.NoError(t, err)
	assert.Equal(t, 7, v.NumberValue)

	v, err = Normalize(Input{Type: "boolean", BooleanValue: true})
	require.NoError(t, err)
	assert.True(t, v.BooleanValue)
}

func TestNormalize_Unknown(t *testing.T) {
	_, err := Normalize(Input{Type: "geo"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalid))
}

func TestNormalize_Date(t *testing.T) {
	v, err := Normalize(Input{Type: "date", TextValue: "2027-03-01"})
	require.NoError(t, err)
	assert.Equal(t, "2027-03-01", v.TextValue)
	require.NotNil(t, v.TimeValue)
	assert.Equal(t, time.UTC, v.TimeValue.Location())
	assert.Equal(t, 0, v.TimeValue.Hour())

	for _, bad := range []string{"01/03/2027", "2027-13-01", "2027-02-30", "tomorrow"} {
		_, err := Normalize(Input{Type: "date", TextValue: bad})
		assert.Error(t, err, bad)
	}

	v, err = Normalize(Input{Type: "date"})
	require.NoError(t, err, "empty means unset")
	assert.Nil(t, v.TimeValue)
}

func TestNormalize_DateTime(t *testing.T) {
	// An explicit offset is kept in the text and normalised to UTC underneath.
	v, err := Normalize(Input{Type: "datetime", TextValue: "2026-10-10T08:00:00+05:30"})
	require.NoError(t, err)
	assert.Equal(t, "2026-10-10T08:00:00+05:30", v.TextValue)
	assert.Equal(t, time.Date(2026, 10, 10, 2, 30, 0, 0, time.UTC), *v.TimeValue)

	// A zone-less value is read in the stated zone.
	v, err = Normalize(Input{Type: "datetime", TextValue: "2026-10-10T08:00", Timezone: "Asia/Karachi"})
	require.NoError(t, err)
	assert.Equal(t, "2026-10-10T08:00:00+05:00", v.TextValue)
	assert.Equal(t, time.Date(2026, 10, 10, 3, 0, 0, 0, time.UTC), *v.TimeValue)
	assert.Equal(t, "Asia/Karachi", v.Timezone)

	_, err = Normalize(Input{Type: "datetime", TextValue: "2026-10-10T08:00", Timezone: "Mars/Base"})
	assert.Error(t, err)
	_, err = Normalize(Input{Type: "datetime", TextValue: "nope"})
	assert.Error(t, err)
}

func TestNormalize_Time(t *testing.T) {
	v, err := Normalize(Input{Type: "time", TextValue: "14:30"})
	require.NoError(t, err)
	assert.Equal(t, "14:30", v.TextValue)
	for _, bad := range []string{"24:00", "9:30", "14:60", "noon"} {
		_, err := Normalize(Input{Type: "time", TextValue: bad})
		assert.Error(t, err, bad)
	}
}

func TestNormalize_Decimal(t *testing.T) {
	v, err := Normalize(Input{Type: "decimal", TextValue: "12.50"})
	require.NoError(t, err)
	assert.Equal(t, "12.5", v.TextValue)
	assert.InDelta(t, 12.5, *v.DecimalValue, 1e-9)
	for _, bad := range []string{"1,5", "1e3", "NaN", "abc", "--1"} {
		_, err := Normalize(Input{Type: "decimal", TextValue: bad})
		assert.Error(t, err, bad)
	}
}

func TestNormalize_Currency(t *testing.T) {
	v, err := Normalize(Input{Type: "currency", TextValue: "12.5", CurrencyCode: "pkr"})
	require.NoError(t, err)
	assert.Equal(t, 1250, v.NumberValue)
	assert.Equal(t, "12.50", v.TextValue)
	assert.Equal(t, "PKR", v.CurrencyCode)

	// Zero-decimal currencies.
	v, err = Normalize(Input{Type: "currency", TextValue: "500", CurrencyCode: "JPY"})
	require.NoError(t, err)
	assert.Equal(t, 500, v.NumberValue)
	assert.Equal(t, "500", v.TextValue)

	v, err = Normalize(Input{Type: "currency", TextValue: "-0.05", CurrencyCode: "USD"})
	require.NoError(t, err)
	assert.Equal(t, -5, v.NumberValue)
	assert.Equal(t, "-0.05", v.TextValue)

	_, err = Normalize(Input{Type: "currency", TextValue: "12.345", CurrencyCode: "USD"})
	assert.Error(t, err, "too many decimals")
	_, err = Normalize(Input{Type: "currency", TextValue: "12", CurrencyCode: "ZZZ"})
	assert.Error(t, err, "unsupported currency")
	_, err = Normalize(Input{Type: "currency", TextValue: "12"})
	assert.Error(t, err, "amount without currency")

	v, err = Normalize(Input{Type: "currency", CurrencyCode: "EUR"})
	require.NoError(t, err, "currency may be chosen before an amount")
	assert.Equal(t, "EUR", v.CurrencyCode)
}

func TestFormatMinorUnits(t *testing.T) {
	assert.Equal(t, "12.50", FormatMinorUnits(1250, "USD"))
	assert.Equal(t, "0.05", FormatMinorUnits(5, "USD"))
	assert.Equal(t, "-0.05", FormatMinorUnits(-5, "USD"))
	assert.Equal(t, "500", FormatMinorUnits(500, "JPY"))
}

func TestNormalize_URL(t *testing.T) {
	v, err := Normalize(Input{Type: "url", TextValue: "https://example.com/a?b=1"})
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/a?b=1", v.TextValue)
	for _, bad := range []string{"example.com", "javascript:alert(1)", "ftp://x.y", "https://"} {
		_, err := Normalize(Input{Type: "url", TextValue: bad})
		assert.Error(t, err, bad)
	}
}

func TestNormalize_Select(t *testing.T) {
	v, err := Normalize(Input{Type: "select", TextValue: "b", Choices: []string{"a", " b ", "b", ""}})
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, v.Choices)
	assert.Equal(t, "b", v.TextValue)

	_, err = Normalize(Input{Type: "select", TextValue: "z", Choices: []string{"a"}})
	assert.Error(t, err)

	v, err = Normalize(Input{Type: "select", Choices: []string{"a"}})
	require.NoError(t, err, "unset is allowed")
	assert.Empty(t, v.TextValue)
}
