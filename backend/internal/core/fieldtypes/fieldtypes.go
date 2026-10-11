// Package fieldtypes validates and normalises custom field values.
//
// Clients send a typed field as a type plus one canonical string (an ISO date, a
// decimal amount, a URL and so on). Normalize parses it, rejects bad input and
// returns both the canonical text and the typed columns derived from it, so the
// database can sort and filter on real types while search, CSV export and the
// UI keep working from the text.
package fieldtypes

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sysadminsmedia/homebox/backend/internal/core/currencies"
	"github.com/sysadminsmedia/homebox/backend/internal/core/regional"
)

// Type is a custom field's data type.
type Type string

const (
	Text     Type = "text"
	Number   Type = "number" // whole number; the original numeric type
	Boolean  Type = "boolean"
	Time     Type = "time" // wall-clock time of day, "HH:MM[:SS]"
	Decimal  Type = "decimal"
	Currency Type = "currency"
	Date     Type = "date"
	DateTime Type = "datetime"
	URL      Type = "url"
	Select   Type = "select"
)

// All lists every supported type, in the order the UI offers them.
var All = []Type{Text, Number, Decimal, Currency, Boolean, Date, Time, DateTime, URL, Select}

const (
	MaxTextLen = 500
	MaxChoices = 50
)

// ErrInvalid wraps every validation failure so callers can map it to a 4xx.
var ErrInvalid = errors.New("invalid custom field")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Input is what a client sends.
type Input struct {
	Type         string
	Name         string
	TextValue    string
	NumberValue  int
	BooleanValue bool
	CurrencyCode string
	Timezone     string
	Choices      []string
}

// Value is the normalised result, ready to persist.
type Value struct {
	Type         Type
	TextValue    string
	NumberValue  int
	BooleanValue bool
	DecimalValue *float64
	TimeValue    *time.Time
	CurrencyCode string
	Timezone     string
	Choices      []string
}

// Valid reports whether t is a known type.
func Valid(t string) bool {
	for _, k := range All {
		if string(k) == t {
			return true
		}
	}
	return false
}

