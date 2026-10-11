import { describe, expect, test } from "vitest";
import {
  blankField,
  displayFieldValue,
  isFieldType,
  parseChoices,
  retypeField,
  wallClock,
  type EditableField,
} from "../lib/custom-fields";

const labels = { yes: "Yes", no: "No" };

describe("isFieldType", () => {
  test("accepts every server type and rejects others", () => {
    for (const t of ["text", "number", "decimal", "currency", "boolean", "date", "time", "datetime", "url", "select"])
      expect(isFieldType(t)).toBe(true);
    expect(isFieldType("geo")).toBe(false);
    expect(isFieldType(undefined)).toBe(false);
  });
});

describe("retypeField", () => {
  test("clears the old value and applies defaults for the new type", () => {
    const f: EditableField = { ...blankField(), name: "x", type: "decimal", textValue: "3.5" };
    retypeField(f, "currency", { currency: "GBP", timezone: "Europe/London" });
    expect(f.type).toBe("currency");
    expect(f.textValue).toBe("");
    expect(f.currencyCode).toBe("GBP");
    expect(f.timezone).toBe("");

    retypeField(f, "datetime", { currency: "GBP", timezone: "Europe/London" });
    expect(f.timezone).toBe("Europe/London");
    expect(f.currencyCode).toBe("");
  });
});

describe("parseChoices", () => {
  test("trims and drops empties", () => {
    expect(parseChoices(" a, b ,, c ")).toEqual(["a", "b", "c"]);
    expect(parseChoices("")).toEqual([]);
  });
});

describe("wallClock", () => {
  test("keeps the local part of an RFC 3339 value", () => {
    expect(wallClock("2026-10-10T08:00:00+05:00")).toBe("2026-10-10T08:00");
    expect(wallClock("")).toBe("");
  });
});

describe("displayFieldValue", () => {
  const base = { numberValue: 0, booleanValue: false };

  test("legacy number and boolean read their own columns", () => {
    expect(displayFieldValue({ ...base, type: "number", textValue: "", numberValue: 12 }, "en", labels)).toBe("12");
    expect(displayFieldValue({ ...base, type: "boolean", textValue: "", booleanValue: true }, "en", labels)).toBe(
      "Yes"
    );
  });

  test("a date is not moved by the viewer's time zone", () => {
    const out = displayFieldValue({ ...base, type: "date", textValue: "2027-03-01" }, "en-GB", labels);
    expect(out).toContain("1");
    expect(out).toContain("2027");
    expect(out).toMatch(/Mar/);
  });

  test("a datetime is shown in its own zone", () => {
    const out = displayFieldValue(
      { ...base, type: "datetime", textValue: "2026-10-10T08:00:00+05:00", timezone: "Asia/Karachi" },
      "en-GB",
      labels
    );
    expect(out).toMatch(/08:00/);
  });

  test("empty values render as empty", () => {
    expect(displayFieldValue({ ...base, type: "date", textValue: "" }, "en", labels)).toBe("");
    expect(displayFieldValue({ ...base, type: "currency", textValue: "", currencyCode: "USD" }, "en", labels)).toBe("");
  });

  test("currency formats with its code", () => {
    const out = displayFieldValue(
      { ...base, type: "currency", textValue: "19.90", currencyCode: "GBP" },
      "en-GB",
      labels
    );
    expect(out).toContain("19.90");
  });
});
