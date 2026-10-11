package services

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/sysadminsmedia/homebox/backend/internal/core/webhooks"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
)

// receiver is a webhook endpoint under test.
type receiver struct {
	srv    *httptest.Server
	mu     sync.Mutex
	got    []received
	status int // what to answer; 0 means 200
}

type received struct {
	header http.Header
	body   []byte
	json   map[string]any
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()
	r := &receiver{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		r.mu.Lock()
		r.got = append(r.got, received{req.Header.Clone(), b, m})
		st := r.status
		r.mu.Unlock()
		if st == 0 {
			st = http.StatusOK
		}
		w.WriteHeader(st)
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *receiver) answer(status int) { r.mu.Lock(); r.status = status; r.mu.Unlock() }
func (r *receiver) received() []received {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]received(nil), r.got...)
}

// whEnv is a collection wired to a webhook service with a controllable clock.
type whEnv struct {
	*alertEnv
	svc *WebhookService
	clk *time.Time
}

func newWhEnv(t *testing.T, tz string) *whEnv {
	t.Helper()
	ae := newAlertEnv(t, tz)
	cfg := config.WebhooksConf{Enabled: true, Timeout: 2 * time.Second, MaxAge: 24 * time.Hour, MaxEndpoints: 20, RatePerMinute: 0}
	// The test receiver is on loopback, which production policy refuses.
	nc := &config.NotifierConf{BlockLocalhost: false, BlockBogonNets: false}
	svc := NewWebhookService(tRepos, cfg, nc)
	clk := time.Date(2030, 6, 10, 8, 0, 0, 0, ae.loc)
	svc.now = func() time.Time { return clk }
	tRepos.SetEventSink(svc)
	ae.svc.SetAlertSink(svc)
	t.Cleanup(func() { tRepos.SetEventSink(nil) })
	return &whEnv{alertEnv: ae, svc: svc, clk: &clk}
}

func (e *whEnv) advance(d time.Duration) { *e.clk = e.clk.Add(d) }

func (e *whEnv) endpoint(url string, events ...string) repo.WebhookEndpointCreated {
	u := url
	ep, err := tRepos.Webhooks.Create(e.ctx, e.gid, repo.WebhookEndpointIn{Name: "test", URL: &u, Events: events, IsActive: true}, webhooks.NewSecret())
	require.NoError(e.t, err)
	return ep
}

func (e *whEnv) deliveries(ep repo.WebhookEndpointCreated) []repo.WebhookDeliveryOut {
	d, _, err := tRepos.Webhooks.Deliveries(e.ctx, e.gid, ep.ID, 100, 0)
	require.NoError(e.t, err)
	return d
}

