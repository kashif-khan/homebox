package repo

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/sysadminsmedia/homebox/backend/internal/data/types"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
)

func day(y int, m time.Month, d int) types.Date {
	return types.DateFromTime(time.Date(y, m, d, 0, 0, 0, 0, time.UTC))
}

func setExpiry(t *testing.T, e EntityOut, at types.Date, kind string) (EntityOut, error) {
	t.Helper()
	upd := EntityUpdate{ID: e.ID, Name: e.Name, ExpiresAt: at, ExpiryKind: kind}
	if e.EntityType != nil {
		upd.EntityTypeID = e.EntityType.ID
	}
	return tRepos.Entities.UpdateByGroup(context.Background(), tGroup.ID, upd)
}

func TestExpiry_RoundTripClearAndKind(t *testing.T) {
	e := useEntities(t, 1)[0]
	assert.True(t, e.ExpiresAt.Time().IsZero())
	assert.Equal(t, "expires", e.ExpiryKind)

	out, err := setExpiry(t, e, day(2027, 3, 1), "best_before")
	require.NoError(t, err)
	assert.Equal(t, "2027-03-01", out.ExpiresAt.String())
	assert.Equal(t, "best_before", out.ExpiryKind)

	out, err = setExpiry(t, e, types.Date{}, "")
	require.NoError(t, err)
	assert.True(t, out.ExpiresAt.Time().IsZero(), "an empty date clears it")
	assert.Equal(t, "expires", out.ExpiryKind, "empty kind means expires")

	_, err = setExpiry(t, e, day(2027, 3, 1), "whenever")
	require.Error(t, err)
	var re *validate.RequestError
	require.ErrorAs(t, err, &re)
	assert.Equal(t, http.StatusUnprocessableEntity, re.Status)
}

func TestExpiry_RequiredByEntityType(t *testing.T) {
	ctx := context.Background()
	et, err := tRepos.EntityTypes.Create(ctx, tGroup.ID, EntityTypeCreate{Name: "Medicine " + fk.Str(6), ExpiryRequired: true})
	require.NoError(t, err)
	require.True(t, et.ExpiryRequired)
	t.Cleanup(func() { _ = tRepos.EntityTypes.Delete(ctx, tGroup.ID, et.ID) })

	e := useEntities(t, 1)[0]

	// Moving an item with no date into the type is refused...
	upd := EntityUpdate{ID: e.ID, Name: e.Name, EntityTypeID: et.ID}
	_, err = tRepos.Entities.UpdateByGroup(ctx, tGroup.ID, upd)
	var re *validate.RequestError
	require.ErrorAs(t, err, &re)
	assert.Equal(t, http.StatusUnprocessableEntity, re.Status)

	// ...and nothing about the item changed.
	same, err := tRepos.Entities.GetOneByGroup(ctx, tGroup.ID, e.ID)
	require.NoError(t, err)
	assert.Equal(t, e.EntityType.ID, same.EntityType.ID)

	// With a date it is accepted, and the date can then not be cleared.
	upd.ExpiresAt = day(2027, 1, 1)
	moved, err := tRepos.Entities.UpdateByGroup(ctx, tGroup.ID, upd)
	require.NoError(t, err)
	assert.Equal(t, et.ID, moved.EntityType.ID)

	_, err = setExpiry(t, moved, types.Date{}, "")
	require.ErrorAs(t, err, &re)

	empty := types.Date{}
	err = tRepos.Entities.Patch(ctx, tGroup.ID, moved.ID, EntityPatch{ID: moved.ID, ExpiresAt: &empty})
	require.ErrorAs(t, err, &re, "patching the date away is refused too")

	// Put it back so the cleanup of the entity type succeeds.
	itemET := useItemEntityType(t)
	back := EntityUpdate{ID: moved.ID, Name: moved.Name, EntityTypeID: itemET.ID, ExpiresAt: day(2027, 1, 1)}
	_, err = tRepos.Entities.UpdateByGroup(ctx, tGroup.ID, back)
	require.NoError(t, err)
}

