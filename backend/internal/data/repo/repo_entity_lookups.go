package repo

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entity"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entitylookup"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/group"
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
