// Package webhooks holds the parts of outbound webhooks that need no I/O: the
// events, the body and signature of a delivery, and the retry schedule.
//
// A receiver can trust a delivery by recomputing its signature, and can tell a
// late delivery from a prompt one without guessing: every body carries when the
// thing happened (or was meant to), when this attempt was made, and how late
// that makes it, all as RFC 3339 timestamps with an explicit offset.
package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/sysadminsmedia/homebox/backend/internal/core/alerts"
)

// Events a webhook can subscribe to.
const (
	ItemCreated         = "item.created"
	ItemUpdated         = "item.updated"
	ItemDeleted         = "item.deleted"
	ItemMoved           = "item.moved"
	ItemQuantityChanged = "item.quantity_changed"
	AttachmentAdded     = "attachment.added"
	MaintenanceDue      = "maintenance.due"
	MaintenanceOverdue  = "maintenance.overdue"
	ExpiryUpcoming      = "expiry.upcoming"
	ExpiryExpired       = "expiry.expired"
	WarrantyUpcoming    = "warranty.upcoming"
	WarrantyExpired     = "warranty.expired"
	BackupCompleted     = "backup.completed"
	BackupFailed        = "backup.failed"

	// Test is sent by the "send test event" button; it is never subscribed to.
	Test = "webhook.test"
)

// Event describes one subscribable event for the UI and docs.
type Event struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Dated events come from alert rules: they fire for the rule's offsets
	// rather than at the moment something is edited.
	Dated bool `json:"dated"`
}

// Catalog lists every subscribable event, in the order the UI shows them.
var Catalog = []Event{
	{ItemCreated, "An item or location was created", false},
	{ItemUpdated, "An item or location was changed; the body lists what changed", false},
	{ItemMoved, "An item was moved to another location or container", false},
	{ItemQuantityChanged, "An item's quantity changed (low-stock automations)", false},
	{ItemDeleted, "An item or location was deleted", false},
	{AttachmentAdded, "A photo, manual, receipt or other file was attached", false},
	{ExpiryUpcoming, "An item is about to expire, per your alert rules", true},
	{ExpiryExpired, "An item has expired, per your alert rules", true},
	{WarrantyUpcoming, "A warranty is about to end, per your alert rules", true},
	{WarrantyExpired, "A warranty has ended, per your alert rules", true},
	{MaintenanceDue, "Maintenance is due, per your alert rules", true},
	{MaintenanceOverdue, "Maintenance is overdue, per your alert rules", true},
	{BackupCompleted, "A scheduled or manual backup finished", false},
	{BackupFailed, "A backup failed", false},
}

// ValidEvent reports whether name can be subscribed to.
func ValidEvent(name string) bool {
	return slices.ContainsFunc(Catalog, func(e Event) bool { return e.Name == name })
}

// EventForAlert names the event an alert becomes: before the date it is the
// "upcoming" (or "due") kind, once the date has passed the "expired" (or
// "overdue") kind. The offset that fired it is carried in the body, so a
// receiver can tell the 30-day warning from the 1-day one.
func EventForAlert(a alerts.Alert) string {
	past := a.DaysUntil < 0
	switch a.Rule.Trigger {
	case alerts.TriggerExpiresAt:
		if past {
			return ExpiryExpired
		}
		return ExpiryUpcoming
	case alerts.TriggerWarrantyExpires:
		if past {
			return WarrantyExpired
		}
		return WarrantyUpcoming
	case alerts.TriggerMaintenanceDue:
		if past {
			return MaintenanceOverdue
		}
		return MaintenanceDue
	}
	return ""
}

// AlertKey is the de-duplication key of an alert for one rule: the same alert
// seen on several ticks produces one delivery per endpoint.
func AlertKey(a alerts.Alert) string {
	return fmt.Sprintf("alert:%s:%s:%d:%s", a.Rule.ID, a.Subject.ID, a.Offset, a.Subject.Anchor.Format(time.DateOnly))
}

