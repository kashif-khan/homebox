/** Helpers for the alert rule editor. */

export const TRIGGERS = ["expires_at", "warranty_expires", "maintenance_due"] as const;
export type Trigger = (typeof TRIGGERS)[number];

export function isTrigger(v: unknown): v is Trigger {
  return typeof v === "string" && (TRIGGERS as readonly string[]).includes(v);
}

/** The offsets offered as one-click choices, furthest before first. */
export const PRESET_OFFSETS = [90, 60, 30, 14, 7, 3, 1, 0, -1, -3, -7, -14, -30] as const;

export const MAX_OFFSETS = 20;
export const MAX_OFFSET_DAYS = 3650;

/** Furthest before the date first, de-duplicated. */
export function normalizeOffsets(offsets: number[]): number[] {
  return [...new Set(offsets)].sort((a, b) => b - a);
}

/** Adds or removes an offset, keeping the list ordered and within the limits. */
export function toggleOffset(offsets: number[], value: number): number[] {
  if (offsets.includes(value)) return offsets.filter(o => o !== value);
  if (offsets.length >= MAX_OFFSETS) return offsets;
  return normalizeOffsets([...offsets, value]);
}

/** Parses a typed offset; null when it is not a whole number within range. */
export function parseOffset(input: string): number | null {
  const s = input.trim();
  if (!/^-?\d+$/.test(s)) return null;
  const n = Number(s);
  return Math.abs(n) <= MAX_OFFSET_DAYS ? n : null;
}

type Translate = (key: string, named?: Record<string, unknown>) => string;

/** "30 days before", "On the day", "7 days after". */
export function offsetLabel(offset: number, t: Translate): string {
  if (offset === 0) return t("alerts.offset.on_the_day");
  if (offset === 1) return t("alerts.offset.one_day_before");
  if (offset === -1) return t("alerts.offset.one_day_after");
  return offset > 0 ? t("alerts.offset.days_before", { n: offset }) : t("alerts.offset.days_after", { n: -offset });
}
