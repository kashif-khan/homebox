package services

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entitylookup"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/group"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
)

func TestLookupQuery(t *testing.T) {
	cases := []struct {
		name, maker, model, want string
	}{
		{"Cordless Drill", "DeWalt", "DCD777", "DeWalt DCD777"},
		{"Cordless Drill", "DeWalt", "", "DeWalt Cordless Drill"},
		{"Cordless Drill", "", "", "Cordless Drill"},
		{"  Spaced  ", " ", " ", "Spaced"},
	}
	for _, c := range cases {
		got := LookupQuery(repo.EntityOut{EntitySummary: repo.EntitySummary{Name: c.name}, Manufacturer: c.maker, ModelNumber: c.model})
		assert.Equal(t, c.want, got)
	}
}

// lookupFixture runs a fake lookup service and a fake picture host, and gives a
// LookupService wired to both together with a fresh collection holding one item.
type lookupFixture struct {
	svc      *LookupService
	ctx      Context
	item     repo.EntityOut
	answer   *string // JSON body the fake service answers with
	status   *int
	received *map[string]any
}

func newLookupFixture(t *testing.T, auto bool) lookupFixture {
	t.Helper()
	bg := context.Background()

	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	pictures := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(buf.Bytes())
	}))
	t.Cleanup(pictures.Close)

	status := http.StatusOK
	received := map[string]any{}
	answer := `{"results":[
		{"name":"DeWalt DCD777 Drill","description":"18V brushless","manufacturer":"DeWalt","modelNumber":"DCD777","imageURLs":["` + pictures.URL + `/p.png","` + pictures.URL + `/q.png"],"confidence":0.8,"sources":["upcitemdb"]},
		{"name":"Other thing","imageURLs":["http://insecure.example/x.png"],"confidence":0.3,"sources":["websearch"]}]}`
	addon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(addon.Close)

	grp, err := tRepos.Groups.GroupCreate(bg, "lookup-"+fk.Str(6), uuid.Nil)
	require.NoError(t, err)
	et, err := tRepos.EntityTypes.GetDefault(bg, grp.ID, false)
	require.NoError(t, err)
	item, err := tRepos.Entities.Create(bg, grp.ID, repo.EntityCreate{Name: "Cordless Drill", Manufacturer: "DeWalt", Quantity: 1, EntityTypeID: et.ID})
	require.NoError(t, err)

	// The worker takes the oldest queued lookup of any collection, so one that a
	// test leaves behind would be run by the next test. Clear them away.
	t.Cleanup(func() {
		_, _ = tClient.EntityLookup.Delete().Where(entitylookup.HasGroupWith(group.ID(grp.ID))).Exec(context.Background())
	})

	svc := NewLookupService(tRepos, tSvc.Entities,
		config.BarcodeAPIConf{EnrichURL: addon.URL, EnrichToken: "tok", EnrichAuto: auto}, nil, pictures.Client())
	return lookupFixture{
		svc: svc, ctx: Context{Context: bg, GID: grp.ID, UID: tUser.ID}, item: item,
		answer: &answer, status: &status, received: &received,
	}
}

func TestLookupService_DisabledWithoutAService(t *testing.T) {
	s := NewLookupService(tRepos, tSvc.Entities, config.BarcodeAPIConf{}, nil, http.DefaultClient)
	assert.False(t, s.Configured(context.Background(), uuid.New()))
	_, err := s.Request(Context{Context: context.Background()}, uuid.New())
	assert.ErrorIs(t, err, ErrLookupDisabled)
}