func TestWebhooks_ItemEventsAreSignedAndDelivered(t *testing.T) {
	e := newWhEnv(t, "Asia/Karachi")
	rcv := newReceiver(t)
	ep := e.endpoint(rcv.srv.URL+"/hook?token=sekrit", webhooks.ItemCreated, webhooks.ItemUpdated, webhooks.ItemMoved, webhooks.ItemQuantityChanged, webhooks.ItemDeleted)

	it, err := tRepos.Entities.Create(e.ctx, e.gid, repo.EntityCreate{Name: "Drill", Quantity: 1, EntityTypeID: e.typeID})
	require.NoError(t, err)
	e.svc.ProcessDue(e.ctx)

	got := rcv.received()
	require.Len(t, got, 1)
	r := got[0]
	assert.Equal(t, "item.created", r.header.Get("X-Homebox-Event"))
	assert.Equal(t, "1", r.header.Get("X-Homebox-Attempt"))
	assert.NotEmpty(t, r.header.Get("X-Homebox-Delivery"))

	// The receiver can verify it with the secret it was given.
	ts, err := strconv.ParseInt(r.header.Get("X-Homebox-Timestamp"), 10, 64)
	require.NoError(t, err)
	sig := r.header.Get("X-Homebox-Signature")
	assert.True(t, webhooks.Verify(ep.Secret, sig, time.Unix(ts, 0), r.body))
	assert.False(t, webhooks.Verify("wrong", sig, time.Unix(ts, 0), r.body))
	assert.False(t, webhooks.Verify(ep.Secret, sig, time.Unix(ts+1, 0), r.body), "the timestamp is part of what is signed")

	assert.Equal(t, r.header.Get("X-Homebox-Delivery"), r.json["id"])
	assert.Equal(t, "item.created", r.json["type"])
	assert.Equal(t, "Asia/Karachi", r.json["collection_timezone"])
	assert.Equal(t, false, r.json["delayed"])
	item := r.json["data"].(map[string]any)["item"].(map[string]any)
	assert.Equal(t, "Drill", item["name"])
	assert.Equal(t, it.ID.String(), item["id"])

	// An edit lists what changed, a quantity change and a move get their own events.
	loc2, err := tRepos.Entities.Create(e.ctx, e.gid, repo.EntityCreate{Name: "Shed", Quantity: 1, EntityTypeID: e.typeID})
	require.NoError(t, err)
	e.advance(time.Minute)
	_, err = tRepos.Entities.UpdateByGroup(e.ctx, e.gid, repo.EntityUpdate{
		ID: it.ID, Name: "Cordless drill", Quantity: 3, EntityTypeID: e.typeID, ParentID: loc2.ID,
	})
	require.NoError(t, err)
	e.svc.ProcessDue(e.ctx)

	byType := map[string]map[string]any{}
	for _, x := range rcv.received() {
		byType[x.header.Get("X-Homebox-Event")] = x.json
	}
	require.Contains(t, byType, "item.updated")
	require.Contains(t, byType, "item.moved")
	require.Contains(t, byType, "item.quantity_changed")
	var fields []string
	for _, c := range byType["item.updated"]["data"].(map[string]any)["changes"].([]any) {
		fields = append(fields, c.(map[string]any)["field"].(string))
	}
	assert.Contains(t, fields, "name")
	assert.Contains(t, fields, "quantity")
	assert.Contains(t, fields, "parent_id")
	q := byType["item.quantity_changed"]["data"].(map[string]any)
	assert.EqualValues(t, 1, q["before"])
	assert.EqualValues(t, 3, q["after"])

	// Saving with no change says nothing.
	before := len(rcv.received())
	_, err = tRepos.Entities.UpdateByGroup(e.ctx, e.gid, repo.EntityUpdate{
		ID: it.ID, Name: "Cordless drill", Quantity: 3, EntityTypeID: e.typeID, ParentID: loc2.ID,
	})
	require.NoError(t, err)
	e.svc.ProcessDue(e.ctx)
	assert.Len(t, rcv.received(), before)

	require.NoError(t, tRepos.Entities.Delete(e.ctx, it.ID))
	e.svc.ProcessDue(e.ctx)
	last := rcv.received()[len(rcv.received())-1]
	assert.Equal(t, "item.deleted", last.header.Get("X-Homebox-Event"))
	assert.Equal(t, "Cordless drill", last.json["data"].(map[string]any)["item"].(map[string]any)["name"])
}

func TestWebhooks_OnlySubscribedEventsAndFilters(t *testing.T) {
	e := newWhEnv(t, "UTC")
	rcv := newReceiver(t)
	other, err := tRepos.EntityTypes.Create(e.ctx, e.gid, repo.EntityTypeCreate{Name: "Tools"})
	require.NoError(t, err)

	u := rcv.srv.URL
	_, err = tRepos.Webhooks.Create(e.ctx, e.gid, repo.WebhookEndpointIn{
		Name: "tools only", URL: &u, Events: []string{webhooks.ItemCreated}, EntityTypeID: &other.ID, IsActive: true,
	}, webhooks.NewSecret())
	require.NoError(t, err)

	_, err = tRepos.Entities.Create(e.ctx, e.gid, repo.EntityCreate{Name: "Not a tool", Quantity: 1, EntityTypeID: e.typeID})
	require.NoError(t, err)
	e.svc.ProcessDue(e.ctx)
	assert.Empty(t, rcv.received(), "wrong entity type")

	_, err = tRepos.Entities.Create(e.ctx, e.gid, repo.EntityCreate{Name: "A tool", Quantity: 1, EntityTypeID: other.ID})
	require.NoError(t, err)
	e.svc.ProcessDue(e.ctx)
	require.Len(t, rcv.received(), 1)

	// Not subscribed to updates, so an edit is silent.
	_, err = tRepos.Entities.UpdateByGroup(e.ctx, e.gid, repo.EntityUpdate{
		ID: uuid.Nil, Name: "x", Quantity: 1, EntityTypeID: other.ID,
	})
	require.Error(t, err)
	assert.Len(t, rcv.received(), 1)
}

