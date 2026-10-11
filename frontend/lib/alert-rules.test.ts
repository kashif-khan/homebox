import { describe, expect, test } from "vitest";
import { isTrigger, normalizeOffsets, offsetLabel, parseOffset, toggleOffset, MAX_OFFSETS } from "./alert-rules";

const t = (key: string, named?: Record<string, unknown>) => (named ? `${key}:${named.n}` : key);

describe("offsets", () => {
  test("normalize orders furthest-before first and drops duplicates", () => {
    expect(normalizeOffsets([0, 7, -1, 30, 7])).toEqual([30, 7, 0, -1]);
  });

  test("toggle adds and removes, staying ordered", () => {
    expect(toggleOffset([30, 1], 7)).toEqual([30, 7, 1]);
    expect(toggleOffset([30, 7, 1], 7)).toEqual([30, 1]);
  });

  test("toggle refuses to grow past the limit but can still remove", () => {
    const full = Array.from({ length: MAX_OFFSETS }, (_, i) => i);
    expect(toggleOffset(full, 500)).toEqual(full);
    expect(toggleOffset(full, 3)).toHaveLength(MAX_OFFSETS - 1);
  });

  test("parseOffset accepts whole numbers in range only", () => {
    expect(parseOffset(" 14 ")).toBe(14);
    expect(parseOffset("-7")).toBe(-7);
    expect(parseOffset("0")).toBe(0);
    for (const bad of ["", "1.5", "abc", "99999", "-99999", "1e3", "+"]) expect(parseOffset(bad)).toBeNull();
  });

  test("labels read naturally", () => {
    expect(offsetLabel(0, t)).toBe("alerts.offset.on_the_day");
    expect(offsetLabel(1, t)).toBe("alerts.offset.one_day_before");
    expect(offsetLabel(-1, t)).toBe("alerts.offset.one_day_after");
    expect(offsetLabel(30, t)).toBe("alerts.offset.days_before:30");
    expect(offsetLabel(-7, t)).toBe("alerts.offset.days_after:7");
  });
});

describe("isTrigger", () => {
  test("knows the three triggers", () => {
    expect(isTrigger("expires_at")).toBe(true);
    expect(isTrigger("warranty_expires")).toBe(true);
    expect(isTrigger("maintenance_due")).toBe(true);
    expect(isTrigger("birthday")).toBe(false);
  });
});