func TestLookupService_RequestRunReview(t *testing.T) {
	f := newLookupFixture(t, false)

	out, err := f.svc.Request(f.ctx, f.item.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", out.Status)
	assert.Equal(t, "DeWalt Cordless Drill", out.Query)

	worked, err := f.svc.runOne(context.Background())
	require.NoError(t, err)
	require.True(t, worked)
	assert.Equal(t, "DeWalt Cordless Drill", (*f.received)["query"])

	out, err = tRepos.EntityLookups.Get(context.Background(), f.ctx.GID, f.item.ID)
	require.NoError(t, err)
	assert.Equal(t, "done", out.Status)
	require.Len(t, out.Candidates, 2)
	assert.Equal(t, "DeWalt DCD777 Drill", out.Candidates[0].Name)
	assert.Len(t, out.Candidates[0].ImageURLs, 2)
	assert.Equal(t, out.Candidates[0].ImageURLs[0], out.Candidates[0].ImageURL, "the first picture is also the main one")
	assert.Empty(t, out.Candidates[1].ImageURLs, "a picture that is not https is dropped")
	assert.NotNil(t, out.Candidates[1].ImageURLs, "and the list is empty, not null")

	worked, err = f.svc.runOne(context.Background())
	require.NoError(t, err)
	assert.False(t, worked, "nothing else is waiting")
}

func TestLookupService_ServiceFailureIsReportedWithoutDetails(t *testing.T) {
	f := newLookupFixture(t, false)
	*f.status = http.StatusInternalServerError
	*f.answer = `{"secret":"stack trace"}`

	_, err := f.svc.Request(f.ctx, f.item.ID)
	require.NoError(t, err)
	_, err = f.svc.runOne(context.Background())
	require.NoError(t, err)

	out, err := tRepos.EntityLookups.Get(context.Background(), f.ctx.GID, f.item.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", out.Status)
	assert.NotContains(t, out.Error, "stack trace")
	assert.NotEmpty(t, out.Error)
}

func TestLookupService_NoSourceAbleToSearchIsNotReportedAsNothingFound(t *testing.T) {
	f := newLookupFixture(t, false)
	*f.answer = `{"results":[],"errors":{},"sources_queried":[]}`

	_, err := f.svc.Request(f.ctx, f.item.ID)
	require.NoError(t, err)
	_, err = f.svc.runOne(context.Background())
	require.NoError(t, err)

	out, err := tRepos.EntityLookups.Get(context.Background(), f.ctx.GID, f.item.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", out.Status)
	assert.Contains(t, out.Error, "web search")

	// A source that did search and found nothing is a real "nothing found".
	*f.answer = `{"results":[],"errors":{},"sources_queried":["websearch"]}`
	_, err = f.svc.Request(f.ctx, f.item.ID)
	require.NoError(t, err)
	_, err = f.svc.runOne(context.Background())
	require.NoError(t, err)
	out, err = tRepos.EntityLookups.Get(context.Background(), f.ctx.GID, f.item.ID)
	require.NoError(t, err)
	assert.Equal(t, "done", out.Status)
	assert.Empty(t, out.Candidates)
}

func TestLookupService_ApplyChangesOnlyWhatWasChosen(t *testing.T) {
	f := newLookupFixture(t, false)
	_, err := f.svc.Request(f.ctx, f.item.ID)
	require.NoError(t, err)
	_, err = f.svc.runOne(context.Background())
	require.NoError(t, err)

	got, err := f.svc.Apply(f.ctx, f.item.ID, LookupApply{Candidate: 0, Fields: []string{"manufacturer", "modelNumber"}})
	require.NoError(t, err)
	assert.Equal(t, "DCD777", got.ModelNumber)
	assert.Equal(t, "DeWalt", got.Manufacturer)
	assert.Equal(t, "Cordless Drill", got.Name, "the name was not chosen, so it stays")
	assert.Empty(t, got.Description)
	assert.Empty(t, got.Attachments)

	_, err = tRepos.EntityLookups.Get(context.Background(), f.ctx.GID, f.item.ID)
	assert.True(t, ent.IsNotFound(err), "applying closes the review")
}

func TestLookupService_ApplyCanAddThePicture(t *testing.T) {
	f := newLookupFixture(t, false)
	_, err := f.svc.Request(f.ctx, f.item.ID)
	require.NoError(t, err)
	_, err = f.svc.runOne(context.Background())
	require.NoError(t, err)

	got, err := f.svc.Apply(f.ctx, f.item.ID, LookupApply{Candidate: 0, Fields: []string{"name", "description", "image"}})
	require.NoError(t, err)
	assert.Equal(t, "DeWalt DCD777 Drill", got.Name)
	assert.Equal(t, "18V brushless", got.Description)
	require.Len(t, got.Attachments, 1)
	assert.Equal(t, "photo", got.Attachments[0].Type)
	assert.True(t, got.Attachments[0].Primary, "the first photo becomes the item's picture")
}

func TestLookupService_ApplyRejectsBadSelections(t *testing.T) {
	f := newLookupFixture(t, false)

	_, err := f.svc.Apply(f.ctx, f.item.ID, LookupApply{Fields: []string{"name"}})
	assert.True(t, ent.IsNotFound(err), "no lookup yet")

	_, err = f.svc.Request(f.ctx, f.item.ID)
	require.NoError(t, err)
	_, err = f.svc.Apply(f.ctx, f.item.ID, LookupApply{Fields: []string{"name"}})
	assert.ErrorIs(t, err, ErrLookupNotReady, "still pending")

	_, err = f.svc.runOne(context.Background())
	require.NoError(t, err)

	for name, in := range map[string]LookupApply{
		"nothing chosen": {Candidate: 0},
		"unknown field":  {Candidate: 0, Fields: []string{"price"}},
		"bad index":      {Candidate: 9, Fields: []string{"name"}},
		"negative index": {Candidate: -1, Fields: []string{"name"}},
		"no picture":     {Candidate: 1, Fields: []string{"image"}},
	} {
		_, err := f.svc.Apply(f.ctx, f.item.ID, in)
		assert.ErrorIs(t, err, ErrLookupInvalid, name)
	}

	// Nothing above touched the item or the review.
	e, err := tRepos.Entities.GetOneByGroup(context.Background(), f.ctx.GID, f.item.ID)
	require.NoError(t, err)
	assert.Equal(t, "Cordless Drill", e.Name)
	_, err = tRepos.EntityLookups.Get(context.Background(), f.ctx.GID, f.item.ID)
	assert.NoError(t, err)
}

func TestLookupService_ApplyIsScopedToTheCollection(t *testing.T) {
	f := newLookupFixture(t, false)
	_, err := f.svc.Request(f.ctx, f.item.ID)
	require.NoError(t, err)
	_, err = f.svc.runOne(context.Background())
	require.NoError(t, err)

	other := f.ctx
	other.GID = tGroup.ID // a real collection, which has no such item
	_, err = f.svc.Apply(other, f.item.ID, LookupApply{Fields: []string{"name"}})
	assert.True(t, ent.IsNotFound(err))
	_, err = f.svc.Request(other, f.item.ID)
	assert.True(t, ent.IsNotFound(err))
}

func TestLookupService_AutoOnlyWhenAsked(t *testing.T) {
	for _, auto := range []bool{false, true} {
		f := newLookupFixture(t, auto)
		f.svc.RequestForNew(f.ctx, f.item)
		_, err := tRepos.EntityLookups.Get(context.Background(), f.ctx.GID, f.item.ID)
		if auto {
			assert.NoError(t, err, "automatic: queued")
		} else {
			assert.True(t, ent.IsNotFound(err), "manual: nothing queued")
		}
	}
}

func TestLookupService_LocationsAreRefused(t *testing.T) {
	f := newLookupFixture(t, false)
	bg := context.Background()
	et, err := tRepos.EntityTypes.GetDefault(bg, f.ctx.GID, true)
	require.NoError(t, err)
	loc, err := tRepos.Entities.Create(bg, f.ctx.GID, repo.EntityCreate{Name: "Garage", Quantity: 1, EntityTypeID: et.ID})
	require.NoError(t, err)

	_, err = f.svc.Request(f.ctx, loc.ID)
	assert.ErrorIs(t, err, ErrLookupNotForLocations)
}

// ---- per-collection settings, connection test, bulk, pictures ----------------

func TestLookupService_CollectionSettingsOverrideTheServers(t *testing.T) {
	f := newLookupFixture(t, false)
	ctx := context.Background()

	out, err := f.svc.Settings(f.ctx)
	require.NoError(t, err)
	assert.True(t, out.Configured)
	assert.True(t, out.FromServer, "until the collection sets its own, the server's applies")
	assert.True(t, out.TokenSet)

	// The collection's own address wins, and the token is never given back.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"name":"From the collection's service"}],"sources_queried":["x"]}`))
	}))
	t.Cleanup(other.Close)
	tok := "mine"
	require.NoError(t, tRepos.EntityLookups.SetSettings(ctx, f.ctx.GID, other.URL, &tok, true))
	out, err = f.svc.Settings(f.ctx)
	require.NoError(t, err)
	assert.Equal(t, other.URL, out.URL)
	assert.False(t, out.FromServer)
	assert.True(t, out.Auto)

	tg, err := f.svc.target(ctx, f.ctx.GID)
	require.NoError(t, err)
	assert.Equal(t, "mine", tg.client.Token)
}