func TestExpiry_PatchSetsAndClears(t *testing.T) {
	ctx := context.Background()
	e := useEntities(t, 1)[0]

	d, kind := day(2028, 6, 30), "use_by"
	require.NoError(t, tRepos.Entities.Patch(ctx, tGroup.ID, e.ID, EntityPatch{ID: e.ID, ExpiresAt: &d, ExpiryKind: &kind}))
	got, err := tRepos.Entities.GetOneByGroup(ctx, tGroup.ID, e.ID)
	require.NoError(t, err)
	assert.Equal(t, "2028-06-30", got.ExpiresAt.String())
	assert.Equal(t, "use_by", got.ExpiryKind)

	// A patch that does not mention expiry leaves it alone.
	q := 3.0
	require.NoError(t, tRepos.Entities.Patch(ctx, tGroup.ID, e.ID, EntityPatch{ID: e.ID, Quantity: &q}))
	got, err = tRepos.Entities.GetOneByGroup(ctx, tGroup.ID, e.ID)
	require.NoError(t, err)
	assert.Equal(t, "2028-06-30", got.ExpiresAt.String())

	empty := types.Date{}
	require.NoError(t, tRepos.Entities.Patch(ctx, tGroup.ID, e.ID, EntityPatch{ID: e.ID, ExpiresAt: &empty}))
	got, err = tRepos.Entities.GetOneByGroup(ctx, tGroup.ID, e.ID)
	require.NoError(t, err)
	assert.True(t, got.ExpiresAt.Time().IsZero())

	bad := "soon"
	require.Error(t, tRepos.Entities.Patch(ctx, tGroup.ID, e.ID, EntityPatch{ID: e.ID, ExpiryKind: &bad}))
}

func TestExpiry_QueryFilterAndOrder(t *testing.T) {
	ctx := context.Background()
	es := useEntities(t, 4)
	dates := []types.Date{day(2031, 5, 1), day(2031, 1, 1), {}, day(2031, 9, 1)}
	for i, e := range es {
		_, err := setExpiry(t, e, dates[i], "")
		require.NoError(t, err)
	}
	ids := map[string]bool{}
	for _, e := range es {
		ids[e.ID.String()] = true
	}
	mine := func(res PaginationResult[EntitySummary]) []EntitySummary {
		var out []EntitySummary
		for _, it := range res.Items {
			if ids[it.ID.String()] {
				out = append(out, it)
			}
		}
		return out
	}

	asc, err := tRepos.Entities.QueryByGroup(ctx, tGroup.ID, EntityQuery{Page: 1, PageSize: 1000, OrderBy: "expiresAt"})
	require.NoError(t, err)
	got := mine(asc)
	require.Len(t, got, 4)
	assert.Equal(t, []string{"2031-01-01", "2031-05-01", "2031-09-01"}, []string{got[0].ExpiresAt.String(), got[1].ExpiresAt.String(), got[2].ExpiresAt.String()})
	assert.True(t, got[3].ExpiresAt.Time().IsZero(), "no date sorts last ascending")

	desc, err := tRepos.Entities.QueryByGroup(ctx, tGroup.ID, EntityQuery{Page: 1, PageSize: 1000, OrderBy: "expiresAt", OrderDirection: "desc"})
	require.NoError(t, err)
	got = mine(desc)
	require.Len(t, got, 4)
	assert.Equal(t, "2031-09-01", got[0].ExpiresAt.String())
	assert.True(t, got[3].ExpiresAt.Time().IsZero(), "no date sorts last descending too")

	before := time.Date(2031, 5, 1, 0, 0, 0, 0, time.UTC)
	res, err := tRepos.Entities.QueryByGroup(ctx, tGroup.ID, EntityQuery{Page: 1, PageSize: 1000, ExpiresBefore: &before})
	require.NoError(t, err)
	got = mine(res)
	require.Len(t, got, 2)
	for _, it := range got {
		assert.False(t, it.ExpiresAt.Time().After(before))
	}

	after := time.Date(2031, 5, 2, 0, 0, 0, 0, time.UTC)
	res, err = tRepos.Entities.QueryByGroup(ctx, tGroup.ID, EntityQuery{Page: 1, PageSize: 1000, ExpiresAfter: &after})
	require.NoError(t, err)
	got = mine(res)
	require.Len(t, got, 1)
	assert.Equal(t, "2031-09-01", got[0].ExpiresAt.String())
}

