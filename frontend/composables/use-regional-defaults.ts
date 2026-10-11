/**
 * Best-effort regional defaults taken from the browser, used to pre-fill the
 * sign-up form. Everything here is a suggestion the user can change: a locale
 * region is only a hint about where someone lives, and the browser has no API
 * that returns a currency. Nothing leaves the device.
 */

export type RegionalDefaults = {
  /** ISO 3166-1 alpha-2, or "" when it could not be worked out. */
  country: string;
  /** BCP 47 tag such as "en-GB". */
  locale: string;
  /** IANA zone name such as "Asia/Karachi". */
  timezone: string;
  /** ISO 4217 code, or "" when the country is unknown. */
  currency: string;
};

/** Country (ISO 3166-1 alpha-2) to its main currency (ISO 4217). */
export const COUNTRY_CURRENCY: Readonly<Record<string, string>> = {
  AE: "AED",
  AR: "ARS",
  AT: "EUR",
  AU: "AUD",
  BD: "BDT",
  BE: "EUR",
  BG: "BGN",
  BR: "BRL",
  CA: "CAD",
  CH: "CHF",
  CL: "CLP",
  CN: "CNY",
  CO: "COP",
  CY: "EUR",
  CZ: "CZK",
  DE: "EUR",
  DK: "DKK",
  EE: "EUR",
  EG: "EGP",
  ES: "EUR",
  FI: "EUR",
  FR: "EUR",
  GB: "GBP",
  GR: "EUR",
  HK: "HKD",
  HR: "EUR",
  HU: "HUF",
  ID: "IDR",
  IE: "EUR",
  IL: "ILS",
  IN: "INR",
  IS: "ISK",
  IT: "EUR",
  JP: "JPY",
  KE: "KES",
  KR: "KRW",
  KW: "KWD",
  LK: "LKR",
  LT: "EUR",
  LU: "EUR",
  LV: "EUR",
  MA: "MAD",
  MT: "EUR",
  MX: "MXN",
  MY: "MYR",
  NG: "NGN",
  NL: "EUR",
  NO: "NOK",
  NP: "NPR",
  NZ: "NZD",
  PE: "PEN",
  PH: "PHP",
  PK: "PKR",
  PL: "PLN",
  PT: "EUR",
  QA: "QAR",
  RO: "RON",
  RS: "RSD",
  RU: "RUB",
  SA: "SAR",
  SE: "SEK",
  SG: "SGD",
  SI: "EUR",
  SK: "EUR",
  TH: "THB",
  TR: "TRY",
  TW: "TWD",
  UA: "UAH",
  US: "USD",
  VN: "VND",
  ZA: "ZAR",
};

/** Fallback when the locale carries no region (for example plain "en"). */
const TIMEZONE_COUNTRY: Readonly<Record<string, string>> = {
  "Asia/Karachi": "PK",
  "Asia/Kolkata": "IN",
  "Asia/Calcutta": "IN",
  "Asia/Dhaka": "BD",
  "Asia/Colombo": "LK",
  "Asia/Kathmandu": "NP",
  "Asia/Dubai": "AE",
  "Asia/Riyadh": "SA",
  "Asia/Singapore": "SG",
  "Asia/Tokyo": "JP",
  "Asia/Seoul": "KR",
  "Asia/Shanghai": "CN",
  "Asia/Hong_Kong": "HK",
  "Asia/Manila": "PH",
  "Asia/Jakarta": "ID",
  "Asia/Bangkok": "TH",
  "Europe/London": "GB",
  "Europe/Dublin": "IE",
  "Europe/Paris": "FR",
  "Europe/Berlin": "DE",
  "Europe/Madrid": "ES",
  "Europe/Rome": "IT",
  "Europe/Amsterdam": "NL",
  "Europe/Stockholm": "SE",
  "Europe/Oslo": "NO",
  "Europe/Copenhagen": "DK",
  "Europe/Warsaw": "PL",
  "Europe/Zurich": "CH",
  "Europe/Istanbul": "TR",
  "America/New_York": "US",
  "America/Chicago": "US",
  "America/Denver": "US",
  "America/Los_Angeles": "US",
  "America/Toronto": "CA",
  "America/Vancouver": "CA",
  "America/Mexico_City": "MX",
  "America/Sao_Paulo": "BR",
  "America/Argentina/Buenos_Aires": "AR",
  "Australia/Sydney": "AU",
  "Australia/Melbourne": "AU",
  "Pacific/Auckland": "NZ",
  "Africa/Johannesburg": "ZA",
  "Africa/Lagos": "NG",
  "Africa/Nairobi": "KE",
  "Africa/Cairo": "EG",
};

/** Pulls the two-letter region out of a locale tag: "en-GB" -> "GB". */
export function regionFromLocale(locale: string): string {
  try {
    const region = new Intl.Locale(locale).maximize().region;
    // maximize() guesses a region for bare languages ("en" -> "US"), which would
    // be a made-up answer. Only trust a region that was actually written.
    const written = new Intl.Locale(locale).region;
    return written && region ? region.toUpperCase() : "";
  } catch {
    return "";
  }
}

type LocaleWithZones = Intl.Locale & { getTimeZones?: () => string[] };

/**
 * The country a time zone belongs to. The zone is the best clue to where
 * someone is, since the browser reports the real one, whereas the language
 * only says what they read (an "en-US" browser can be anywhere). The built-in
 * list is tried first, then the browser's own zone-by-country data, which most
 * browsers have, searched over the countries we can price.
 */
export function countryFromTimezone(timezone: string): string {
  if (!timezone) return "";
  if (TIMEZONE_COUNTRY[timezone]) return TIMEZONE_COUNTRY[timezone];
  for (const code of Object.keys(COUNTRY_CURRENCY)) {
    try {
      const zones = (new Intl.Locale(`und-${code}`) as LocaleWithZones).getTimeZones?.() ?? [];
      if (zones.includes(timezone)) return code;
    } catch {
      return "";
    }
  }
  return "";
}

export function currencyForCountry(country: string): string {
  return COUNTRY_CURRENCY[country.toUpperCase()] ?? "";
}

/** Reads the browser. Safe to call during SSR: it returns empty values there. */
export function detectRegionalDefaults(): RegionalDefaults {
  if (typeof navigator === "undefined") {
    return { country: "", locale: "", timezone: "", currency: "" };
  }

  const locale = navigator.languages?.find(l => regionFromLocale(l)) ?? navigator.language ?? "";

  let timezone = "";
  try {
    timezone = Intl.DateTimeFormat().resolvedOptions().timeZone ?? "";
  } catch {
    // some privacy modes hide the zone
  }

  // Where you are (time zone) beats what you read (language region).
  const country = countryFromTimezone(timezone) || regionFromLocale(locale);
  return { country, locale, timezone, currency: currencyForCountry(country) };
}

/** All IANA zones the browser knows, for a picker. Empty when unsupported. */
export function listTimezones(): string[] {
  try {
    return (Intl as unknown as { supportedValuesOf(k: string): string[] }).supportedValuesOf("timeZone");
  } catch {
    return [];
  }
}

/** Display names for every country code we know a currency for. */
export function listCountries(locale: string): { code: string; name: string }[] {
  let names: Intl.DisplayNames | null = null;
  try {
    names = new Intl.DisplayNames([locale || "en"], { type: "region" });
  } catch {
    // fall back to bare codes
  }
  return Object.keys(COUNTRY_CURRENCY)
    .map(code => ({ code, name: names?.of(code) ?? code }))
    .sort((a, b) => a.name.localeCompare(b.name));
}
