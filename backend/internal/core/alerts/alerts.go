// Package alerts decides which date-based alerts are due and words the digest
// that is sent for them. It does no I/O: callers load the dates, ask what is
// due, send the result and remember it.
//
// All dates are calendar days held as midnight UTC, whatever zone the
// collection lives in. "Today" is the collection's own today, which the caller
// works out from the collection's time zone.
package alerts

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Triggers a rule can watch.
const (
	TriggerExpiresAt       = "expires_at"
	TriggerWarrantyExpires = "warranty_expires"
	TriggerMaintenanceDue  = "maintenance_due"
)

// ValidTrigger reports whether t is a known trigger.
func ValidTrigger(t string) bool {
	switch t {
	case TriggerExpiresAt, TriggerWarrantyExpires, TriggerMaintenanceDue:
		return true
	}
	return false
}

// CatchUpDays is how late an alert may still be sent. The server being down
// over the hour an alert was due delays it rather than dropping it, but an
// alert more than this many days overdue is considered stale and skipped, so a
// long outage or a brand-new rule cannot flood people with old news.
const CatchUpDays = 7

// MaxOffsets bounds how many offsets one rule may have, and MaxOffsetDays how
// far from the date one may be.
const (
	MaxOffsets    = 20
	MaxOffsetDays = 3650
)

// Rule is the part of a notification rule the engine needs.
type Rule struct {
	ID       uuid.UUID
	Name     string
	Trigger  string
	Offsets  []int
	Notifier []uuid.UUID // empty: every active notifier
}

// Subject is one dated thing: an item's expiry, its warranty end, or a
// maintenance entry's due date.
type Subject struct {
	// ID identifies the thing for de-duplication: the item for expiry and
	// warranty, the entry for maintenance.
	ID uuid.UUID
	// EntityID is the item the date belongs to.
	EntityID uuid.UUID
	Name     string
	// Detail is extra text: the maintenance entry's name, for example.
	Detail string
	// Anchor is the calendar date, midnight UTC.
	Anchor   time.Time
	Kind     string // expiry kind, for expires_at
	Location string
}

// Alert is one subject at one offset that is due now.
type Alert struct {
	Rule    Rule
	Subject Subject
	Offset  int
	// DueOn is the calendar day the alert was meant to go out: Anchor - Offset.
	DueOn time.Time
	// ScheduledFor is the instant it was meant to go out: DueOn at the
	// collection's send hour, in the collection's zone.
	ScheduledFor time.Time
	// DaysUntil counts from today to the date; negative once it has passed.
	DaysUntil int
}