func TestLookupService_SaveSettingsChecksTheAddressAndClearsOnEmpty(t *testing.T) {
	f := newLookupFixture(t, false)

	for _, bad := range []string{"ftp://x", "not a url", "http://user:pw@host", "http://127.0.0.1:8787", "http://169.254.169.254/"} {
		_, err := f.svc.SaveSettings(f.ctx, LookupSettingsIn{URL: bad})
		assert.ErrorIs(t, err, ErrLookupInvalid, bad)
	}

	set, err := tRepos.EntityLookups.GetSettings(context.Background(), f.ctx.GID)
	require.NoError(t, err)
	assert.Empty(t, set.URL, "a refused address is not stored")

	// Empty switches the collection back to the server's setting and forgets the token.
	tok := "t"
	require.NoError(t, tRepos.EntityLookups.SetSettings(context.Background(), f.ctx.GID, "http://example.com", &tok, true))
	out, err := f.svc.SaveSettings(f.ctx, LookupSettingsIn{URL: "", Auto: true})
	require.NoError(t, err)
	set, _ = tRepos.EntityLookups.GetSettings(context.Background(), f.ctx.GID)
	assert.Equal(t, repo.LookupSettings{}, set)
	assert.True(t, out.FromServer)
}

func TestLookupService_TestConnection(t *testing.T) {
	var gotAuth string
	status := http.StatusOK
	body := `{"sources":[{"name":"upcitemdb","ok":true,"detail":""},{"name":"websearch","ok":true,"detail":"SearXNG"}],"web_search":true}`
	addon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(addon.Close)
	f := newLookupFixture(t, false)
	// The server's own address is trusted, so test through that one.
	f.svc.env.EnrichURL, f.svc.env.EnrichToken = addon.URL, "tok"

	res := f.svc.TestConnection(f.ctx, LookupSettingsIn{})
	assert.True(t, res.OK, res.Message)
	assert.Equal(t, "Bearer tok", gotAuth)
	assert.Equal(t, "Connected.", res.Message)
	require.Len(t, res.Sources, 2)
	assert.True(t, res.WebSearch)

	body = `{"sources":[{"name":"upcitemdb","ok":true,"detail":""}],"web_search":false}`
	res = f.svc.TestConnection(f.ctx, LookupSettingsIn{})
	assert.True(t, res.OK)
	assert.Contains(t, res.Message, "web search is off")

	status = http.StatusUnauthorized
	res = f.svc.TestConnection(f.ctx, LookupSettingsIn{})
	assert.False(t, res.OK)
	assert.Contains(t, res.Message, "rejected the token")

	// An address typed into the form goes through the network policy first.
	res = f.svc.TestConnection(f.ctx, LookupSettingsIn{URL: "http://127.0.0.1:1"})
	assert.False(t, res.OK)
	assert.NotContains(t, res.Message, "Connected")

	// Nothing to test at all.
	empty := NewLookupService(tRepos, tSvc.Entities, config.BarcodeAPIConf{}, nil, http.DefaultClient)
	res = empty.TestConnection(f.ctx, LookupSettingsIn{})
	assert.False(t, res.OK)
	assert.Contains(t, res.Message, "Enter the address")
}

