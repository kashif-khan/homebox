/**
 * Mirrors the permission presets defined by the backend (internal/core/scopes).
 * Only used to label things for display; the server is the source of truth and
 * validates everything it is sent.
 */
export type ScopePreset = "read-only" | "read-write" | "full";

const READ_WRITE = [
  "attachments:read",
  "collection:read",
  "items:read",
  "maintenance:read",
  "attachments:write",
  "items:write",
  "maintenance:write",
];

function sameSet(a: string[], b: string[]) {
  return a.length === b.length && a.every(s => b.includes(s));
}

/** Classifies a scope list as one of the presets, or "custom". */
export function scopePreset(scopes: string[] | null | undefined): ScopePreset | "custom" {
  const list = scopes ?? [];
  if (list.includes("*")) return "full";
  if (sameSet(list, READ_WRITE)) return "read-write";
  // Anything that can only look, whatever subset of the read scopes it holds.
  if (list.length > 0 && !scopesCanWrite(list)) return "read-only";
  return "custom";
}

/** True when the scopes allow changing data. */
export function scopesCanWrite(scopes: string[] | null | undefined): boolean {
  return (scopes ?? []).some(s => s === "*" || /:(write|delete)$/.test(s));
}

/** Collection AI-access levels, weakest first. */
export const AI_ACCESS_LEVELS = ["off", "read", "write", "full"] as const;
export type AIAccessLevel = (typeof AI_ACCESS_LEVELS)[number];
