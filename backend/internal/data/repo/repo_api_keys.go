package repo

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/apikey"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type APIKeyRepository struct {
	db     *ent.Client
	mapper MapFunc[*ent.APIKey, APIKeyOut]
}

func NewAPIKeyRepository(db *ent.Client) *APIKeyRepository {
	return &APIKeyRepository{
		db: db,
		mapper: func(k *ent.APIKey) APIKeyOut {
			return APIKeyOut{
				ID:         k.ID,
				UserID:     k.UserID,
				Name:       k.Name,
				CreatedAt:  k.CreatedAt,
				ExpiresAt:  k.ExpiresAt,
				LastUsedAt: k.LastUsedAt,
				Scopes:     k.Scopes,
				GroupID:    k.GroupID,
			}
		},
	}
}

type (
	APIKeyCreate struct {
		Name      string     `json:"name"      validate:"required,min=1,max=255"`
		ExpiresAt *time.Time `json:"expiresAt" extensions:"x-nullable"`
		// Preset is a shortcut for Scopes: read-only, read-write or full. It is
		// ignored when Scopes is set. With neither, the key is read-only.
		Preset string `json:"preset" validate:"omitempty,oneof=read-only read-write full"`
		// Scopes limits what the key may do, e.g. "items:read".
		Scopes []string `json:"scopes"`
		// GroupID pins the key to one collection the user belongs to.
		GroupID *uuid.UUID `json:"groupId" extensions:"x-nullable"`
	}

	// APIKeyGrant is what an authenticated API key is allowed to do.
	APIKeyGrant struct {
		ID      uuid.UUID
		Name    string
		Scopes  []string
		GroupID *uuid.UUID
	}

	// APIKeyOut is the metadata of an API key, returned for list views. The raw
	// token is never included here — see APIKeyCreatedOut for that.
	APIKeyOut struct {
		ID         uuid.UUID  `json:"id"`
		UserID     uuid.UUID  `json:"userId"`
		Name       string     `json:"name"`
		CreatedAt  time.Time  `json:"createdAt"`
		ExpiresAt  *time.Time `json:"expiresAt"  extensions:"x-nullable"`
		LastUsedAt *time.Time `json:"lastUsedAt" extensions:"x-nullable"`
		Scopes     []string   `json:"scopes"`
		GroupID    *uuid.UUID `json:"groupId"    extensions:"x-nullable"`
	}

	// APIKeyCreatedOut is returned exactly once at creation time and contains
	// the raw token. After this response the raw token is unrecoverable.
	APIKeyCreatedOut struct {
		APIKeyOut
		Token string `json:"token"`
	}
)

// Create persists a new API key for the given user. The caller supplies the
// pre-hashed token bytes; the raw token is never stored.
func (r *APIKeyRepository) Create(ctx context.Context, userID uuid.UUID, name string, tokenHash []byte, expiresAt *time.Time, scopes []string, groupID *uuid.UUID) (APIKeyOut, error) {
	ctx, span := entityTracer().Start(ctx, "repo.APIKeyRepository.Create",
		trace.WithAttributes(
			attribute.String("user.id", userID.String()),
			attribute.Int("api_key.name.length", len(name)),
			attribute.Bool("api_key.has_expiration", expiresAt != nil),
		))
	defer span.End()

	q := r.db.APIKey.Create().
		SetUserID(userID).
		SetName(name).
		SetToken(tokenHash).
		SetScopes(scopes)

	if expiresAt != nil {
		q.SetExpiresAt(*expiresAt)
	}
	if groupID != nil {
		q.SetGroupID(*groupID)
	}

	key, err := q.Save(ctx)
	if err != nil {
		recordSpanError(span, err)
		return APIKeyOut{}, err
	}
	span.SetAttributes(attribute.String("api_key.id", key.ID.String()))
	return r.mapper.Map(key), nil
}

// GetUserFromToken returns the user that owns the API key with the given hash,
// if it exists and has not expired. The matching key's ID is returned so that
// the caller can update last_used_at.
func (r *APIKeyRepository) GetUserFromToken(ctx context.Context, tokenHash []byte) (UserOut, uuid.UUID, error) {
	usr, grant, err := r.GetGrantFromToken(ctx, tokenHash)
	return usr, grant.ID, err
}