func TestExpiry_ExpiringBuckets(t *testing.T) {
	ctx := context.Background()
	// A group of our own so other tests' dated items do not interfere.
	g, err := tRepos.Groups.GroupCreate(ctx, "expiry-"+fk.Str(6), uuid.Nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tRepos.Groups.GroupDelete(ctx, g.ID) })

	itemET, err := tRepos.EntityTypes.Create(ctx, g.ID, EntityTypeCreate{Name: "Item"})
	require.NoError(t, err)
	mk := func(name string, at types.Date, archived bool) {
		e, err := tRepos.Entities.Create(ctx, g.ID, EntityCreate{Name: name, Quantity: 1, EntityTypeID: itemET.ID})
		require.NoError(t, err)
		upd := EntityUpdate{ID: e.ID, Name: name, EntityTypeID: itemET.ID, ExpiresAt: at, Archived: archived, Quantity: 1}
		_, err = tRepos.Entities.UpdateByGroup(ctx, g.ID, upd)
		require.NoError(t, err)
	}
	mk("yesterday", day(2030, 6, 14), false)
	mk("long ago", day(2030, 1, 1), false)
	mk("today", day(2030, 6, 15), false)
	mk("in ten days", day(2030, 6, 25), false)
	mk("in a year", day(2031, 6, 15), false)
	mk("archived and expired", day(2030, 6, 1), true)
	mk("no date", types.Date{}, false)

	// 23:30 on the 15th in a far-east zone is still the 15th there, even though
	// it is already the 16th in UTC+14 terms of a naive conversion.
	now := time.Date(2030, 6, 15, 23, 30, 0, 0, time.FixedZone("x", 5*3600))
	res, err := tRepos.Entities.Expiring(ctx, g.ID, now, 30, 10)
	require.NoError(t, err)

	names := func(es []ExpiryEntry) []string {
		var out []string
		for _, e := range es {
			out = append(out, e.Name)
		}
		return out
	}
	assert.Equal(t, []string{"yesterday", "long ago"}, names(res.Expired), "most recently expired first, archived excluded")
	assert.Equal(t, 2, res.ExpiredTotal)
	assert.Equal(t, []string{"today", "in ten days"}, names(res.Soon), "an item expiring today is not yet expired")
	assert.Equal(t, 0, res.Soon[0].DaysLeft)
	assert.Equal(t, 10, res.Soon[1].DaysLeft)
	assert.Equal(t, -1, res.Expired[0].DaysLeft)

	capped, err := tRepos.Entities.Expiring(ctx, g.ID, now, 30, 1)
	require.NoError(t, err)
	assert.Len(t, capped.Expired, 1)
	assert.Equal(t, 2, capped.ExpiredTotal, "totals ignore the cap")
}

func TestExpiry_TemplateOffsetAndCreateFromTemplate(t *testing.T) {
	ctx := context.Background()
	data := templateFactory()
	data.IncludeExpiryFields = true
	days := 730
	data.DefaultExpiryOffsetDays = &days
	data.DefaultExpiryKind = "best_before"
	tpl, err := tRepos.EntityTemplates.Create(ctx, tGroup.ID, data)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tRepos.EntityTemplates.Delete(ctx, tGroup.ID, tpl.ID) })
	assert.True(t, tpl.IncludeExpiryFields)
	require.NotNil(t, tpl.DefaultExpiryOffsetDays)
	assert.Equal(t, 730, *tpl.DefaultExpiryOffsetDays)
	assert.Equal(t, "best_before", tpl.DefaultExpiryKind)

	// "Today" is taken in the collection's zone: 23:30 UTC on the 15th is already
	// the 16th in Karachi.
	now := time.Date(2030, 6, 15, 23, 30, 0, 0, time.UTC)
	at, kind := TemplateExpiry(tpl, now, time.FixedZone("PKT", 5*3600))
	assert.Equal(t, "2032-06-15", at.String())
	assert.Equal(t, "best_before", kind)
	at, _ = TemplateExpiry(tpl, now, time.UTC)
	assert.Equal(t, "2032-06-14", at.String())

	item, err := tRepos.Entities.CreateFromTemplate(ctx, tGroup.ID, EntityCreateFromTemplate{
		Name: fk.Str(8), Quantity: 1, ExpiresAt: at, ExpiryKind: kind,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tRepos.Entities.Delete(ctx, item.ID) })
	assert.Equal(t, "2032-06-14", item.ExpiresAt.String())
	assert.Equal(t, "best_before", item.ExpiryKind)

	// A template that does not include expiry sets none.
	plain := tpl
	plain.IncludeExpiryFields = false
	none, _ := TemplateExpiry(plain, now, time.UTC)
	assert.True(t, none.Time().IsZero())

	// Bad defaults are rejected.
	bad := templateFactory()
	neg := -1
	bad.DefaultExpiryOffsetDays = &neg
	_, err = tRepos.EntityTemplates.Create(ctx, tGroup.ID, bad)
	require.Error(t, err)
	bad = templateFactory()
	bad.DefaultExpiryKind = "someday"
	_, err = tRepos.EntityTemplates.Create(ctx, tGroup.ID, bad)
	require.Error(t, err)
}

func TestExpiry_DuplicateCopiesDate(t *testing.T) {
	ctx := context.Background()
	e := useEntities(t, 1)[0]
	_, err := setExpiry(t, e, day(2029, 2, 3), "use_by")
	require.NoError(t, err)

	dup, err := tRepos.Entities.Duplicate(ctx, tGroup.ID, e.ID, DuplicateOptions{CopyPrefix: "Copy of "})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tRepos.Entities.Delete(ctx, dup.ID) })
	assert.Equal(t, "2029-02-03", dup.ExpiresAt.String())
	assert.Equal(t, "use_by", dup.ExpiryKind)
}
