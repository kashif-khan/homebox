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
		{"name":"DeWalt DCD777 Drill","description":"18V brushless","manufacturer":"DeWalt","modelNumber":"DCD777","imageURL":"` + pictures.URL + `/p.png","confidence":0.8,"sources":["upcitemdb"]},
		{"name":"Other thing","imageURL":"http://insecure.example/x.png","confidence":0.3,"sources":["websearch"]}]}`
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

	svc := NewLookupService(tRepos, tSvc.Entities,
		config.BarcodeAPIConf{EnrichURL: addon.URL, EnrichToken: "tok", EnrichAuto: auto}, pictures.Client())
	return lookupFixture{
		svc: svc, ctx: Context{Context: bg, GID: grp.ID, UID: tUser.ID}, item: item,
		answer: &answer, status: &status, received: &received,
	}
}

func TestLookupService_DisabledWithoutAService(t *testing.T) {
	s := NewLookupService(tRepos, tSvc.Entities, config.BarcodeAPIConf{}, http.DefaultClient)
	assert.False(t, s.Enabled())
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
	assert.NotEmpty(t, out.Candidates[0].ImageURL)
	assert.Empty(t, out.Candidates[1].ImageURL, "a picture that is not https is dropped")

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
	other.GID = uuid.New()
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
