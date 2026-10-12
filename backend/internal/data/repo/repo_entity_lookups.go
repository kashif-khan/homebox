package repo

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/attachment"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entity"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entitylookup"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entitytype"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/group"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/predicate"
	"github.com/sysadminsmedia/homebox/backend/internal/data/types"
)

// EntityLookupRepository keeps the product lookup of each item: the request, and
// the candidates the worker found for the user to review.
type EntityLookupRepository struct {
	db *ent.Client
}

type (
	EntityLookupOut struct {
		ID         uuid.UUID               `json:"id"`
		EntityID   uuid.UUID               `json:"entityId"`
		Status     string                  `json:"status"` // pending, done or failed
		Query      string                  `json:"query"`
		Error      string                  `json:"error"`
		Candidates []types.LookupCandidate `json:"candidates"`
		CreatedAt  time.Time               `json:"createdAt"`
		UpdatedAt  time.Time               `json:"updatedAt"`
	}

	// EntityLookupJob is a pending lookup, as the worker sees it.
	EntityLookupJob struct {
		ID       uuid.UUID
		GroupID  uuid.UUID
		EntityID uuid.UUID
		Query    string
		Attempts int
	}
)

func mapEntityLookup(l *ent.EntityLookup) EntityLookupOut {
	candidates := l.Candidates
	if candidates == nil {
		candidates = []types.LookupCandidate{}
	}
	return EntityLookupOut{
		ID: l.ID, EntityID: l.EntityID, Status: string(l.Status), Query: l.Query, Error: l.Error,
		Candidates: candidates, CreatedAt: l.CreatedAt, UpdatedAt: l.UpdatedAt,
	}
}

// Request queues a lookup for an item, replacing whatever an earlier one found.
// The item must belong to the group, otherwise ent.NotFoundError is returned.
func (r *EntityLookupRepository) Request(ctx context.Context, gid, entityID uuid.UUID, query string) (EntityLookupOut, error) {
	exists, err := r.db.Entity.Query().
		Where(entity.ID(entityID), entity.HasGroupWith(group.ID(gid))).Exist(ctx)
	if err != nil {
		return EntityLookupOut{}, err
	}
	if !exists {
		return EntityLookupOut{}, &ent.NotFoundError{}
	}

	reset := func() (int, error) {
		return r.db.EntityLookup.Update().
			Where(entitylookup.EntityID(entityID), entitylookup.HasGroupWith(group.ID(gid))).
			SetStatus(entitylookup.StatusPending).SetQuery(query).SetError("").SetAttempts(0).ClearCandidates().Save(ctx)
	}
	n, err := reset()
	if err == nil && n == 0 {
		err = r.db.EntityLookup.Create().SetGroupID(gid).SetEntityID(entityID).SetQuery(query).Exec(ctx)
		if ent.IsConstraintError(err) { // another request created it first
			_, err = reset()
		}
	}
	if err != nil {
		return EntityLookupOut{}, err
	}
	return r.Get(ctx, gid, entityID)
}

// Get returns an item's lookup, or ent.NotFoundError when it has none.
func (r *EntityLookupRepository) Get(ctx context.Context, gid, entityID uuid.UUID) (EntityLookupOut, error) {
	l, err := r.db.EntityLookup.Query().
		Where(entitylookup.EntityID(entityID), entitylookup.HasGroupWith(group.ID(gid))).Only(ctx)
	if err != nil {
		return EntityLookupOut{}, err
	}
	return mapEntityLookup(l), nil
}

// Delete removes an item's lookup, which is how a review is dismissed or finished.
func (r *EntityLookupRepository) Delete(ctx context.Context, gid, entityID uuid.UUID) error {
	n, err := r.db.EntityLookup.Delete().
		Where(entitylookup.EntityID(entityID), entitylookup.HasGroupWith(group.ID(gid))).Exec(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return &ent.NotFoundError{}
	}
	return nil
}