// AlertData is the body's data for an alert.
func AlertData(a alerts.Alert, loc *time.Location) map[string]any {
	s := a.Subject
	d := map[string]any{
		"rule":       map[string]any{"id": a.Rule.ID, "name": a.Rule.Name},
		"trigger":    a.Rule.Trigger,
		"item":       map[string]any{"id": s.EntityID, "name": s.Name, "path": "/item/" + s.EntityID.String()},
		"date":       s.Anchor.Format(time.DateOnly),
		"offset":     a.Offset,
		"days_until": a.DaysUntil,
		"due_on":     a.DueOn.Format(time.DateOnly),
	}
	if s.Kind != "" {
		d["expiry_kind"] = s.Kind
	}
	if s.Detail != "" {
		d["maintenance"] = map[string]any{"id": s.ID, "name": s.Detail}
	}
	if s.Location != "" {
		d["location"] = s.Location
	}
	return d
}

// DelayedAfter is how far past the intended time a delivery must be before it
// is flagged as delayed. Below it, ordinary queueing is not worth mentioning.
const DelayedAfter = 5 * time.Minute

// Delivery is what Body needs to know about one attempt.
type Delivery struct {
	ID          uuid.UUID
	EventID     uuid.UUID
	Type        string
	GroupID     uuid.UUID
	OccurredAt  time.Time
	ScheduledAt *time.Time // set for dated events
	CreatedAt   time.Time
	Data        map[string]any
}

// Body builds the JSON sent for one attempt. Timestamps are written in loc (the
// collection's zone) with their offset, so "08:00+05:00" is unambiguous, and a
// receiver in any zone can compare them as instants.
func Body(d Delivery, attempt int, now time.Time, loc *time.Location, zone string) ([]byte, error) {
	if loc == nil {
		loc = time.UTC
	}
	ts := func(t time.Time) string { return t.In(loc).Format(time.RFC3339) }

	// Lateness is measured against when the thing was meant to happen: the
	// scheduled time for dated events, otherwise when it occurred.
	ref := d.OccurredAt
	if d.ScheduledAt != nil {
		ref = *d.ScheduledAt
	}
	delay := max(now.Sub(ref), 0)

	body := map[string]any{
		"id":                  d.ID,
		"event_id":            d.EventID,
		"type":                d.Type,
		"collection_id":       d.GroupID,
		"collection_timezone": zone,
		"created_at":          ts(d.CreatedAt),
		"occurred_at":         ts(d.OccurredAt),
		"scheduled_for":       nil,
		"delivered_at":        ts(now),
		"attempt":             attempt,
		"delayed":             delay >= DelayedAfter,
		"delay_seconds":       int64(delay.Seconds()),
		"data":                d.Data,
	}
	if d.ScheduledAt != nil {
		body["scheduled_for"] = ts(*d.ScheduledAt)
	}
	return json.Marshal(body)
}

// Sign returns the signature header value for a body. The timestamp is part of
// the signed material, so a captured delivery cannot be replayed later: the
// receiver rejects timestamps outside its tolerance. The timestamp is the time
// of this attempt, not the event's, so a legitimately late delivery is not
// mistaken for a replay.
func Sign(secret string, ts time.Time, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts.Unix(), 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify checks a signature the way a receiver should.
func Verify(secret, signature string, ts time.Time, body []byte) bool {
	return hmac.Equal([]byte(Sign(secret, ts, body)), []byte(signature))
}

// backoff is the wait before each retry: attempt n failed, wait backoff[n-1].
var backoff = []time.Duration{
	30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour,
}

// NextAttempt returns when to retry after the given number of failed attempts,
// and false when the delivery should be given up on: it has used up the
// schedule, or is older than maxAge.
func NextAttempt(failed int, created, now time.Time, maxAge time.Duration) (time.Time, bool) {
	if failed < 1 || failed > len(backoff) {
		return time.Time{}, false
	}
	next := now.Add(backoff[failed-1])
	if next.Sub(created) > maxAge {
		return time.Time{}, false
	}
	return next, true
}

// NewSecret returns a fresh signing secret.
func NewSecret() string {
	return "whsec_" + hex.EncodeToString(uuidBytes())
}

func uuidBytes() []byte {
	a, b := uuid.New(), uuid.New()
	return append(a[:], b[:]...)
}
