import { describe, expect, test } from "vitest";
import { deliverySummary, groupEvents, humanDuration, lateBySeconds, toggleIn } from "./webhooks";

describe("lateBySeconds", () => {
  test("is zero for a prompt delivery", () => {
    expect(lateBySeconds({ occurredAt: "2030-06-10T08:00:00+05:00", deliveredAt: "2030-06-10T08:01:00+05:00" })).toBe(
      0
    );
  });

  test("measures from the scheduled time for date alerts, across zones", () => {
    // Meant for 08:00 Karachi (03:00 UTC), delivered 13:30 Karachi (08:30 UTC).
    expect(
      lateBySeconds({
        occurredAt: "2030-06-10T08:00:00+05:00",
        scheduledFor: "2030-06-10T08:00:00+05:00",
        deliveredAt: "2030-06-10T08:30:00Z",
      })
    ).toBe(5.5 * 3600);
  });

  test("is zero when not yet delivered or unparseable", () => {
    expect(lateBySeconds({ occurredAt: "2030-06-10T08:00:00Z", deliveredAt: null })).toBe(0);
    expect(lateBySeconds({ occurredAt: "nope", deliveredAt: "also nope" })).toBe(0);
  });
});

describe("humanDuration", () => {
  test("picks the two largest units", () => {
    expect(humanDuration(5 * 60)).toBe("5 min");
    expect(humanDuration(5 * 3600 + 12 * 60)).toBe("5 h 12 min");
    expect(humanDuration(3600)).toBe("1 h");
    expect(humanDuration(2 * 86400 + 4 * 3600)).toBe("2 d 4 h");
    expect(humanDuration(86400)).toBe("1 d");
  });
});

describe("deliverySummary", () => {
  test("describes each state", () => {
    expect(deliverySummary({ status: "delivered", attempts: 1, lastStatusCode: 200, lastError: "" })).toBe("HTTP 200");
    expect(deliverySummary({ status: "failed", attempts: 3, lastStatusCode: 0, lastError: "timeout" })).toBe("timeout");
    expect(deliverySummary({ status: "pending", attempts: 0, lastStatusCode: 0, lastError: "" })).toBe("");
    expect(deliverySummary({ status: "pending", attempts: 1, lastStatusCode: 503, lastError: "" })).toBe("HTTP 503");
  });
});

describe("groupEvents / toggleIn", () => {
  test("sections are item, dated, then backup, and empty ones are dropped", () => {
    const groups = groupEvents([
      { name: "backup.failed", dated: false },
      { name: "expiry.upcoming", dated: true },
      { name: "item.created", dated: false },
    ]);
    expect(groups.map(g => g.key)).toEqual(["item", "dated", "backup"]);
    expect(groupEvents([{ name: "item.created", dated: false }]).map(g => g.key)).toEqual(["item"]);
  });

  test("toggleIn adds and removes", () => {
    expect(toggleIn(["a"], "b")).toEqual(["a", "b"]);
    expect(toggleIn(["a", "b"], "a")).toEqual(["b"]);
  });
});
