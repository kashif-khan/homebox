package repo

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entitylookup"
	"github.com/sysadminsmedia/homebox/backend/internal/data/types"
)

func TestEntityLookupRepository_Lifecycle(t *testing.T) {
	ctx := context.Background()
	e := useEntities(t, 1)[0]

	_, err := tRepos.EntityLookups.Get(ctx, tGroup.ID, e.ID)
	require.True(t, ent.IsNotFound(err), "an item starts without a lookup")

	out, err := tRepos.EntityLookups.Request(ctx, tGroup.ID, e.ID, "dewalt drill")
	require.NoError(t, err)
	assert.Equal(t, "pending", out.Status)
	assert.Equal(t, "dewalt drill", out.Query)
	assert.Empty(t, out.Candidates)

	job, err := tRepos.EntityLookups.Claim(ctx)
	require.NoError(t, err)
	require.NotNil(t, job)
	assert.Equal(t, e.ID, job.EntityID)
	assert.Equal(t, 1, job.Attempts)

	found := []types.LookupCandidate{{Name: "DeWalt Drill", Confidence: 0.7, Sources: []string{"websearch"}}}
	require.NoError(t, tRepos.EntityLookups.Complete(ctx, job.ID, found))

	out, err = tRepos.EntityLookups.Get(ctx, tGroup.ID, e.ID)
	require.NoError(t, err)
	assert.Equal(t, "done", out.Status)
	assert.Equal(t, found, out.Candidates)

	next, err := tRepos.EntityLookups.Claim(ctx)
	require.NoError(t, err)
	assert.Nil(t, next, "a finished lookup is not claimed again")

	// Asking again starts over rather than adding a second row.
	out, err = tRepos.EntityLookups.Request(ctx, tGroup.ID, e.ID, "another query")
	require.NoError(t, err)
	assert.Equal(t, "pending", out.Status)
	assert.Empty(t, out.Candidates)
	count, err := tClient.EntityLookup.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	job, err = tRepos.EntityLookups.Claim(ctx)
	require.NoError(t, err)
	require.NoError(t, tRepos.EntityLookups.Fail(ctx, job.ID, "service down"))
	out, err = tRepos.EntityLookups.Get(ctx, tGroup.ID, e.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", out.Status)
	assert.Equal(t, "service down", out.Error)

	require.NoError(t, tRepos.EntityLookups.Delete(ctx, tGroup.ID, e.ID))
	assert.True(t, ent.IsNotFound(tRepos.EntityLookups.Delete(ctx, tGroup.ID, e.ID)))
}

func TestEntityLookupRepository_IsScopedToTheGroup(t *testing.T) {
	ctx := context.Background()
	e := useEntities(t, 1)[0]
	other := uuid.New()

	_, err := tRepos.EntityLookups.Request(ctx, other, e.ID, "x")
	assert.True(t, ent.IsNotFound(err), "another collection cannot start a lookup on this item")

	_, err = tRepos.EntityLookups.Request(ctx, tGroup.ID, e.ID, "x")
	require.NoError(t, err)
	_, err = tRepos.EntityLookups.Get(ctx, other, e.ID)
	assert.True(t, ent.IsNotFound(err), "nor read it")
	assert.True(t, ent.IsNotFound(tRepos.EntityLookups.Delete(ctx, other, e.ID)), "nor dismiss it")
	require.NoError(t, tRepos.EntityLookups.Delete(ctx, tGroup.ID, e.ID))
}

func TestEntityLookupRepository_DeletingTheItemRemovesTheLookup(t *testing.T) {
	ctx := context.Background()
	e := useEntities(t, 1)[0]
	_, err := tRepos.EntityLookups.Request(ctx, tGroup.ID, e.ID, "x")
	require.NoError(t, err)

	require.NoError(t, tRepos.Entities.DeleteByGroup(ctx, tGroup.ID, e.ID))
	_, err = tRepos.EntityLookups.Get(ctx, tGroup.ID, e.ID)
	assert.True(t, ent.IsNotFound(err))
}

func TestEntityLookupRepository_Settings(t *testing.T) {
	ctx := context.Background()
	g, err := tRepos.Groups.GroupCreate(ctx, "lookup-settings", uuid.Nil)
	require.NoError(t, err)

	s, err := tRepos.EntityLookups.GetSettings(ctx, g.ID)
	require.NoError(t, err)
	assert.Equal(t, LookupSettings{}, s, "off and empty until set")

	token := "secret"
	require.NoError(t, tRepos.EntityLookups.SetSettings(ctx, g.ID, "http://barcode:8787", &token, true))
	s, err = tRepos.EntityLookups.GetSettings(ctx, g.ID)
	require.NoError(t, err)
	assert.Equal(t, LookupSettings{URL: "http://barcode:8787", Token: "secret", Auto: true}, s)

	// Saving without a token keeps the stored one.
	require.NoError(t, tRepos.EntityLookups.SetSettings(ctx, g.ID, "http://other:8787", nil, false))
	s, err = tRepos.EntityLookups.GetSettings(ctx, g.ID)
	require.NoError(t, err)
	assert.Equal(t, LookupSettings{URL: "http://other:8787", Token: "secret", Auto: false}, s)

	empty := ""
	require.NoError(t, tRepos.EntityLookups.SetSettings(ctx, g.ID, "", &empty, false))
	s, _ = tRepos.EntityLookups.GetSettings(ctx, g.ID)
	assert.Equal(t, LookupSettings{}, s, "an empty token clears it")
}

func TestEntityLookupRepository_ListRetryAndClear(t *testing.T) {
	ctx := context.Background()
	g, err := tRepos.Groups.GroupCreate(ctx, "lookup-list", uuid.Nil)
	require.NoError(t, err)
	et, err := tRepos.EntityTypes.GetDefault(ctx, g.ID, false)
	require.NoError(t, err)
	mk := func(name string) uuid.UUID {
		e, err := tRepos.Entities.Create(ctx, g.ID, EntityCreate{Name: name, Quantity: 1, EntityTypeID: et.ID})
		require.NoError(t, err)
		_, err = tRepos.EntityLookups.Request(ctx, g.ID, e.ID, name)
		require.NoError(t, err)
		return e.ID
	}
	a, b, c := mk("alpha"), mk("beta"), mk("gamma")
	set := func(id uuid.UUID, f func(*ent.EntityLookupUpdate)) {
		u := tClient.EntityLookup.Update().Where(entitylookup.EntityID(id))
		f(u)
		require.NoError(t, u.Exec(ctx))
	}
	set(a, func(u *ent.EntityLookupUpdate) {
		u.SetStatus(entitylookup.StatusDone).SetCandidates([]types.LookupCandidate{{Name: "A"}, {Name: "A2"}})
	})
	set(b, func(u *ent.EntityLookupUpdate) { u.SetStatus(entitylookup.StatusFailed).SetError("down") })
	_ = c // stays pending

	items, counts, err := tRepos.EntityLookups.List(ctx, g.ID, "", 50)
	require.NoError(t, err)
	assert.Equal(t, EntityLookupCounts{Pending: 1, Done: 1, Failed: 1}, counts)
	require.Len(t, items, 3)
	byName := map[string]EntityLookupListItem{}
	for _, i := range items {
		byName[i.EntityName] = i
	}
	assert.Equal(t, 2, byName["alpha"].Matches)
	assert.Equal(t, "down", byName["beta"].Error)

	failedOnly, _, err := tRepos.EntityLookups.List(ctx, g.ID, "failed", 50)
	require.NoError(t, err)
	require.Len(t, failedOnly, 1)
	assert.Equal(t, "beta", failedOnly[0].EntityName)

	n, err := tRepos.EntityLookups.RetryFailed(ctx, g.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	_, counts, _ = tRepos.EntityLookups.List(ctx, g.ID, "", 50)
	assert.Equal(t, EntityLookupCounts{Pending: 2, Done: 1}, counts)

	n, err = tRepos.EntityLookups.DeleteByStatus(ctx, g.ID, entitylookup.StatusDone)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	_, counts, _ = tRepos.EntityLookups.List(ctx, g.ID, "", 50)
	assert.Equal(t, EntityLookupCounts{Pending: 2}, counts, "pending ones are untouched")

	// Another collection sees none of it.
	_, other, err := tRepos.EntityLookups.List(ctx, tGroup.ID, "", 50)
	require.NoError(t, err)
	assert.Zero(t, other.Failed+other.Done)
}

func TestEntityLookupRepository_MissingAndQueueMany(t *testing.T) {
	ctx := context.Background()
	g, err := tRepos.Groups.GroupCreate(ctx, "lookup-missing", uuid.Nil)
	require.NoError(t, err)
	item, err := tRepos.EntityTypes.GetDefault(ctx, g.ID, false)
	require.NoError(t, err)
	loc, err := tRepos.EntityTypes.GetDefault(ctx, g.ID, true)
	require.NoError(t, err)
	mk := func(c EntityCreate) EntityOut {
		e, err := tRepos.Entities.Create(ctx, g.ID, c)
		require.NoError(t, err)
		return e
	}
	bare := mk(EntityCreate{Name: "Bare", Quantity: 1, EntityTypeID: item.ID})
	full := mk(EntityCreate{Name: "Full", Description: "d", Manufacturer: "m", ModelNumber: "x", Quantity: 1, EntityTypeID: item.ID})
	mk(EntityCreate{Name: "Garage", Quantity: 1, EntityTypeID: loc.ID})
	queued := mk(EntityCreate{Name: "Queued", Quantity: 1, EntityTypeID: item.ID})
	_, err = tRepos.EntityLookups.Request(ctx, g.ID, queued.ID, "q")
	require.NoError(t, err)

	got, total, err := tRepos.EntityLookups.Missing(ctx, g.ID, []string{"description", "manufacturer", "modelNumber"}, 10)
	require.NoError(t, err)
	require.Equal(t, 1, total, "locations, complete items and ones already queued are left out")
	assert.Equal(t, bare.ID, got[0].ID)

	_, total, err = tRepos.EntityLookups.Missing(ctx, g.ID, []string{"photo"}, 10)
	require.NoError(t, err)
	assert.Equal(t, 2, total, "neither Bare nor Full has a photo")

	_, total, err = tRepos.EntityLookups.Missing(ctx, g.ID, []string{"manufacturer"}, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, total)

	got, total, err = tRepos.EntityLookups.Missing(ctx, g.ID, []string{"photo"}, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	assert.Len(t, got, 1, "the limit caps what is returned, not what is counted")

	none, total, err := tRepos.EntityLookups.Missing(ctx, g.ID, nil, 10)
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.Empty(t, none)

	n, err := tRepos.EntityLookups.QueueMany(ctx, g.ID, map[uuid.UUID]string{bare.ID: "Bare", full.ID: "Full", queued.ID: "again"})
	require.NoError(t, err)
	assert.Equal(t, 2, n, "the one that already had a lookup is skipped")
}
