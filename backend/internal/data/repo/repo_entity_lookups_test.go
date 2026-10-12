package repo

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
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