// Claim takes the oldest pending lookup and counts the attempt, so a lookup that
// keeps taking the worker down is given up on rather than retried for ever. It
// returns nil when nothing is waiting.
func (r *EntityLookupRepository) Claim(ctx context.Context) (*EntityLookupJob, error) {
	l, err := r.db.EntityLookup.Query().
		Where(entitylookup.StatusEQ(entitylookup.StatusPending)).
		Order(ent.Asc(entitylookup.FieldCreatedAt)).First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := r.db.EntityLookup.UpdateOneID(l.ID).AddAttempts(1).Exec(ctx); err != nil {
		return nil, err
	}
	return &EntityLookupJob{ID: l.ID, GroupID: l.GroupID, EntityID: l.EntityID, Query: l.Query, Attempts: l.Attempts + 1}, nil
}

// Complete stores what a lookup found. No candidates is a valid answer.
func (r *EntityLookupRepository) Complete(ctx context.Context, id uuid.UUID, candidates []types.LookupCandidate) error {
	if candidates == nil {
		candidates = []types.LookupCandidate{}
	}
	return r.db.EntityLookup.UpdateOneID(id).
		SetStatus(entitylookup.StatusDone).SetCandidates(candidates).SetError("").Exec(ctx)
}

// Fail records why a lookup could not be completed.
func (r *EntityLookupRepository) Fail(ctx context.Context, id uuid.UUID, reason string) error {
	if len(reason) > 500 {
		reason = reason[:500]
	}
	return r.db.EntityLookup.UpdateOneID(id).SetStatus(entitylookup.StatusFailed).SetError(reason).Exec(ctx)
}

// LookupSettings is how a collection reaches the lookup service. The token is a
// credential: callers that answer to users must not pass it on.
type LookupSettings struct {
	URL   string
	Token string
	Auto  bool
}

// GetSettings returns a collection's lookup settings, which are all empty and off
// until the owner sets them.
func (r *EntityLookupRepository) GetSettings(ctx context.Context, gid uuid.UUID) (LookupSettings, error) {
	g, err := r.db.Group.Get(ctx, gid)
	if err != nil {
		return LookupSettings{}, err
	}
	return LookupSettings{URL: g.LookupURL, Token: g.LookupToken, Auto: g.LookupAuto}, nil
}

// SetSettings stores a collection's lookup settings. A nil token leaves the
// stored one as it is, so the form can be saved without typing it again.
func (r *EntityLookupRepository) SetSettings(ctx context.Context, gid uuid.UUID, url string, token *string, auto bool) error {
	q := r.db.Group.UpdateOneID(gid).SetLookupURL(url).SetLookupAuto(auto)
	if token != nil {
		q.SetLookupToken(*token)
	}
	return q.Exec(ctx)
}

type (
	// EntityLookupListItem is one row of the activity list.
	EntityLookupListItem struct {
		EntityID   uuid.UUID `json:"entityId"`
		EntityName string    `json:"entityName"`
		Status     string    `json:"status"`
		Query      string    `json:"query"`
		Error      string    `json:"error"`
		Matches    int       `json:"matches"`
		UpdatedAt  time.Time `json:"updatedAt"`
	}

	// EntityLookupCounts is how many lookups there are in each state.
	EntityLookupCounts struct {
		Pending int `json:"pending"`
		Done    int `json:"done"`
		Failed  int `json:"failed"`
	}

	// LookupSubject is an item that could be looked up, with what it is searched by.
	LookupSubject struct {
		ID           uuid.UUID
		Name         string
		Manufacturer string
		ModelNumber  string
	}
)

