package regional

import "testing"

func TestCountry(t *testing.T) {
	for in, want := range map[string]string{"pk": "PK", " GB ": "GB", "USA": "", "1a": "", "": ""} {
		if got := Country(in); got != want {
			t.Errorf("Country(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLocale(t *testing.T) {
	for in, want := range map[string]string{"en-GB": "en-GB", "en": "en", "zh-Hant-TW": "zh-Hant-TW", "e": "", "en_GB": "", "<script>": ""} {
		if got := Locale(in); got != want {
			t.Errorf("Locale(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTimezone(t *testing.T) {
	for in, want := range map[string]string{"Asia/Karachi": "Asia/Karachi", "UTC": "UTC", "Local": "", "Mars/Base": "", "": ""} {
		if got := Timezone(in); got != want {
			t.Errorf("Timezone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLocationFallsBackToUTC(t *testing.T) {
	if Location("nope").String() != "UTC" || Location("").String() != "UTC" {
		t.Fatal("expected UTC fallback")
	}
	if Location("Asia/Karachi").String() != "Asia/Karachi" {
		t.Fatal("expected named zone")
	}
}
