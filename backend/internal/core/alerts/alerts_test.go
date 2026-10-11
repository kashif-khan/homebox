package alerts

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func d(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 0, 0, 0, 0, time.UTC) }

func TestDueOnAndIsDue(t *testing.T) {
	anchor := d(2026, 10, 20)
	assert.Equal(t, d(2026, 10, 13), DueOn(anchor, 7), "7 days before")
	assert.Equal(t, d(2026, 10, 20), DueOn(anchor, 0))
	assert.Equal(t, d(2026, 10, 27), DueOn(anchor, -7), "7 days after")

	cases := []struct {
		name   string
		today  time.Time
		offset int
		due    bool
	}{
		{"not yet", d(2026, 10, 12), 7, false},
		{"exactly on the day", d(2026, 10, 13), 7, true},
		{"a day late (server was down)", d(2026, 10, 14), 7, true},
		{"seven days late is the last day it is still sent", d(2026, 10, 20), 7, true},
		{"eight days late is stale", d(2026, 10, 21), 7, false},
		{"after-expiry offset on the day", d(2026, 10, 27), -7, true},
		{"after-expiry offset early", d(2026, 10, 26), -7, false},
	}
	for _, c := range cases {
		assert.Equal(t, c.due, IsDue(c.today, anchor, c.offset), c.name)
	}
}

func TestIsDueIgnoresTimeOfDay(t *testing.T) {
	// A "today" taken from a wall clock late at night must not shift the day.
	late := time.Date(2026, 10, 13, 23, 59, 0, 0, time.FixedZone("x", 5*3600))
	assert.True(t, IsDue(late, d(2026, 10, 20), 7))
	early := time.Date(2026, 10, 13, 0, 1, 0, 0, time.FixedZone("x", -8*3600))
	assert.True(t, IsDue(early, d(2026, 10, 20), 7))
}

func TestAnchorRangeCoversEveryDueAnchor(t *testing.T) {
	today := d(2026, 10, 10)
	offsets := []int{30, 7, 1, 0, -1, -7}
	from, to := AnchorRange(today, offsets)

	// Brute force: every anchor for which some offset is due must be inside.
	for delta := -60; delta <= 60; delta++ {
		anchor := today.AddDate(0, 0, delta)
		due := false
		for _, o := range offsets {
			due = due || IsDue(today, anchor, o)
		}
		inside := !anchor.Before(from) && !anchor.After(to)
		if due {
			assert.True(t, inside, "anchor %v is due but outside [%v, %v]", anchor, from, to)
		}
	}
}

func TestEvaluateSkipsAlreadySent(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Karachi")
	require.NoError(t, err)
	rule := Rule{ID: uuid.New(), Trigger: TriggerExpiresAt, Offsets: []int{7, 1, 0}}
	soon := Subject{ID: uuid.New(), Name: "Aspirin", Anchor: d(2026, 10, 11)} // 1 day away
	later := Subject{ID: uuid.New(), Name: "Rice", Anchor: d(2027, 1, 1)}
	today := d(2026, 10, 10)

	got := Evaluate(rule, []Subject{soon, later}, today, 8, loc, nil)
	require.Len(t, got, 2, "offset 7 (3 days late) and offset 1 are both due for the soon item")
	offsets := []int{got[0].Offset, got[1].Offset}
	assert.ElementsMatch(t, []int{7, 1}, offsets)

	got = Evaluate(rule, []Subject{soon, later}, today, 8, loc, func(s Subject, off int) bool {
		return s.ID == soon.ID && off == 7
	})
	require.Len(t, got, 1)
	assert.Equal(t, 1, got[0].Offset)
	assert.Equal(t, 1, got[0].DaysUntil)
	assert.Equal(t, d(2026, 10, 10), got[0].DueOn)
	// Meant to go out at 08:00 Karachi on the 10th = 03:00 UTC.
	assert.Equal(t, time.Date(2026, 10, 10, 3, 0, 0, 0, time.UTC), got[0].ScheduledFor.UTC())
}