// GetGrantFromToken is GetUserFromToken plus the key's scopes and collection
// pin, which the auth middleware needs to enforce limits.
func (r *APIKeyRepository) GetGrantFromToken(ctx context.Context, tokenHash []byte) (UserOut, APIKeyGrant, error) {
	ctx, span := entityTracer().Start(ctx, "repo.APIKeyRepository.GetGrantFromToken",
		trace.WithAttributes(attribute.Int("token.hash.length", len(tokenHash))))
	defer span.End()

	key, err := r.db.APIKey.Query().
		Where(apikey.Token(tokenHash)).
		WithUser(func(uq *ent.UserQuery) {
			uq.WithGroups()
		}).
		Only(ctx)
	if err != nil {
		span.SetAttributes(
			attribute.Bool("api_key.found", false),
			attribute.Bool("api_key.lookup.not_found", ent.IsNotFound(err)),
		)
		if !ent.IsNotFound(err) {
			recordSpanError(span, err)
		}
		return UserOut{}, APIKeyGrant{}, err
	}

	if key.ExpiresAt != nil && key.ExpiresAt.Before(time.Now()) {
		span.SetAttributes(
			attribute.Bool("api_key.found", true),
			attribute.Bool("api_key.expired", true),
		)
		return UserOut{}, APIKeyGrant{}, &ent.NotFoundError{}
	}

	out := mapUserOut(key.Edges.User)
	span.SetAttributes(
		attribute.Bool("api_key.found", true),
		attribute.String("api_key.id", key.ID.String()),
		attribute.StringSlice("api_key.scopes", key.Scopes),
		attribute.Bool("api_key.group_pinned", key.GroupID != nil),
	)
	span.SetAttributes(userSpanAttrs(out)...)
	return out, APIKeyGrant{ID: key.ID, Name: key.Name, Scopes: key.Scopes, GroupID: key.GroupID}, nil
}

// TouchLastUsed updates the last_used_at timestamp on the given API key.
// Failures are best-effort: callers should log but not abort the request.
func (r *APIKeyRepository) TouchLastUsed(ctx context.Context, id uuid.UUID, at time.Time) error {
	ctx, span := entityTracer().Start(ctx, "repo.APIKeyRepository.TouchLastUsed",
		trace.WithAttributes(attribute.String("api_key.id", id.String())))
	defer span.End()

	err := r.db.APIKey.UpdateOneID(id).SetLastUsedAt(at).Exec(ctx)
	if err != nil {
		recordSpanError(span, err)
	}
	return err
}

func (r *APIKeyRepository) GetByUser(ctx context.Context, userID uuid.UUID) ([]APIKeyOut, error) {
	ctx, span := entityTracer().Start(ctx, "repo.APIKeyRepository.GetByUser",
		trace.WithAttributes(attribute.String("user.id", userID.String())))
	defer span.End()

	keys, err := r.db.APIKey.Query().
		Where(apikey.UserID(userID)).
		Order(ent.Desc(apikey.FieldCreatedAt)).
		All(ctx)

	out, err := r.mapper.MapEachErr(keys, err)
	if err != nil {
		recordSpanError(span, err)
		return nil, err
	}
	span.SetAttributes(attribute.Int("api_keys.count", len(out)))
	return out, nil
}

func (r *APIKeyRepository) Delete(ctx context.Context, userID, id uuid.UUID) error {
	ctx, span := entityTracer().Start(ctx, "repo.APIKeyRepository.Delete",
		trace.WithAttributes(
			attribute.String("user.id", userID.String()),
			attribute.String("api_key.id", id.String()),
		))
	defer span.End()

	deleted, err := r.db.APIKey.Delete().
		Where(apikey.UserID(userID), apikey.ID(id)).
		Exec(ctx)
	if err != nil {
		recordSpanError(span, err)
		return err
	}
	span.SetAttributes(attribute.Int("api_keys.deleted.count", deleted))
	if deleted == 0 {
		return &ent.NotFoundError{}
	}
	return nil
}