// List returns a collection's lookups, newest activity first, optionally of one
// status, together with how many there are of each status.
func (r *EntityLookupRepository) List(ctx context.Context, gid uuid.UUID, status string, limit int) ([]EntityLookupListItem, EntityLookupCounts, error) {
	var counts EntityLookupCounts
	for _, c := range []struct {
		status entitylookup.Status
		into   *int
	}{{entitylookup.StatusPending, &counts.Pending}, {entitylookup.StatusDone, &counts.Done}, {entitylookup.StatusFailed, &counts.Failed}} {
		n, err := r.db.EntityLookup.Query().Where(entitylookup.HasGroupWith(group.ID(gid)), entitylookup.StatusEQ(c.status)).Count(ctx)
		if err != nil {
			return nil, counts, err
		}
		*c.into = n
	}

	q := r.db.EntityLookup.Query().Where(entitylookup.HasGroupWith(group.ID(gid)))
	if status != "" {
		q = q.Where(entitylookup.StatusEQ(entitylookup.Status(status)))
	}
	rows, err := q.WithEntity().Order(ent.Desc(entitylookup.FieldUpdatedAt)).Limit(limit).All(ctx)
	if err != nil {
		return nil, counts, err
	}
	out := make([]EntityLookupListItem, 0, len(rows))
	for _, l := range rows {
		item := EntityLookupListItem{
			EntityID: l.EntityID, Status: string(l.Status), Query: l.Query, Error: l.Error,
			Matches: len(l.Candidates), UpdatedAt: l.UpdatedAt,
		}
		if l.Edges.Entity != nil {
			item.EntityName = l.Edges.Entity.Name
		}
		out = append(out, item)
	}
	return out, counts, nil
}

// DeleteByStatus clears a collection's lookups of the given statuses, which
// dismisses them. It returns how many went.
func (r *EntityLookupRepository) DeleteByStatus(ctx context.Context, gid uuid.UUID, statuses ...entitylookup.Status) (int, error) {
	return r.db.EntityLookup.Delete().
		Where(entitylookup.HasGroupWith(group.ID(gid)), entitylookup.StatusIn(statuses...)).Exec(ctx)
}

// RetryFailed puts a collection's failed lookups back in the queue.
func (r *EntityLookupRepository) RetryFailed(ctx context.Context, gid uuid.UUID) (int, error) {
	return r.db.EntityLookup.Update().
		Where(entitylookup.HasGroupWith(group.ID(gid)), entitylookup.StatusEQ(entitylookup.StatusFailed)).
		SetStatus(entitylookup.StatusPending).SetError("").SetAttempts(0).Save(ctx)
}

// Missing finds items that have no lookup yet and are missing at least one of the
// given details: description, manufacturer, modelNumber or photo. Locations and
// archived items are left out. It returns up to limit of them, and how many there
// are in all.
func (r *EntityLookupRepository) Missing(ctx context.Context, gid uuid.UUID, missing []string, limit int) ([]LookupSubject, int, error) {
	var conds []predicate.Entity
	for _, m := range missing {
		switch m {
		case "description":
			conds = append(conds, entity.Or(entity.DescriptionIsNil(), entity.DescriptionEQ("")))
		case "manufacturer":
			conds = append(conds, entity.Or(entity.ManufacturerIsNil(), entity.ManufacturerEQ("")))
		case "modelNumber":
			conds = append(conds, entity.Or(entity.ModelNumberIsNil(), entity.ModelNumberEQ("")))
		case "photo":
			conds = append(conds, entity.Not(entity.HasAttachmentsWith(attachment.TypeEQ(attachment.TypePhoto))))
		}
	}
	if len(conds) == 0 {
		return nil, 0, nil
	}

	q := r.db.Entity.Query().Where(
		entity.HasGroupWith(group.ID(gid)),
		entity.HasEntityTypeWith(entitytype.IsLocation(false)),
		entity.Archived(false),
		entity.Not(entity.HasLookups()),
		entity.Or(conds...),
	)
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Order(ent.Asc(entity.FieldName)).Limit(limit).All(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]LookupSubject, 0, len(rows))
	for _, e := range rows {
		out = append(out, LookupSubject{ID: e.ID, Name: e.Name, Manufacturer: e.Manufacturer, ModelNumber: e.ModelNumber})
	}
	return out, total, nil
}

// QueueMany queues a lookup for each given item that is in the collection and has
// none yet. It returns how many were queued.
func (r *EntityLookupRepository) QueueMany(ctx context.Context, gid uuid.UUID, queries map[uuid.UUID]string) (int, error) {
	queued := 0
	for id, query := range queries {
		err := r.db.EntityLookup.Create().SetGroupID(gid).SetEntityID(id).SetQuery(query).Exec(ctx)
		if ent.IsConstraintError(err) { // already has one
			continue
		}
		if err != nil {
			return queued, err
		}
		queued++
	}
	return queued, nil
}