// Day truncates t to a calendar day, midnight UTC, keeping the date as it reads
// on the clock in t's own zone.
func Day(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// DueOn is the day an alert at offset is meant to go out for a date: offset
// days before it, or after it when offset is negative.
func DueOn(anchor time.Time, offset int) time.Time {
	return Day(anchor).AddDate(0, 0, -offset)
}

// IsDue reports whether the alert should be sent today: its day has come, and it
// is not so far past that it counts as stale.
func IsDue(today, anchor time.Time, offset int) bool {
	due := DueOn(anchor, offset)
	today = Day(today)
	return !due.After(today) && !due.Before(today.AddDate(0, 0, -CatchUpDays))
}

// ScheduledFor is the instant a day's alerts are meant to go out.
func ScheduledFor(dueOn time.Time, hour int, loc *time.Location) time.Time {
	return time.Date(dueOn.Year(), dueOn.Month(), dueOn.Day(), hour, 0, 0, 0, loc)
}

// AnchorRange is the span of dates that can have an alert due on today for any
// of offsets: the dates a loader needs to fetch.
func AnchorRange(today time.Time, offsets []int) (from, to time.Time) {
	today = Day(today)
	if len(offsets) == 0 {
		return today, today
	}
	lo, hi := offsets[0], offsets[0]
	for _, o := range offsets {
		lo, hi = min(lo, o), max(hi, o)
	}
	// anchor - offset in [today-CatchUp, today]  =>  anchor in [today-CatchUp+offset, today+offset]
	return today.AddDate(0, 0, -CatchUpDays+lo), today.AddDate(0, 0, hi)
}

// Evaluate returns the alerts of rule that are due today, skipping any that
// sent reports as already delivered. sent receives the subject, offset and
// anchor date.
func Evaluate(rule Rule, subjects []Subject, today time.Time, hour int, loc *time.Location, sent func(Subject, int) bool) []Alert {
	today = Day(today)
	var out []Alert
	for _, s := range subjects {
		for _, off := range rule.Offsets {
			if !IsDue(today, s.Anchor, off) {
				continue
			}
			if sent != nil && sent(s, off) {
				continue
			}
			due := DueOn(s.Anchor, off)
			out = append(out, Alert{
				Rule:         rule,
				Subject:      s,
				Offset:       off,
				DueOn:        due,
				ScheduledFor: ScheduledFor(due, hour, loc),
				DaysUntil:    int(Day(s.Anchor).Sub(today).Hours() / 24),
			})
		}
	}
	return out
}

// Delay is how long after it was meant to go out an alert is going out.
func (a Alert) Delay(now time.Time) time.Duration {
	if d := now.Sub(a.ScheduledFor); d > 0 {
		return d
	}
	return 0
}

// MaxLinesPerSection caps a digest section; the rest are counted.
const MaxLinesPerSection = 40

type section struct {
	title string
	lines []string
	extra int
}

// Digest words the alerts for one notifier. today is the collection's date.
func Digest(collection string, today time.Time, alerts []Alert) string {
	sort.SliceStable(alerts, func(i, j int) bool {
		if alerts[i].DaysUntil != alerts[j].DaysUntil {
			return alerts[i].DaysUntil < alerts[j].DaysUntil
		}
		return alerts[i].Subject.Name < alerts[j].Subject.Name
	})

	titles := map[string]string{
		TriggerExpiresAt:       "Expiry",
		TriggerWarrantyExpires: "Warranty",
		TriggerMaintenanceDue:  "Maintenance",
	}
	order := []string{TriggerExpiresAt, TriggerWarrantyExpires, TriggerMaintenanceDue}
	sections := map[string]*section{}
	for _, a := range alerts {
		sec := sections[a.Rule.Trigger]
		if sec == nil {
			sec = &section{title: titles[a.Rule.Trigger]}
			sections[a.Rule.Trigger] = sec
		}
		if len(sec.lines) >= MaxLinesPerSection {
			sec.extra++
			continue
		}
		sec.lines = append(sec.lines, "- "+Line(a))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Homebox alerts for %s (%s)\n", collection, Day(today).Format(time.DateOnly))
	for _, trig := range order {
		sec := sections[trig]
		if sec == nil {
			continue
		}
		fmt.Fprintf(&b, "\n%s\n%s\n", sec.title, strings.Join(sec.lines, "\n"))
		if sec.extra > 0 {
			fmt.Fprintf(&b, "...and %d more\n", sec.extra)
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// Line words one alert, for example "Aspirin: best before in 3 days (2026-10-13), Bathroom".
func Line(a Alert) string {
	s := a.Subject
	what := phrase(a.Rule.Trigger, s.Kind, a.DaysUntil)
	out := fmt.Sprintf("%s: %s (%s)", s.Name, what, Day(s.Anchor).Format(time.DateOnly))
	if s.Detail != "" {
		out = fmt.Sprintf("%s - %s: %s (%s)", s.Name, s.Detail, what, Day(s.Anchor).Format(time.DateOnly))
	}
	if s.Location != "" {
		out += ", " + s.Location
	}
	return out
}

func phrase(trigger, kind string, days int) string {
	var noun, past string
	switch trigger {
	case TriggerWarrantyExpires:
		noun, past = "warranty ends", "warranty ended"
	case TriggerMaintenanceDue:
		noun, past = "due", "overdue by"
	default:
		switch kind {
		case "use_by":
			noun, past = "use by", "was use by"
		case "best_before":
			noun, past = "best before", "was best before"
		default:
			noun, past = "expires", "expired"
		}
	}
	switch {
	case days == 0:
		return noun + " today"
	case days == 1:
		return noun + " tomorrow"
	case days > 1:
		return fmt.Sprintf("%s in %d days", noun, days)
	case trigger == TriggerMaintenanceDue && days == -1:
		return "overdue by 1 day"
	case trigger == TriggerMaintenanceDue:
		return fmt.Sprintf("overdue by %d days", -days)
	case days == -1:
		return past + " yesterday"
	default:
		return fmt.Sprintf("%s %d days ago", past, -days)
	}
}