func TestWebhooks_RetriesWithBackoffAndTellsTheReceiverItIsLate(t *testing.T) {
	e := newWhEnv(t, "UTC")
	rcv := newReceiver(t)
	rcv.answer(http.StatusServiceUnavailable)
	ep := e.endpoint(rcv.srv.URL, webhooks.ItemCreated)

	_, err := tRepos.Entities.Create(e.ctx, e.gid, repo.EntityCreate{Name: "Kettle", Quantity: 1, EntityTypeID: e.typeID})
	require.NoError(t, err)
	e.svc.ProcessDue(e.ctx)
	require.Len(t, rcv.received(), 1)
	d := e.deliveries(ep)
	require.Len(t, d, 1)
	assert.Equal(t, "pending", d[0].Status)
	assert.Equal(t, 1, d[0].Attempts)
	assert.Equal(t, 503, d[0].LastStatusCode)
	require.NotNil(t, d[0].NextAttemptAt)
	assert.Equal(t, 30*time.Second, d[0].NextAttemptAt.Sub(*e.clk).Round(time.Second), "first retry after 30s")

	// Nothing to do until the backoff has passed.
	e.svc.ProcessDue(e.ctx)
	assert.Len(t, rcv.received(), 1)

	// The receiver comes back four hours later.
	rcv.answer(http.StatusOK)
	e.advance(4 * time.Hour)
	e.svc.ProcessDue(e.ctx)
	got := rcv.received()
	require.Len(t, got, 2)
	last := got[1]
	assert.Equal(t, "2", last.header.Get("X-Homebox-Attempt"))
	assert.Equal(t, true, last.json["delayed"])
	assert.EqualValues(t, 4*3600, last.json["delay_seconds"])
	assert.Equal(t, "4", strconv.Itoa(int(4*time.Hour/time.Hour)), "sanity")
	assert.Equal(t, strconv.Itoa(4*3600), last.header.Get("X-Homebox-Delay-Seconds"))
	assert.Equal(t, "2030-06-10T08:00:00Z", last.json["occurred_at"], "the original time is kept")
	assert.Equal(t, "2030-06-10T12:00:00Z", last.json["delivered_at"])
	assert.Equal(t, got[0].header.Get("X-Homebox-Delivery"), last.header.Get("X-Homebox-Delivery"), "same delivery id so it can be de-duplicated")

	d = e.deliveries(ep)
	assert.Equal(t, "delivered", d[0].Status)
	assert.Equal(t, 2, d[0].Attempts)
}

func TestWebhooks_GivesUpWhenTooOldOrGone(t *testing.T) {
	e := newWhEnv(t, "UTC")
	rcv := newReceiver(t)
	rcv.answer(http.StatusInternalServerError)
	ep := e.endpoint(rcv.srv.URL, webhooks.ItemCreated)

	_, err := tRepos.Entities.Create(e.ctx, e.gid, repo.EntityCreate{Name: "A", Quantity: 1, EntityTypeID: e.typeID})
	require.NoError(t, err)

	// Keep failing until the schedule is exhausted.
	for i := 0; i < 20; i++ {
		e.svc.ProcessDue(e.ctx)
		e.advance(9 * time.Hour)
	}
	d := e.deliveries(ep)
	require.Len(t, d, 1)
	assert.Equal(t, "failed", d[0].Status)
	assert.Contains(t, d[0].LastError, "500")

	// 410 Gone ends it at once.
	rcv.answer(http.StatusGone)
	_, err = tRepos.Entities.Create(e.ctx, e.gid, repo.EntityCreate{Name: "B", Quantity: 1, EntityTypeID: e.typeID})
	require.NoError(t, err)
	e.svc.ProcessDue(e.ctx)
	var gone repo.WebhookDeliveryOut
	for _, x := range e.deliveries(ep) {
		if x.LastStatusCode == http.StatusGone {
			gone = x
		}
	}
	assert.Equal(t, "failed", gone.Status)
	assert.Equal(t, 1, gone.Attempts)
}

func TestWebhooks_RedirectsAreNotFollowedAndPolicyIsEnforced(t *testing.T) {
	e := newWhEnv(t, "UTC")
	target := newReceiver(t)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.srv.URL, http.StatusFound)
	}))
	defer redirector.Close()
	ep := e.endpoint(redirector.URL+"/secret-token", webhooks.ItemCreated)

	_, err := tRepos.Entities.Create(e.ctx, e.gid, repo.EntityCreate{Name: "A", Quantity: 1, EntityTypeID: e.typeID})
	require.NoError(t, err)
	e.svc.ProcessDue(e.ctx)
	assert.Empty(t, target.received(), "the redirect was not chased")
	d := e.deliveries(ep)
	require.Len(t, d, 1)
	assert.Equal(t, 302, d[0].LastStatusCode)

	// With the production policy loopback is refused, and the stored error does
	// not echo the URL (which could carry a token).
	strict := NewWebhookService(tRepos, e.svc.cfg, &config.NotifierConf{BlockLocalhost: true, BlockBogonNets: true})
	strict.now = e.svc.now
	e.advance(time.Minute)
	strict.ProcessDue(e.ctx)
	d = e.deliveries(ep)
	assert.Equal(t, "failed", d[0].Status)
	assert.NotContains(t, d[0].LastError, "secret-token")
	assert.NotEmpty(t, d[0].LastError)
}

