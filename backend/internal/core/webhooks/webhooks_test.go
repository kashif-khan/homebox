package webhooks

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/sysadminsmedia/homebox/backend/internal/core/alerts"
)

func TestEventForAlert(t *testing.T) {
	mk := func(trigger string, days int) alerts.Alert {
		return alerts.Alert{Rule: alerts.Rule{Trigger: trigger}, DaysUntil: days}
	}
	cases := map[string]string{
		"expires_at/3":        ExpiryUpcoming,
		"expires_at/0":        ExpiryUpcoming,
		"expires_at/-1":       ExpiryExpired,
		"warranty_expires/10": WarrantyUpcoming,
		"warranty_expires/-5": WarrantyExpired,
		"maintenance_due/0":   MaintenanceDue,
		"maintenance_due/-2":  MaintenanceOverdue,
	}
	got := map[string]string{
		"expires_at/3":        EventForAlert(mk("expires_at", 3)),
		"expires_at/0":        EventForAlert(mk("expires_at", 0)),
		"expires_at/-1":       EventForAlert(mk("expires_at", -1)),
		"warranty_expires/10": EventForAlert(mk("warranty_expires", 10)),
		"warranty_expires/-5": EventForAlert(mk("warranty_expires", -5)),
		"maintenance_due/0":   EventForAlert(mk("maintenance_due", 0)),
		"maintenance_due/-2":  EventForAlert(mk("maintenance_due", -2)),
	}
	assert.Equal(t, cases, got)
	assert.Empty(t, EventForAlert(mk("birthday", 0)))
}

func TestEveryAlertEventIsSubscribable(t *testing.T) {
	for _, tr := range []string{alerts.TriggerExpiresAt, alerts.TriggerWarrantyExpires, alerts.TriggerMaintenanceDue} {
		for _, days := range []int{5, -5} {
			name := EventForAlert(alerts.Alert{Rule: alerts.Rule{Trigger: tr}, DaysUntil: days})
			assert.True(t, ValidEvent(name), name)
		}
	}
	assert.False(t, ValidEvent(Test), "the test event is not something to subscribe to")
	assert.False(t, ValidEvent("item.exploded"))
}

func TestAlertKeyIsStablePerDateAndChangesWithIt(t *testing.T) {
	rule := alerts.Rule{ID: uuid.New()}
	sub := alerts.Subject{ID: uuid.New(), Anchor: time.Date(2030, 6, 17, 0, 0, 0, 0, time.UTC)}
	a := alerts.Alert{Rule: rule, Subject: sub, Offset: 7}
	assert.Equal(t, AlertKey(a), AlertKey(a))

	moved := a
	moved.Subject.Anchor = sub.Anchor.AddDate(0, 0, 1)
	assert.NotEqual(t, AlertKey(a), AlertKey(moved), "a corrected date is a new alert")
	other := a
	other.Offset = 1
	assert.NotEqual(t, AlertKey(a), AlertKey(other))
}

func TestSignAndVerify(t *testing.T) {
	ts := time.Unix(1_900_000_000, 0)
	body := []byte(`{"a":1}`)
	sig := Sign("secret", ts, body)
	assert.True(t, strings.HasPrefix(sig, "sha256="))
	assert.True(t, Verify("secret", sig, ts, body))
	assert.False(t, Verify("other", sig, ts, body), "wrong secret")
	assert.False(t, Verify("secret", sig, ts.Add(time.Second), body), "the timestamp is signed, so it cannot be swapped")
	assert.False(t, Verify("secret", sig, ts, []byte(`{"a":2}`)), "tampered body")
}

