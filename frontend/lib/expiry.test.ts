import { describe, expect, test } from "vitest";
import { daysBetween, expiryLabel, expiryStatus, hasExpiry, todayString } from "./expiry";

const t = (key: string, named?: Record<string, unknown>) => (named ? `${key}:${named.n}` : key);

describe("hasExpiry", () => {
  test("treats empty and zero dates as not set", () => {
    expect(hasExpiry("")).toBe(false);
    expect(hasExpiry(undefined)).toBe(false);
    expect(hasExpiry("0001-01-01T00:00:00Z")).toBe(false);
    expect(hasExpiry("2027-03-01")).toBe(true);
  });
});

describe("daysBetween", () => {
  test("counts whole days across month and DST boundaries", () => {
    expect(daysBetween("2026-10-10", "2026-10-11")).toBe(1);
    expect(daysBetween("2026-10-31", "2026-11-02")).toBe(2);
    expect(daysBetween("2026-03-28", "2026-03-30")).toBe(2);
    expect(daysBetween("2026-10-10", "2026-10-09")).toBe(-1);
    expect(daysBetween("2028-02-28", "2028-03-01")).toBe(2); // leap year
  });
});

describe("expiryStatus", () => {
  const today = "2026-10-10";
  test("classifies by distance from today", () => {
    expect(expiryStatus("2026-10-09", today)).toBe("expired");
    expect(expiryStatus("2026-10-10", today)).toBe("soon");
    expect(expiryStatus("2026-11-09", today)).toBe("soon");
    expect(expiryStatus("2026-11-10", today)).toBe("ok");
    expect(expiryStatus("", today)).toBe("none");
  });
  test("the soon window is adjustable", () => {
    expect(expiryStatus("2026-10-20", today, 7)).toBe("ok");
  });
});

describe("expiryLabel", () => {
  const today = "2026-10-10";
  test("uses singular and plural forms", () => {
    expect(expiryLabel("2026-10-10", today, t)).toBe("items.expiry_status.today");
    expect(expiryLabel("2026-10-11", today, t)).toBe("items.expiry_status.tomorrow");
    expect(expiryLabel("2026-10-15", today, t)).toBe("items.expiry_status.in_days:5");
    expect(expiryLabel("2026-10-09", today, t)).toBe("items.expiry_status.ago_one");
    expect(expiryLabel("2026-10-05", today, t)).toBe("items.expiry_status.ago_days:5");
    expect(expiryLabel("", today, t)).toBe("");
  });
});

describe("todayString", () => {
  test("uses the local calendar date", () => {
    expect(todayString(new Date(2026, 9, 10, 23, 59))).toBe("2026-10-10");
    expect(todayString(new Date(2026, 0, 5, 0, 1))).toBe("2026-01-05");
  });
});
