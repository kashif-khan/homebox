import { describe, expect, test } from "vitest";
import { COUNTRY_CURRENCY, currencyForCountry, regionFromLocale } from "./use-regional-defaults";

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