func TestBodyCarriesLatenessAndZone(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Karachi")
	require.NoError(t, err)
	sched := time.Date(2030, 6, 10, 8, 0, 0, 0, loc)
	d := Delivery{
		ID: uuid.New(), EventID: uuid.New(), Type: ExpiryUpcoming, GroupID: uuid.New(),
		OccurredAt: sched, ScheduledAt: &sched, CreatedAt: sched.Add(5 * time.Hour),
		Data: map[string]any{"x": 1},
	}

	// Sent promptly.
	raw, err := Body(d, 1, sched.Add(time.Minute), loc, "Asia/Karachi")
	require.NoError(t, err)
	var b map[string]any
	require.NoError(t, json.Unmarshal(raw, &b))
	assert.Equal(t, false, b["delayed"])
	assert.Equal(t, "2030-06-10T08:00:00+05:00", b["scheduled_for"], "offset is explicit")
	assert.Equal(t, "Asia/Karachi", b["collection_timezone"])
	assert.EqualValues(t, 60, b["delay_seconds"])
	assert.EqualValues(t, 1, b["attempt"])

	// The server was down and it goes out five hours late, on its third try.
	raw, err = Body(d, 3, sched.Add(5*time.Hour), loc, "Asia/Karachi")
	require.NoError(t, err)
	b = nil
	require.NoError(t, json.Unmarshal(raw, &b))
	assert.Equal(t, true, b["delayed"])
	assert.EqualValues(t, 5*3600, b["delay_seconds"])
	assert.Equal(t, "2030-06-10T13:00:00+05:00", b["delivered_at"])
	assert.Equal(t, "2030-06-10T08:00:00+05:00", b["occurred_at"], "when it was meant to happen is unchanged")
	assert.EqualValues(t, 3, b["attempt"])

	// The same instants read in another zone are the same instants.
	utc, err := Body(d, 1, sched.Add(time.Minute), time.UTC, "UTC")
	require.NoError(t, err)
	b = nil
	require.NoError(t, json.Unmarshal(utc, &b))
	assert.Equal(t, "2030-06-10T03:00:00Z", b["scheduled_for"])
}

func TestBodyForUnscheduledEventMeasuresFromOccurrence(t *testing.T) {
	occurred := time.Date(2030, 6, 10, 8, 0, 0, 0, time.UTC)
	d := Delivery{ID: uuid.New(), EventID: uuid.New(), Type: ItemCreated, OccurredAt: occurred, CreatedAt: occurred}
	raw, err := Body(d, 4, occurred.Add(2*time.Hour), time.UTC, "UTC")
	require.NoError(t, err)
	var b map[string]any
	require.NoError(t, json.Unmarshal(raw, &b))
	assert.Nil(t, b["scheduled_for"])
	assert.Equal(t, true, b["delayed"])
	assert.EqualValues(t, 7200, b["delay_seconds"])
}

func TestDelayNeverNegative(t *testing.T) {
	at := time.Date(2030, 6, 10, 8, 0, 0, 0, time.UTC)
	d := Delivery{ID: uuid.New(), Type: ItemCreated, OccurredAt: at.Add(time.Hour), CreatedAt: at}
	raw, err := Body(d, 1, at, time.UTC, "UTC")
	require.NoError(t, err)
	var b map[string]any
	require.NoError(t, json.Unmarshal(raw, &b))
	assert.EqualValues(t, 0, b["delay_seconds"])
	assert.Equal(t, false, b["delayed"])
}

func TestNextAttemptBacksOffAndGivesUp(t *testing.T) {
	created := time.Date(2030, 6, 10, 8, 0, 0, 0, time.UTC)
	now := created
	maxAge := 24 * time.Hour

	var waits []time.Duration
	for failed := 1; ; failed++ {
		next, ok := NextAttempt(failed, created, now, maxAge)
		if !ok {
			break
		}
		waits = append(waits, next.Sub(now))
		now = next
		require.Less(t, failed, 50, "must terminate")
	}
	assert.Equal(t, 30*time.Second, waits[0])
	assert.Equal(t, 2*time.Minute, waits[1])
	for i := 1; i < len(waits); i++ {
		assert.Greater(t, waits[i], waits[i-1], "waits grow")
	}
	assert.LessOrEqual(t, now.Sub(created), maxAge, "never schedules past the max age")
	assert.GreaterOrEqual(t, len(waits), 6, "a receiver that is down for hours still gets several tries")

	_, ok := NextAttempt(1, created, created.Add(23*time.Hour+59*time.Minute+45*time.Second), maxAge)
	assert.False(t, ok, "too old to retry")
	_, ok = NextAttempt(0, created, created, maxAge)
	assert.False(t, ok)
}

func TestNewSecret(t *testing.T) {
	a, b := NewSecret(), NewSecret()
	assert.NotEqual(t, a, b)
	assert.True(t, strings.HasPrefix(a, "whsec_"))
	assert.GreaterOrEqual(t, len(a), 32)
}
