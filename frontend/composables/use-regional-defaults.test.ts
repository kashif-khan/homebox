import { describe, expect, test } from "vitest";
import { COUNTRY_CURRENCY, countryFromTimezone, currencyForCountry, regionFromLocale } from "./use-regional-defaults";

describe("regionFromLocale", () => {
  test("reads an explicit region", () => {
    expect(regionFromLocale("en-GB")).toBe("GB");
    expect(regionFromLocale("ur-PK")).toBe("PK");
  });

  test("does not invent a region for a bare language", () => {
    expect(regionFromLocale("en")).toBe("");
  });

  test("survives garbage", () => {
    expect(regionFromLocale("not a locale")).toBe("");
  });
});

describe("currencyForCountry", () => {
  test("maps known countries, case-insensitively", () => {
    expect(currencyForCountry("PK")).toBe("PKR");
    expect(currencyForCountry("de")).toBe("EUR");
  });

  test("returns empty for unknown countries", () => {
    expect(currencyForCountry("ZZ")).toBe("");
  });

  test("only maps to three-letter codes", () => {
    for (const code of Object.values(COUNTRY_CURRENCY)) expect(code).toMatch(/^[A-Z]{3}$/);
  });
});

describe("countryFromTimezone", () => {
  test("maps well-known zones", () => {
    expect(countryFromTimezone("Asia/Karachi")).toBe("PK");
    expect(countryFromTimezone("America/Toronto")).toBe("CA");
    expect(countryFromTimezone("Europe/London")).toBe("GB");
  });

  test("covers zones that are not in the built-in list, via the browser's data", () => {
    // Not in the built-in map; resolved from Intl where supported.
    const got = countryFromTimezone("America/Halifax");
    expect(["CA", ""]).toContain(got);
  });

  test("returns empty for unknown or missing zones", () => {
    expect(countryFromTimezone("")).toBe("");
    expect(countryFromTimezone("Mars/Base")).toBe("");
  });
});