func TestLookupService_ConnectionFailuresAreExplainedWithoutDetails(t *testing.T) {
	f := newLookupFixture(t, false)
	f.svc.env.EnrichURL = "http://127.0.0.1:1" // nothing listens here
	res := f.svc.TestConnection(f.ctx, LookupSettingsIn{})
	assert.False(t, res.OK)
	assert.Equal(t, "Could not reach the lookup service. Check the address, and that it is running.", res.Message)
}

func TestLookupService_BulkQueueTargetsItemsMissingDetails(t *testing.T) {
	f := newLookupFixture(t, false) // holds "Cordless Drill" by DeWalt: no description, model or photo
	bg := context.Background()
	et, err := tRepos.EntityTypes.GetDefault(bg, f.ctx.GID, false)
	require.NoError(t, err)
	_, err = tRepos.Entities.Create(bg, f.ctx.GID, repo.EntityCreate{
		Name: "Complete", Description: "d", Manufacturer: "m", ModelNumber: "x", Quantity: 1, EntityTypeID: et.ID})
	require.NoError(t, err)

	dry, err := f.svc.QueueMissing(f.ctx, []string{"description"}, true)
	require.NoError(t, err)
	assert.Equal(t, LookupQueueOut{Matched: 1}, dry, "a dry run counts and queues nothing")
	_, err = tRepos.EntityLookups.Get(bg, f.ctx.GID, f.item.ID)
	assert.True(t, ent.IsNotFound(err))

	out, err := f.svc.QueueMissing(f.ctx, []string{"description", "modelNumber"}, false)
	require.NoError(t, err)
	assert.Equal(t, LookupQueueOut{Matched: 1, Queued: 1}, out)
	lk, err := tRepos.EntityLookups.Get(bg, f.ctx.GID, f.item.ID)
	require.NoError(t, err)
	assert.Equal(t, "DeWalt Cordless Drill", lk.Query)

	again, err := f.svc.QueueMissing(f.ctx, []string{"description"}, false)
	require.NoError(t, err)
	assert.Equal(t, LookupQueueOut{}, again, "items that already have a lookup are not queued twice")

	for name, in := range map[string][]string{"nothing": nil, "unknown": {"price"}} {
		_, err := f.svc.QueueMissing(f.ctx, in, true)
		assert.ErrorIs(t, err, ErrLookupInvalid, name)
	}

	unset := NewLookupService(tRepos, tSvc.Entities, config.BarcodeAPIConf{}, nil, http.DefaultClient)
	_, err = unset.QueueMissing(f.ctx, []string{"description"}, false)
	assert.ErrorIs(t, err, ErrLookupDisabled)
}