func TestWebhooks_DateAlertsCatchUpAndSayHowLate(t *testing.T) {
	e := newWhEnv(t, "Asia/Karachi")
	rcv := newReceiver(t)
	e.item("Aspirin", day(2030, 6, 17))
	e.rule(repo.NotificationRuleIn{Name: "week", OffsetsDays: []int{7}})
	ep := e.endpoint(rcv.srv.URL, webhooks.ExpiryUpcoming, webhooks.ExpiryExpired)

	// The alert is due 08:00 Karachi on the 10th. The server is down then and
	// comes back at 13:30 the same day, with the receiver also unreachable.
	rcv.answer(http.StatusBadGateway)
	*e.clk = e.at(2030, 6, 10, 13, 30)
	require.NoError(t, e.alertEnv.svc.AlertTick(e.ctx, *e.clk))
	e.svc.ProcessDue(e.ctx)
	assert.Len(t, rcv.received(), 1)

	// Further ticks do not queue the same alert again.
	*e.clk = e.at(2030, 6, 10, 14, 0)
	require.NoError(t, e.alertEnv.svc.AlertTick(e.ctx, *e.clk))
	assert.Len(t, e.deliveries(ep), 1, "one delivery per alert per endpoint")

	// Receiver recovers that evening.
	rcv.answer(http.StatusOK)
	*e.clk = e.at(2030, 6, 10, 20, 0)
	e.svc.ProcessDue(e.ctx)
	got := rcv.received()
	last := got[len(got)-1]
	assert.Equal(t, "expiry.upcoming", last.header.Get("X-Homebox-Event"))
	assert.Equal(t, "2030-06-10T08:00:00+05:00", last.json["scheduled_for"], "when it was meant to go out, with the zone's offset")
	assert.Equal(t, "2030-06-10T20:00:00+05:00", last.json["delivered_at"])
	assert.Equal(t, true, last.json["delayed"])
	assert.EqualValues(t, 12*3600, last.json["delay_seconds"])
	data := last.json["data"].(map[string]any)
	assert.EqualValues(t, 7, data["offset"])
	assert.EqualValues(t, 7, data["days_until"])
	assert.Equal(t, "2030-06-17", data["date"])
	assert.Equal(t, "Aspirin", data["item"].(map[string]any)["name"])

	// Correcting the date makes a new alert (a new key) and so a new delivery.
	items, err := tRepos.Entities.QueryByGroup(e.ctx, e.gid, repo.EntityQuery{Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Len(t, items.Items, 1)
	e.setExpiry(repo.EntityOut{EntitySummary: items.Items[0]}, day(2030, 6, 18))
	*e.clk = e.at(2030, 6, 11, 9, 0)
	require.NoError(t, e.alertEnv.svc.AlertTick(e.ctx, *e.clk))
	assert.Len(t, e.deliveries(ep), 2, "a moved date is a new alert")
}

func TestWebhooks_TestEventRedeliverAndDisabled(t *testing.T) {
	e := newWhEnv(t, "UTC")
	rcv := newReceiver(t)
	ep := e.endpoint(rcv.srv.URL, webhooks.ItemCreated)

	res, err := e.svc.SendTest(e.ctx, e.gid, ep.ID)
	require.NoError(t, err)
	assert.True(t, res.OK)
	require.Len(t, rcv.received(), 1)
	assert.Equal(t, webhooks.Test, rcv.received()[0].header.Get("X-Homebox-Event"))

	// A failing test is reported and not retried.
	rcv.answer(http.StatusInternalServerError)
	res, err = e.svc.SendTest(e.ctx, e.gid, ep.ID)
	require.NoError(t, err)
	assert.False(t, res.OK)
	assert.Equal(t, 500, res.StatusCode)
	e.svc.ProcessDue(e.ctx)
	assert.Len(t, rcv.received(), 2, "no retry of a test")

	// Redeliver sends a fresh copy that keeps the event id.
	rcv.answer(http.StatusOK)
	first := e.deliveries(ep)[len(e.deliveries(ep))-1]
	newID, err := e.svc.Redeliver(e.ctx, e.gid, ep.ID, first.ID)
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, newID)
	e.svc.ProcessDue(e.ctx)
	got := rcv.received()
	assert.Equal(t, newID.String(), got[len(got)-1].json["id"])
	assert.Equal(t, first.EventID.String(), got[len(got)-1].json["event_id"])

	// Another collection cannot touch this endpoint.
	other, err := tRepos.Groups.GroupCreate(e.ctx, "o-"+fk.Str(5), uuid.Nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tRepos.Groups.GroupDelete(e.ctx, other.ID) })
	_, err = e.svc.SendTest(e.ctx, other.ID, ep.ID)
	require.Error(t, err)

	// Disabled instance: nothing is queued.
	off := NewWebhookService(tRepos, config.WebhooksConf{Enabled: false}, &config.NotifierConf{})
	off.Emit(e.ctx, repo.DomainEvent{GID: e.gid, Type: webhooks.ItemCreated})
	_, err = off.SendTest(e.ctx, e.gid, ep.ID)
	require.Error(t, err)
}

