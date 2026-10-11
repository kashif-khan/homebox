package services

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/sysadminsmedia/homebox/backend/internal/core/alerts"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/data/types"
)

// alertEnv is a collection of its own, so other tests' items never show up.
type alertEnv struct {
	t        *testing.T
	ctx      context.Context
	gid      uuid.UUID
	uid      uuid.UUID
	typeID   uuid.UUID
	loc      *time.Location
	svc      *BackgroundService
	mu       sync.Mutex
	sent     []sentMsg
	failNext map[uuid.UUID]error
}

type sentMsg struct {
	notifier uuid.UUID
	text     string
}

func newAlertEnv(t *testing.T, tz string) *alertEnv {
	t.Helper()
	ctx := context.Background()
	g, err := tRepos.Groups.GroupCreate(ctx, "alerts-"+fk.Str(6), uuid.Nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tRepos.Groups.GroupDelete(ctx, g.ID) })

	tzp := tz
	_, err = tRepos.Groups.GroupUpdate(ctx, g.ID, repo.GroupUpdate{Name: g.Name, Currency: "USD", Timezone: &tzp})
	require.NoError(t, err)

	u, err := tRepos.Users.Create(ctx, repo.UserCreate{
		Name: fk.Str(8), Email: fk.Email(), Password: new(fk.Str(10)), DefaultGroupID: g.ID, IsOwner: true,
	})
	require.NoError(t, err)

	et, err := tRepos.EntityTypes.Create(ctx, g.ID, repo.EntityTypeCreate{Name: "Item"})
	require.NoError(t, err)

	loc, err := time.LoadLocation(tz)
	require.NoError(t, err)

	e := &alertEnv{t: t, ctx: ctx, gid: g.ID, uid: u.ID, typeID: et.ID, loc: loc, failNext: map[uuid.UUID]error{}}
	e.svc = &BackgroundService{repos: tRepos}
	e.svc.SetSender(func(n repo.NotifierOut, message string) error {
		e.mu.Lock()
		defer e.mu.Unlock()
		if err := e.failNext[n.ID]; err != nil {
			delete(e.failNext, n.ID)
			return err
		}
		e.sent = append(e.sent, sentMsg{n.ID, message})
		return nil
	})
	return e
}

func (e *alertEnv) notifier(name string) uuid.UUID {
	n, err := tRepos.Notifiers.Create(e.ctx, e.gid, e.uid, repo.NotifierCreate{Name: name, IsActive: true, URL: "generic://example.invalid/" + name})
	require.NoError(e.t, err)
	return n.ID
}

func (e *alertEnv) item(name string, expires time.Time) repo.EntityOut {
	it, err := tRepos.Entities.Create(e.ctx, e.gid, repo.EntityCreate{Name: name, Quantity: 1, EntityTypeID: e.typeID})
	require.NoError(e.t, err)
	e.setExpiry(it, expires)
	return it
}

func (e *alertEnv) setExpiry(it repo.EntityOut, expires time.Time) {
	_, err := tRepos.Entities.UpdateByGroup(e.ctx, e.gid, repo.EntityUpdate{
		ID: it.ID, Name: it.Name, Quantity: 1, EntityTypeID: e.typeID, ExpiresAt: types.DateFromTime(expires),
	})
	require.NoError(e.t, err)
}

func (e *alertEnv) rule(in repo.NotificationRuleIn) repo.NotificationRuleOut {
	in.IsActive = true
	if in.Trigger == "" {
		in.Trigger = alerts.TriggerExpiresAt
	}
	r, err := tRepos.NotificationRules.Create(e.ctx, e.gid, in)
	require.NoError(e.t, err)
	return r
}

// at is a wall-clock time in the collection's zone.
func (e *alertEnv) at(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, e.loc)
}

func (e *alertEnv) tick(now time.Time) {
	e.t.Helper()
	require.NoError(e.t, e.svc.AlertTick(e.ctx, now))
}

func (e *alertEnv) messages() []sentMsg {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]sentMsg(nil), e.sent...)
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestAlerts_WaitsForTheCollectionsSendHour(t *testing.T) {
	e := newAlertEnv(t, "Asia/Karachi")
	e.notifier("n1")
	e.item("Aspirin", day(2030, 6, 17))
	e.rule(repo.NotificationRuleIn{Name: "r", OffsetsDays: []int{7}})

	// 07:59 in Karachi. In UTC that is 02:59, which a server-local "hour == 8"
	// check would have mistaken for the middle of the night anywhere.
	e.tick(e.at(2030, 6, 10, 7, 59))
	assert.Empty(t, e.messages(), "before the send hour")

	e.tick(e.at(2030, 6, 10, 8, 0))
	msgs := e.messages()
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0].text, "Aspirin: expires in 7 days (2030-06-17)")
	assert.Contains(t, msgs[0].text, "(2030-06-10)", "dated in the collection's zone")
}