func TestLookupService_RetryAndClear(t *testing.T) {
	f := newLookupFixture(t, false)
	bg := context.Background()
	*f.status = http.StatusInternalServerError
	_, err := f.svc.Request(f.ctx, f.item.ID)
	require.NoError(t, err)
	_, err = f.svc.runOne(bg)
	require.NoError(t, err)

	act, err := f.svc.Activity(f.ctx, "")
	require.NoError(t, err)
	assert.Equal(t, 1, act.Counts.Failed)
	require.Len(t, act.Items, 1)
	assert.Equal(t, "Cordless Drill", act.Items[0].EntityName)

	n, err := f.svc.RetryFailed(f.ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	act, _ = f.svc.Activity(f.ctx, "pending")
	assert.Equal(t, 1, act.Counts.Pending)

	n, err = f.svc.Clear(f.ctx, "finished")
	require.NoError(t, err)
	assert.Zero(t, n, "a lookup that is still searching is not cleared")

	_, err = f.svc.runOne(bg)
	require.NoError(t, err)
	n, err = f.svc.Clear(f.ctx, "failed")
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	_, err = f.svc.Clear(f.ctx, "everything")
	assert.ErrorIs(t, err, ErrLookupInvalid)
	_, err = f.svc.Activity(f.ctx, "bogus")
	assert.ErrorIs(t, err, ErrLookupInvalid)
}

func TestLookupService_ApplyTakesTheChosenPicture(t *testing.T) {
	f := newLookupFixture(t, false)
	_, err := f.svc.Request(f.ctx, f.item.ID)
	require.NoError(t, err)
	_, err = f.svc.runOne(context.Background())
	require.NoError(t, err)

	_, err = f.svc.Apply(f.ctx, f.item.ID, LookupApply{Candidate: 0, Fields: []string{"image"}, ImageIndex: 5})
	assert.ErrorIs(t, err, ErrLookupInvalid, "there is no sixth picture")

	got, err := f.svc.Apply(f.ctx, f.item.ID, LookupApply{Candidate: 0, Fields: []string{"image"}, ImageIndex: 1})
	require.NoError(t, err)
	require.Len(t, got.Attachments, 1)
	assert.Equal(t, "Cordless Drill", got.Name, "only the picture was chosen")
}

func TestLookupService_CandidateImageIsServedOnlyFromStoredAddresses(t *testing.T) {
	f := newLookupFixture(t, false)
	_, err := f.svc.Request(f.ctx, f.item.ID)
	require.NoError(t, err)
	_, err = f.svc.runOne(context.Background())
	require.NoError(t, err)

	data, mime, err := f.svc.CandidateImage(f.ctx, f.item.ID, 0, 1)
	require.NoError(t, err)
	assert.Equal(t, "image/png", mime)
	assert.NotEmpty(t, data)

	for name, args := range map[string][2]int{"match": {7, 0}, "picture": {0, 9}, "negative": {-1, 0}} {
		_, _, err := f.svc.CandidateImage(f.ctx, f.item.ID, args[0], args[1])
		assert.ErrorIs(t, err, ErrLookupInvalid, name)
	}

	other := f.ctx
	other.GID = tGroup.ID
	_, _, err = f.svc.CandidateImage(other, f.item.ID, 0, 0)
	assert.True(t, ent.IsNotFound(err), "another collection cannot read it")
}

func TestLookupService_FetchImageFormatsAndFailures(t *testing.T) {
	var gotUA, gotAccept string
	avif := append([]byte{0, 0, 0, 0x1c}, []byte("ftypavif")...)
	avif = append(avif, make([]byte, 32)...)
	var body []byte
	var status = http.StatusOK
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotAccept = r.Header.Get("User-Agent"), r.Header.Get("Accept")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	svc := NewLookupService(tRepos, tSvc.Entities, config.BarcodeAPIConf{}, nil, srv.Client())

	body = avif
	data, mime, ext, err := svc.fetchImage(context.Background(), srv.URL)
	require.NoError(t, err)
	assert.Equal(t, "image/avif", mime)
	assert.Equal(t, ".avif", ext)
	assert.Equal(t, avif, data)
	assert.Contains(t, gotUA, "Homebox", "sites often refuse Go's default agent")
	assert.NotContains(t, gotAccept, "avif", "ask for formats everything can show")

	for name, tc := range map[string]struct {
		body   []byte
		status int
	}{
		"html instead of a picture": {[]byte("<html>Access denied</html>"), http.StatusOK},
		"refused":                   {[]byte("no"), http.StatusForbidden},
		"too large":                 {make([]byte, lookupMaxImageBytes+10), http.StatusOK},
	} {
		body, status = tc.body, tc.status
		_, _, _, err := svc.fetchImage(context.Background(), srv.URL)
		assert.ErrorIs(t, err, ErrLookupPicture, name)
	}

	// A connection that cannot be made is the same kind of failure.
	_, _, _, err = svc.fetchImage(context.Background(), "https://127.0.0.1:1/x.png")
	assert.ErrorIs(t, err, ErrLookupPicture)
}