var (
	decimalRe = regexp.MustCompile(`^-?\d{1,15}(\.\d{1,15})?$`)
	clockRe   = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)(:([0-5]\d))?$`)
	codeRe    = regexp.MustCompile(`^[A-Z]{3}$`)
)

// Normalize validates in and derives the typed columns. An empty TextValue on
// a text-backed type means "no value yet" and is allowed, so a template can
// declare a field without a default.
func Normalize(in Input) (Value, error) {
	t := Type(in.Type)
	if !Valid(in.Type) {
		return Value{}, invalid("unknown type %q", in.Type)
	}

	v := Value{Type: t}
	text := strings.TrimSpace(in.TextValue)

	switch t {
	case Text:
		v.TextValue = in.TextValue
		if len([]rune(v.TextValue)) > MaxTextLen {
			return Value{}, invalid("text is longer than %d characters", MaxTextLen)
		}
		// The original three types always stored all three columns whatever the
		// type said; clients rely on getting them back unchanged.
		v.NumberValue = in.NumberValue
		v.BooleanValue = in.BooleanValue
		return v, nil
	case Number:
		v.TextValue = in.TextValue
		v.NumberValue = in.NumberValue
		v.BooleanValue = in.BooleanValue
		return v, nil
	case Boolean:
		v.TextValue = in.TextValue
		v.NumberValue = in.NumberValue
		v.BooleanValue = in.BooleanValue
		return v, nil
	}

	// Every remaining type is carried as a canonical string.
	switch t {
	case Time:
		if text == "" {
			return v, nil
		}
		m := clockRe.FindStringSubmatch(text)
		if m == nil {
			return Value{}, invalid("time must look like 14:30")
		}
		v.TextValue = text
	case Decimal:
		if text == "" {
			return v, nil
		}
		if !decimalRe.MatchString(text) {
			return Value{}, invalid("decimal must be a plain number such as 12.5")
		}
		f, err := strconv.ParseFloat(text, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return Value{}, invalid("decimal is out of range")
		}
		v.DecimalValue = &f
		v.TextValue = strconv.FormatFloat(f, 'f', -1, 64)
	case Currency:
		code := strings.ToUpper(strings.TrimSpace(in.CurrencyCode))
		if code != "" {
			if _, ok := decimalsFor(code); !ok {
				return Value{}, invalid("unsupported currency %q", in.CurrencyCode)
			}
			v.CurrencyCode = code
		}
		if text == "" {
			return v, nil
		}
		if v.CurrencyCode == "" {
			return Value{}, invalid("a currency code is required with an amount")
		}
		minor, canonical, err := parseMinorUnits(text, v.CurrencyCode)
		if err != nil {
			return Value{}, err
		}
		v.NumberValue = minor
		v.TextValue = canonical
	case Date:
		if text == "" {
			return v, nil
		}
		d, err := time.Parse(time.DateOnly, text)
		if err != nil {
			return Value{}, invalid("date must look like 2026-12-31")
		}
		v.TimeValue = &d
		v.TextValue = d.Format(time.DateOnly)
	case DateTime:
		tz := strings.TrimSpace(in.Timezone)
		if tz != "" {
			if regional.Timezone(tz) == "" {
				return Value{}, invalid("unknown time zone %q", in.Timezone)
			}
			v.Timezone = tz
		}
		if text == "" {
			return v, nil
		}
		inst, err := parseDateTime(text, v.Timezone)
		if err != nil {
			return Value{}, err
		}
		utc := inst.UTC()
		v.TimeValue = &utc
		loc := time.UTC
		if v.Timezone != "" {
			loc = regional.Location(v.Timezone)
		}
		// With a zone set, show the instant in it. Without one, keep the offset
		// the client wrote so the wall-clock time round-trips.
		if v.Timezone != "" {
			v.TextValue = inst.In(loc).Format(time.RFC3339)
		} else {
			v.TextValue = inst.Format(time.RFC3339)
		}
	case URL:
		if text == "" {
			return v, nil
		}
		if len(text) > MaxTextLen {
			return Value{}, invalid("url is longer than %d characters", MaxTextLen)
		}
		u, err := url.Parse(text)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return Value{}, invalid("url must start with http:// or https://")
		}
		v.TextValue = u.String()
	case Select:
		choices, err := cleanChoices(in.Choices)
		if err != nil {
			return Value{}, err
		}
		v.Choices = choices
		if text == "" {
			return v, nil
		}
		for _, c := range choices {
			if c == text {
				v.TextValue = text
				return v, nil
			}
		}
		return Value{}, invalid("%q is not one of the allowed choices", text)
	}
	return v, nil
}

func cleanChoices(in []string) ([]string, error) {
	if len(in) > MaxChoices {
		return nil, invalid("a select field can have at most %d choices", MaxChoices)
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, c := range in {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if len([]rune(c)) > 100 {
			return nil, invalid("a choice is longer than 100 characters")
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out, nil
}

// parseDateTime accepts RFC 3339 with an offset, or a zone-less local form
// ("2026-12-31T14:30") that is read in tz (UTC when tz is empty).
func parseDateTime(s, tz string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	loc := time.UTC
	if tz != "" {
		loc = regional.Location(tz)
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, invalid("datetime must look like 2026-12-31T14:30:00+05:00")
}

var (
	curMu    sync.RWMutex
	curTable map[string]int
)

// UseCurrencies replaces the currency table used to validate amounts. The app
// calls it with the configured list; the embedded defaults apply until then.
func UseCurrencies(list []currencies.Currency) {
	m := make(map[string]int, len(list))
	for _, c := range list {
		m[strings.ToUpper(c.Code)] = c.Decimals
	}
	curMu.Lock()
	curTable = m
	curMu.Unlock()
}

func decimalsFor(code string) (int, bool) {
	curMu.RLock()
	tbl := curTable
	curMu.RUnlock()
	if tbl == nil {
		list, err := currencies.CollectionCurrencies(currencies.CollectDefaults())
		if err != nil {
			return 0, false
		}
		UseCurrencies(list)
		curMu.RLock()
		tbl = curTable
		curMu.RUnlock()
	}
	d, ok := tbl[code]
	return d, ok && codeRe.MatchString(code)
}

// parseMinorUnits turns "12.5" in PKR (2 decimals) into 1250 and "12.50".
func parseMinorUnits(s, code string) (int, string, error) {
	dec, _ := decimalsFor(code)
	if !decimalRe.MatchString(s) {
		return 0, "", invalid("amount must be a plain number such as 12.50")
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > dec {
		// Extra zeros are harmless; real extra precision is not.
		if strings.Trim(frac[dec:], "0") != "" {
			return 0, "", invalid("%s allows at most %d decimal places", code, dec)
		}
		frac = frac[:dec]
	}
	frac += strings.Repeat("0", dec-len(frac))
	n, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil || n > math.MaxInt32*1000 {
		return 0, "", invalid("amount is out of range")
	}
	canonical := whole
	if dec > 0 {
		canonical = whole + "." + frac
	}
	canonical = strings.TrimLeft(canonical, "0")
	if canonical == "" || strings.HasPrefix(canonical, ".") {
		canonical = "0" + canonical
	}
	if neg {
		n = -n
		canonical = "-" + canonical
	}
	return int(n), canonical, nil
}

// FormatMinorUnits renders minor units back to the canonical decimal string.
func FormatMinorUnits(minor int, code string) string {
	dec, _ := decimalsFor(code)
	neg := minor < 0
	if neg {
		minor = -minor
	}
	s := strconv.Itoa(minor)
	if dec > 0 {
		for len(s) <= dec {
			s = "0" + s
		}
		s = s[:len(s)-dec] + "." + s[len(s)-dec:]
	}
	if neg {
		s = "-" + s
	}
	return s
}