func TestWebhooks_RateCap(t *testing.T) {
	e := newWhEnv(t, "UTC")
	e.svc.cfg.RatePerMinute = 2
	rcv := newReceiver(t)
	e.endpoint(rcv.srv.URL, webhooks.ItemCreated)
	for i := 0; i < 5; i++ {
		_, err := tRepos.Entities.Create(e.ctx, e.gid, repo.EntityCreate{Name: fk.Str(6), Quantity: 1, EntityTypeID: e.typeID})
		require.NoError(t, err)
	}
	e.svc.ProcessDue(e.ctx)
	assert.Len(t, rcv.received(), 2, "a burst is held back to the cap")

	e.advance(2 * time.Minute)
	e.svc.ProcessDue(e.ctx)
	assert.Len(t, rcv.received(), 4)
	e.advance(2 * time.Minute)
	e.svc.ProcessDue(e.ctx)
	assert.Len(t, rcv.received(), 5, "nothing is lost, only spread out")
}

func TestWebhooks_EndpointAPIHidesURLAndSecret(t *testing.T) {
	e := newWhEnv(t, "UTC")
	u := "https://hooks.example.com/api/webhook/very-secret-id?x=1"
	created, err := tRepos.Webhooks.Create(e.ctx, e.gid, repo.WebhookEndpointIn{Name: "ha", URL: &u, Events: []string{webhooks.ItemCreated}, IsActive: true}, "whsec_abc")
	require.NoError(t, err)
	assert.Equal(t, "whsec_abc", created.Secret, "shown once, at creation")
	assert.Equal(t, "https://hooks.example.com/…", created.URLHost)

	list, err := tRepos.Webhooks.GetAll(e.ctx, e.gid)
	require.NoError(t, err)
	raw, err := json.Marshal(list)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "very-secret-id")
	assert.NotContains(t, string(raw), "whsec_abc")

	// Updating without a URL keeps the stored one.
	_, err = tRepos.Webhooks.Update(e.ctx, e.gid, created.ID, repo.WebhookEndpointIn{Name: "renamed", Events: []string{webhooks.ItemDeleted}, IsActive: true})
	require.NoError(t, err)
	full, err := tRepos.Webhooks.GetFull(e.ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, u, full.URL)
	assert.Equal(t, "renamed", full.Name)

	// Rotating replaces the secret.
	rot, err := tRepos.Webhooks.RotateSecret(e.ctx, e.gid, created.ID, "whsec_new")
	require.NoError(t, err)
	assert.Equal(t, "whsec_new", rot.Secret)
	full, _ = tRepos.Webhooks.GetFull(e.ctx, created.ID)
	assert.Equal(t, "whsec_new", full.Secret)

	for name, in := range map[string]repo.WebhookEndpointIn{
		"no events":     {Name: "x", URL: &u},
		"unknown event": {Name: "x", URL: &u, Events: []string{"item.exploded"}},
		"test event":    {Name: "x", URL: &u, Events: []string{webhooks.Test}},
		"no name":       {URL: &u, Events: []string{webhooks.ItemCreated}},
	} {
		_, err := tRepos.Webhooks.Create(e.ctx, e.gid, in, "s")
		assert.Error(t, err, name)
	}
	_, err = tRepos.Webhooks.Create(e.ctx, e.gid, repo.WebhookEndpointIn{Name: "x", Events: []string{webhooks.ItemCreated}}, "s")
	assert.Error(t, err, "url required")
}
