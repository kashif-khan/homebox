/** Helpers for the webhooks page. */

export type DeliveryStatus = "pending" | "delivered" | "failed";

/** A short plain-English line for a delivery's outcome. */
export function deliverySummary(d: {
  status: string;
  attempts: number;
  lastStatusCode: number;
  lastError: string;
}): string {
  if (d.status === "delivered") return `HTTP ${d.lastStatusCode}`;
  const why = d.lastError || (d.lastStatusCode ? `HTTP ${d.lastStatusCode}` : "");
  return d.status === "pending" && d.attempts === 0 ? "" : why;
}

/**
 * How late a delivery was, as a number of seconds, or 0 when it was prompt.
 * Mirrors the server: lateness is measured from when the thing was meant to
 * happen (scheduledFor for date alerts, otherwise occurredAt), and under five
 * minutes is not worth reporting.
 */
export function lateBySeconds(d: {
  occurredAt: string;
  scheduledFor?: string | null;
  deliveredAt?: string | null;
}): number {
  if (!d.deliveredAt) return 0;
  const ref = Date.parse(d.scheduledFor ?? d.occurredAt);
  const at = Date.parse(d.deliveredAt);
  if (isNaN(ref) || isNaN(at)) return 0;
  const secs = Math.round((at - ref) / 1000);
  return secs >= 300 ? secs : 0;
}

/** "5 h 12 min", "3 min", "2 d 4 h". */
export function humanDuration(totalSeconds: number): string {
  const s = Math.max(0, Math.round(totalSeconds));
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (d > 0) return h > 0 ? `${d} d ${h} h` : `${d} d`;
  if (h > 0) return m > 0 ? `${h} h ${m} min` : `${h} h`;
  return `${m} min`;
}

/** Groups the event catalogue into the sections the picker shows. */
export function groupEvents<T extends { name: string; dated: boolean }>(events: T[]): { key: string; events: T[] }[] {
  const section = (e: T) => (e.dated ? "dated" : e.name.split(".")[0] === "backup" ? "backup" : "item");
  const order = ["item", "dated", "backup"];
  return order.map(key => ({ key, events: events.filter(e => section(e) === key) })).filter(g => g.events.length > 0);
}

export function toggleIn(list: string[], value: string): string[] {
  return list.includes(value) ? list.filter(v => v !== value) : [...list, value];
}
