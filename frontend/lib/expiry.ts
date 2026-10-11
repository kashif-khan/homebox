/**
 * Helpers for expiry dates. A consumable's expiry is a calendar date with no
 * time zone, so everything here works on "YYYY-MM-DD" strings and whole days.
 */

export type ExpiryStatus = "expired" | "soon" | "ok" | "none";

/** Today's calendar date in the viewer's own zone, as "YYYY-MM-DD". */
export function todayString(now: Date = new Date()): string {
  const y = now.getFullYear();
  const m = String(now.getMonth() + 1).padStart(2, "0");
  const d = String(now.getDate()).padStart(2, "0");
  return `${y}-${m}-${d}`;
}

const DAY_MS = 24 * 60 * 60 * 1000;

/** Whole days from `from` to `to` (both "YYYY-MM-DD"); negative when `to` is earlier. NaN if either is invalid. */
export function daysBetween(from: string, to: string): number {
  const a = Date.parse(`${from}T00:00:00Z`);
  const b = Date.parse(`${to}T00:00:00Z`);
  return Math.round((b - a) / DAY_MS);
}

/** True for a usable date string; the API sends "" or a 0001 date for "not set". */
export function hasExpiry(expiresAt: string | Date | null | undefined): expiresAt is string {
  if (!expiresAt || typeof expiresAt !== "string") return false;
  return !expiresAt.startsWith("0001") && !isNaN(Date.parse(`${expiresAt.slice(0, 10)}T00:00:00Z`));
}

export function expiryStatus(expiresAt: string | null | undefined, today: string, soonDays = 30): ExpiryStatus {
  if (!hasExpiry(expiresAt)) return "none";
  const left = daysBetween(today, expiresAt.slice(0, 10));
  if (left < 0) return "expired";
  if (left <= soonDays) return "soon";
  return "ok";
}

type Translate = (key: string, named?: Record<string, unknown>) => string;

/** "Expires in 3 days", "Expired yesterday" and so on. */
export function expiryLabel(expiresAt: string | null | undefined, today: string, t: Translate): string {
  if (!hasExpiry(expiresAt)) return "";
  const left = daysBetween(today, expiresAt.slice(0, 10));
  if (left === 0) return t("items.expiry_status.today");
  if (left === 1) return t("items.expiry_status.tomorrow");
  if (left > 1) return t("items.expiry_status.in_days", { n: left });
  if (left === -1) return t("items.expiry_status.ago_one");
  return t("items.expiry_status.ago_days", { n: -left });
}