func TestAlertDelay(t *testing.T) {
	loc := time.UTC
	a := Alert{ScheduledFor: ScheduledFor(d(2026, 10, 10), 8, loc)}
	assert.Equal(t, time.Duration(0), a.Delay(time.Date(2026, 10, 10, 8, 0, 0, 0, loc)))
	assert.Equal(t, time.Duration(0), a.Delay(time.Date(2026, 10, 10, 7, 0, 0, 0, loc)), "never negative")
	assert.Equal(t, 5*time.Hour, a.Delay(time.Date(2026, 10, 10, 13, 0, 0, 0, loc)))
	assert.Equal(t, 24*time.Hour, a.Delay(time.Date(2026, 10, 11, 8, 0, 0, 0, loc)))
}

func TestDigestWording(t *testing.T) {
	mk := func(trigger, kind, name, detail string, anchor time.Time, off, days int, loc string) Alert {
		return Alert{
			Rule:    Rule{Trigger: trigger},
			Subject: Subject{Name: name, Detail: detail, Anchor: anchor, Kind: kind, Location: loc},
			Offset:  off, DaysUntil: days,
		}
	}
	text := Digest("Home", d(2026, 10, 10), []Alert{
		mk(TriggerExpiresAt, "best_before", "Aspirin", "", d(2026, 10, 13), 3, 3, "Bathroom"),
		mk(TriggerExpiresAt, "expires", "Milk", "", d(2026, 10, 8), -2, -2, ""),
		mk(TriggerWarrantyExpires, "", "Laptop", "", d(2026, 10, 17), 7, 7, ""),
		mk(TriggerMaintenanceDue, "", "Car", "Oil change", d(2026, 10, 10), 0, 0, ""),
		mk(TriggerMaintenanceDue, "", "Boiler", "Service", d(2026, 10, 8), 0, -2, ""),
	})
	assert.Contains(t, text, "Homebox alerts for Home (2026-10-10)")
	assert.Contains(t, text, "- Aspirin: best before in 3 days (2026-10-13), Bathroom")
	assert.Contains(t, text, "- Milk: expired 2 days ago (2026-10-08)")
	assert.Contains(t, text, "- Laptop: warranty ends in 7 days (2026-10-17)")
	assert.Contains(t, text, "- Car - Oil change: due today (2026-10-10)")
	assert.Contains(t, text, "- Boiler - Service: overdue by 2 days (2026-10-08)")
	// Sections are ordered expiry, warranty, maintenance; soonest first inside.
	assert.Less(t, strings.Index(text, "Expiry"), strings.Index(text, "Warranty"))
	assert.Less(t, strings.Index(text, "Warranty"), strings.Index(text, "Maintenance"))
	assert.Less(t, strings.Index(text, "Milk"), strings.Index(text, "Aspirin"), "already expired comes before upcoming")
}

func TestDigestCapsLongSections(t *testing.T) {
	var as []Alert
	for i := 0; i < MaxLinesPerSection+5; i++ {
		as = append(as, Alert{Rule: Rule{Trigger: TriggerExpiresAt}, Subject: Subject{Name: "x", Anchor: d(2026, 10, 12)}, DaysUntil: 2})
	}
	text := Digest("Home", d(2026, 10, 10), as)
	assert.Equal(t, MaxLinesPerSection, strings.Count(text, "\n- "))
	assert.Contains(t, text, "...and 5 more")
}

func TestPhraseSingulars(t *testing.T) {
	assert.Equal(t, "expires tomorrow", phrase(TriggerExpiresAt, "expires", 1))
	assert.Equal(t, "expired yesterday", phrase(TriggerExpiresAt, "expires", -1))
	assert.Equal(t, "use by today", phrase(TriggerExpiresAt, "use_by", 0))
	assert.Equal(t, "overdue by 1 day", phrase(TriggerMaintenanceDue, "", -1))
}
