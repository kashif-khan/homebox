// Package regional validates and normalises the regional preferences a user or
// collection carries: country, locale, timezone and currency. The values are
// hints taken from the browser at sign-up, so invalid ones are dropped rather
// than rejected.
package regional

import (
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // IANA zone names must resolve on minimal images
)

// DefaultTimezone is used for a collection that has not chosen one.
const DefaultTimezone = "UTC"

var (
	countryRe = regexp.MustCompile(`^[A-Za-z]{2}$`)
	localeRe  = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8}){0,3}$`)
)

// Country returns the upper-case ISO 3166-1 alpha-2 code, or "" when the input
// is not shaped like one.
func Country(s string) string {
	s = strings.TrimSpace(s)
	if !countryRe.MatchString(s) {
		return ""
	}
	return strings.ToUpper(s)
}

// Locale returns a BCP 47 style tag such as "en-GB", or "" when the input is
// not shaped like one.
func Locale(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 35 || !localeRe.MatchString(s) {
		return ""
	}
	return s
}

// Timezone returns the input when it names a known IANA zone, or "" otherwise.
// "Local" is refused because it would mean the server's zone.
func Timezone(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 64 || strings.EqualFold(s, "local") {
		return ""
	}
	if _, err := time.LoadLocation(s); err != nil {
		return ""
	}
	return s
}

// Location resolves a stored zone name, falling back to UTC when it is empty or
// no longer valid.
func Location(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}