func TestAlerts_FireOncePerOffsetEvenIfTickedRepeatedly(t *testing.T) {
	e := newAlertEnv(t, "UTC")
	e.notifier("n1")
	e.item("Milk", day(2030, 6, 17))
	e.rule(repo.NotificationRuleIn{Name: "r", OffsetsDays: []int{7, 1}})

	for i := 0; i < 5; i++ {
		e.tick(e.at(2030, 6, 10, 9+i, 0))
	}
	assert.Len(t, e.messages(), 1, "the 7-day alert went out once")

	// The next alert is the next day, and only that one.
	e.tick(e.at(2030, 6, 16, 9, 0))
	e.tick(e.at(2030, 6, 16, 12, 0))
	msgs := e.messages()
	require.Len(t, msgs, 2)
	assert.Contains(t, msgs[1].text, "Milk: expires tomorrow")
	assert.NotContains(t, msgs[1].text, "in 7 days")
}

func TestAlerts_CatchUpAfterDowntimeButNotForeverStale(t *testing.T) {
	e := newAlertEnv(t, "UTC")
	e.notifier("n1")
	e.item("Yoghurt", day(2030, 6, 20))
	e.rule(repo.NotificationRuleIn{Name: "r", OffsetsDays: []int{0}})

	// Server was down on the 20th and came back on the 22nd.
	e.tick(e.at(2030, 6, 19, 9, 0))
	assert.Empty(t, e.messages(), "not due yet")
	e.tick(e.at(2030, 6, 22, 9, 0))
	require.Len(t, e.messages(), 1, "caught up two days late")
	assert.Contains(t, e.messages()[0].text, "expired 2 days ago")

	// A different item whose day is more than a week gone is not announced.
	e.item("Ancient", day(2030, 6, 1))
	e.tick(e.at(2030, 6, 22, 10, 0))
	assert.Len(t, e.messages(), 1, "stale alerts are skipped, not flooded out")
}

func TestAlerts_FailedDeliveryIsRetriedNotLost(t *testing.T) {
	e := newAlertEnv(t, "UTC")
	n := e.notifier("n1")
	e.item("Cheese", day(2030, 6, 11))
	e.rule(repo.NotificationRuleIn{Name: "r", OffsetsDays: []int{1}})

	e.failNext[n] = errors.New("smtp down")
	err := e.svc.AlertTick(e.ctx, e.at(2030, 6, 10, 9, 0))
	require.Error(t, err)
	assert.Empty(t, e.messages())

	e.tick(e.at(2030, 6, 10, 9, 5))
	require.Len(t, e.messages(), 1, "the next tick delivers it")
	e.tick(e.at(2030, 6, 10, 9, 10))
	assert.Len(t, e.messages(), 1)
}

func TestAlerts_MovingTheDateMakesItDueAgain(t *testing.T) {
	e := newAlertEnv(t, "UTC")
	e.notifier("n1")
	it := e.item("Jam", day(2030, 6, 11))
	e.rule(repo.NotificationRuleIn{Name: "r", OffsetsDays: []int{1}})

	e.tick(e.at(2030, 6, 10, 9, 0))
	require.Len(t, e.messages(), 1)

	// Correcting the date to a day later gives a fresh alert for the new date.
	e.setExpiry(it, day(2030, 6, 12))
	e.tick(e.at(2030, 6, 11, 9, 0))
	require.Len(t, e.messages(), 2)
	assert.Contains(t, e.messages()[1].text, "Jam: expires tomorrow (2030-06-12)")
}

func TestAlerts_RuleFiltersAndNotifierSelection(t *testing.T) {
	e := newAlertEnv(t, "UTC")
	a, b := e.notifier("a"), e.notifier("b")
	e.item("Pills", day(2030, 6, 11))

	// Other type: never matches.
	other, err := tRepos.EntityTypes.Create(e.ctx, e.gid, repo.EntityTypeCreate{Name: "Other"})
	require.NoError(t, err)
	e.rule(repo.NotificationRuleIn{Name: "wrong type", OffsetsDays: []int{1}, EntityTypeID: &other.ID, NotifierIDs: []uuid.UUID{a}})
	e.tick(e.at(2030, 6, 10, 9, 0))
	assert.Empty(t, e.messages())

	// Right type, only notifier b.
	e.rule(repo.NotificationRuleIn{Name: "right type", OffsetsDays: []int{1}, EntityTypeID: &e.typeID, NotifierIDs: []uuid.UUID{b}})
	e.tick(e.at(2030, 6, 10, 9, 5))
	msgs := e.messages()
	require.Len(t, msgs, 1)
	assert.Equal(t, b, msgs[0].notifier)
}

