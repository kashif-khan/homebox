import { fmtCurrency } from "../composables/utils";

/** The custom field types the server accepts, in the order the picker shows them. */
export const FIELD_TYPES = [
  "text",
  "number",
  "decimal",
  "currency",
  "boolean",
  "date",
  "time",
  "datetime",
  "url",
  "select",
] as const;

export type FieldType = (typeof FIELD_TYPES)[number];

/**
 * The shape the editors work with. Entity fields and template fields share it;
 * the server derives every other typed value from textValue (or numberValue /
 * booleanValue for the two original types), so only these are ever sent.
 */
export type EditableField = {
  id: string;
  name: string;
  type: FieldType;
  textValue: string;
  numberValue: number;
  booleanValue: boolean;
  currencyCode: string;
  timezone: string;
  choices: string[];
};

export function isFieldType(v: unknown): v is FieldType {
  return typeof v === "string" && (FIELD_TYPES as readonly string[]).includes(v);
}

/** Resets the value-bearing parts of a field when its type changes. */
export function retypeField(field: EditableField, type: FieldType, defaults: { currency?: string; timezone?: string }) {
  field.type = type;
  field.textValue = "";
  field.numberValue = 0;
  field.booleanValue = false;
  field.currencyCode = type === "currency" ? (defaults.currency ?? "") : "";
  field.timezone = type === "datetime" ? (defaults.timezone ?? "") : "";
  field.choices = type === "select" ? field.choices : [];
}

/** The id the API expects for a field that has not been saved yet. */
export const NEW_FIELD_ID = "00000000-0000-0000-0000-000000000000";

export function blankField(defaults: { currency?: string; timezone?: string } = {}): EditableField {
  const f: EditableField = {
    id: NEW_FIELD_ID,
    name: "",
    type: "text",
    textValue: "",
    numberValue: 0,
    booleanValue: false,
    currencyCode: "",
    timezone: "",
    choices: [],
  };
  retypeField(f, "text", defaults);
  return f;
}

/** Splits "a, b ,c" into trimmed, non-empty choices. */
export function parseChoices(s: string): string[] {
  return s
    .split(",")
    .map(c => c.trim())
    .filter(Boolean);
}

type Displayable = Pick<EditableField, "type" | "textValue" | "numberValue" | "booleanValue"> &
  Partial<Pick<EditableField, "currencyCode" | "timezone">>;

/**
 * A human-readable rendering of a field's value, or "" when it has none. The
 * canonical text the server stores is parsed here, never guessed from other
 * columns, so what is shown is what was saved.
 */
export function displayFieldValue(f: Displayable, locale: string, labels: { yes: string; no: string }): string {
  switch (f.type) {
    case "number":
      return String(f.numberValue);
    case "boolean":
      return f.booleanValue ? labels.yes : labels.no;
    case "currency": {
      if (!f.textValue || !f.currencyCode) return "";
      return fmtCurrency(Number(f.textValue), f.currencyCode, locale);
    }
    case "date": {
      if (!f.textValue) return "";
      // A date has no zone. Build it at noon UTC and format in UTC so no
      // viewer's zone can move it to the neighbouring day.
      const d = new Date(`${f.textValue}T12:00:00Z`);
      return isNaN(d.getTime())
        ? f.textValue
        : new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeZone: "UTC" }).format(d);
    }
    case "datetime": {
      if (!f.textValue) return "";
      const d = new Date(f.textValue);
      if (isNaN(d.getTime())) return f.textValue;
      try {
        return new Intl.DateTimeFormat(locale, {
          dateStyle: "medium",
          timeStyle: "short",
          timeZone: f.timezone || undefined,
          timeZoneName: "short",
        }).format(d);
      } catch {
        return f.textValue;
      }
    }
    default:
      return f.textValue;
  }
}

/** For a datetime field, the "YYYY-MM-DDTHH:mm" wall-clock part of its canonical text. */
export function wallClock(textValue: string): string {
  return textValue ? textValue.slice(0, 16) : "";
}