func TestAlerts_OneDigestPerNotifierAcrossRules(t *testing.T) {
	e := newAlertEnv(t, "UTC")
	e.notifier("n1")
	e.item("Pills", day(2030, 6, 11))
	e.item("Rice", day(2030, 6, 17))
	e.rule(repo.NotificationRuleIn{Name: "tomorrow", OffsetsDays: []int{1}})
	e.rule(repo.NotificationRuleIn{Name: "week", OffsetsDays: []int{7}})

	e.tick(e.at(2030, 6, 10, 9, 0))
	msgs := e.messages()
	require.Len(t, msgs, 1, "two rules, one message")
	assert.Contains(t, msgs[0].text, "Pills")
	assert.Contains(t, msgs[0].text, "Rice")
}

func TestAlerts_InactiveRuleAndArchivedItemsAreSilent(t *testing.T) {
	e := newAlertEnv(t, "UTC")
	e.notifier("n1")
	it := e.item("Pills", day(2030, 6, 11))
	r := e.rule(repo.NotificationRuleIn{Name: "r", OffsetsDays: []int{1}})

	in := repo.NotificationRuleIn{Name: r.Name, Trigger: r.Trigger, OffsetsDays: r.OffsetsDays, IsActive: false}
	_, err := tRepos.NotificationRules.Update(e.ctx, e.gid, r.ID, in)
	require.NoError(t, err)
	e.tick(e.at(2030, 6, 10, 9, 0))
	assert.Empty(t, e.messages(), "rule switched off")

	in.IsActive = true
	_, err = tRepos.NotificationRules.Update(e.ctx, e.gid, r.ID, in)
	require.NoError(t, err)
	require.NoError(t, tRepos.Entities.Patch(e.ctx, e.gid, it.ID, repo.EntityPatch{ID: it.ID}))
	_, err = tRepos.Entities.UpdateByGroup(e.ctx, e.gid, repo.EntityUpdate{
		ID: it.ID, Name: it.Name, Quantity: 1, EntityTypeID: e.typeID, Archived: true, ExpiresAt: types.DateFromTime(day(2030, 6, 11)),
	})
	require.NoError(t, err)
	e.tick(e.at(2030, 6, 10, 9, 5))
	assert.Empty(t, e.messages(), "archived items are not announced")
}

func TestAlerts_WarrantyAndMaintenance(t *testing.T) {
	e := newAlertEnv(t, "UTC")
	e.notifier("n1")
	it := e.item("Laptop", day(2031, 1, 1))
	_, err := tRepos.Entities.UpdateByGroup(e.ctx, e.gid, repo.EntityUpdate{
		ID: it.ID, Name: it.Name, Quantity: 1, EntityTypeID: e.typeID, WarrantyExpires: types.DateFromTime(day(2030, 6, 17)),
	})
	require.NoError(t, err)
	_, err = tRepos.MaintEntry.Create(e.ctx, e.gid, it.ID, repo.MaintenanceEntryCreate{
		Name: "Clean fans", ScheduledDate: types.DateFromTime(day(2030, 6, 10)),
	})
	require.NoError(t, err)

	e.rule(repo.NotificationRuleIn{Name: "warranty", Trigger: alerts.TriggerWarrantyExpires, OffsetsDays: []int{7}})
	e.rule(repo.NotificationRuleIn{Name: "maint", Trigger: alerts.TriggerMaintenanceDue, OffsetsDays: []int{0}})
	e.tick(e.at(2030, 6, 10, 9, 0))

	msgs := e.messages()
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0].text, "Laptop: warranty ends in 7 days (2030-06-17)")
	assert.Contains(t, msgs[0].text, "Laptop - Clean fans: due today (2030-06-10)")
}

func TestAlerts_LegacyMaintenanceDigestIsSeededOnce(t *testing.T) {
	e := newAlertEnv(t, "UTC")
	// No notifier yet: nothing to seed.
	e.tick(e.at(2030, 6, 10, 9, 0))
	rules, err := tRepos.NotificationRules.GetAll(e.ctx, e.gid)
	require.NoError(t, err)
	assert.Empty(t, rules)

	e.notifier("n1")
	e.tick(e.at(2030, 6, 10, 9, 5))
	rules, err = tRepos.NotificationRules.GetAll(e.ctx, e.gid)
	require.NoError(t, err)
	require.Len(t, rules, 1)
	assert.Equal(t, alerts.TriggerMaintenanceDue, rules[0].Trigger)
	assert.Equal(t, []int{0}, rules[0].OffsetsDays)

	// Deleting it sticks.
	require.NoError(t, tRepos.NotificationRules.Delete(e.ctx, e.gid, rules[0].ID))
	e.tick(e.at(2030, 6, 10, 9, 10))
	rules, err = tRepos.NotificationRules.GetAll(e.ctx, e.gid)
	require.NoError(t, err)
	assert.Empty(t, rules)
}

func TestAlerts_SinkSeesDueAlertsEvenWithoutNotifiers(t *testing.T) {
	e := newAlertEnv(t, "UTC")
	e.item("Pills", day(2030, 6, 11))
	e.rule(repo.NotificationRuleIn{Name: "r", OffsetsDays: []int{1}})
	sink := &recordingSink{}
	e.svc.SetAlertSink(sink)

	e.tick(e.at(2030, 6, 10, 9, 0))
	require.Len(t, sink.got, 1)
	assert.Equal(t, "Pills", sink.got[0].Subject.Name)
	assert.Empty(t, e.messages(), "no notifiers, nothing sent")

	// It is offered again on the next tick: the sink must de-duplicate itself.
	e.tick(e.at(2030, 6, 10, 9, 5))
	assert.Len(t, sink.got, 2)
}

type recordingSink struct{ got []alerts.Alert }

func (s *recordingSink) Alerts(_ context.Context, _ repo.Group, _ time.Time, due []alerts.Alert) {
	s.got = append(s.got, due...)
}

func TestNotificationRule_Validation(t *testing.T) {
	e := newAlertEnv(t, "UTC")
	ctx := e.ctx
	ok := repo.NotificationRuleIn{Name: "r", Trigger: alerts.TriggerExpiresAt, OffsetsDays: []int{30, 7, 7, 1, 0, -1}, IsActive: true}

	created, err := tRepos.NotificationRules.Create(ctx, e.gid, ok)
	require.NoError(t, err)
	assert.Equal(t, []int{30, 7, 1, 0, -1}, created.OffsetsDays, "de-duplicated and ordered")

	bad := map[string]repo.NotificationRuleIn{
		"no name":        {Trigger: ok.Trigger, OffsetsDays: ok.OffsetsDays},
		"bad trigger":    {Name: "x", Trigger: "birthday", OffsetsDays: ok.OffsetsDays},
		"no offsets":     {Name: "x", Trigger: ok.Trigger},
		"huge offset":    {Name: "x", Trigger: ok.Trigger, OffsetsDays: []int{99999}},
		"too many":       {Name: "x", Trigger: ok.Trigger, OffsetsDays: seq(alerts.MaxOffsets + 1)},
		"foreign notify": {Name: "x", Trigger: ok.Trigger, OffsetsDays: []int{1}, NotifierIDs: []uuid.UUID{uuid.New()}},
		"foreign type":   {Name: "x", Trigger: ok.Trigger, OffsetsDays: []int{1}, EntityTypeID: ptr(uuid.New())},
		"foreign tag":    {Name: "x", Trigger: ok.Trigger, OffsetsDays: []int{1}, TagID: ptr(uuid.New())},
	}
	for name, in := range bad {
		_, err := tRepos.NotificationRules.Create(ctx, e.gid, in)
		require.Error(t, err, name)
		assert.True(t, strings.Contains(err.Error(), "must") || strings.Contains(err.Error(), "needs") || strings.Contains(err.Error(), "within") || strings.Contains(err.Error(), "not "), name+": "+err.Error())
	}

	// Another collection cannot touch it.
	other, err := tRepos.Groups.GroupCreate(ctx, "other-"+fk.Str(5), uuid.Nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tRepos.Groups.GroupDelete(ctx, other.ID) })
	_, err = tRepos.NotificationRules.GetOne(ctx, other.ID, created.ID)
	require.Error(t, err)
	require.Error(t, tRepos.NotificationRules.Delete(ctx, other.ID, created.ID))
	_, err = tRepos.NotificationRules.Update(ctx, other.ID, created.ID, ok)
	require.Error(t, err)
}

func seq(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func ptr[T any](v T) *T { return &v }
