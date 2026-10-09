package repo

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/oauthclient"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/oauthgrant"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/oauthrequest"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/oauthtoken"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// OAuthRepository stores the state of the built-in OAuth 2.1 authorization
// server: registered clients, pending authorization requests, user grants and
// issued tokens. Secrets are stored only as keyed hashes.
type OAuthRepository struct {
	db *ent.Client
}

type (
	OAuthClientOut struct {
		ID           uuid.UUID `json:"-"`
		ClientID     string    `json:"clientId"`
		Name         string    `json:"name"`
		RedirectURIs []string  `json:"redirectUris"`
		CreatedAt    time.Time `json:"createdAt"`
	}

	OAuthRequestOut struct {
		ID            uuid.UUID
		ClientDBID    uuid.UUID
		ClientID      string
		ClientName    string
		RedirectURI   string
		Scopes        []string
		State         string
		CodeChallenge string
		Resource      string
		ExpiresAt     time.Time
	}

	// OAuthGrantOut is a user's standing consent for one client.
	OAuthGrantOut struct {
		ID         uuid.UUID  `json:"id"`
		UserID     uuid.UUID  `json:"userId"`
		GroupID    uuid.UUID  `json:"groupId"`
		GroupName  string     `json:"groupName"`
		ClientID   string     `json:"clientId"`
		ClientName string     `json:"clientName"`
		Scopes     []string   `json:"scopes"`
		CreatedAt  time.Time  `json:"createdAt"`
		LastUsedAt *time.Time `json:"lastUsedAt" extensions:"x-nullable"`
	}

	// OAuthTokenOut is a stored token together with the grant it belongs to.
	OAuthTokenOut struct {
		ID            uuid.UUID
		Kind          string
		ExpiresAt     time.Time
		UsedAt        *time.Time
		CodeChallenge string
		RedirectURI   string

		GrantID    uuid.UUID
		UserID     uuid.UUID
		GroupID    uuid.UUID
		Scopes     []string
		ClientDBID uuid.UUID
		ClientID   string
		ClientName string
	}
)

func mapOAuthClient(c *ent.OAuthClient) OAuthClientOut {
	return OAuthClientOut{ID: c.ID, ClientID: c.ClientID, Name: c.Name, RedirectURIs: c.RedirectUris, CreatedAt: c.CreatedAt}
}

// ---- clients ----

func (r *OAuthRepository) CreateClient(ctx context.Context, clientID, name string, redirectURIs []string) (OAuthClientOut, error) {
	c, err := r.db.OAuthClient.Create().SetClientID(clientID).SetName(name).SetRedirectUris(redirectURIs).Save(ctx)
	if err != nil {
		return OAuthClientOut{}, err
	}
	return mapOAuthClient(c), nil
}

func (r *OAuthRepository) GetClient(ctx context.Context, clientID string) (OAuthClientOut, error) {
	c, err := r.db.OAuthClient.Query().Where(oauthclient.ClientID(clientID)).Only(ctx)
	if err != nil {
		return OAuthClientOut{}, err
	}
	return mapOAuthClient(c), nil
}

func (r *OAuthRepository) CountClients(ctx context.Context) (int, error) {
	return r.db.OAuthClient.Query().Count(ctx)
}

// PruneUnusedClients deletes clients registered before cutoff that no user ever
// granted anything to. Dynamic registration is unauthenticated, so this keeps
// abandoned registrations from accumulating.
func (r *OAuthRepository) PruneUnusedClients(ctx context.Context, cutoff time.Time) (int, error) {
	return r.db.OAuthClient.Delete().
		Where(oauthclient.CreatedAtLT(cutoff), oauthclient.Not(oauthclient.HasGrants())).
		Exec(ctx)
}

// ---- pending authorization requests ----

func (r *OAuthRepository) CreateRequest(ctx context.Context, clientDBID uuid.UUID, redirectURI string, scopes []string, state, challenge, resource string, expiresAt time.Time) (uuid.UUID, error) {
	q := r.db.OAuthRequest.Create().
		SetClientID(clientDBID).
		SetRedirectURI(redirectURI).
		SetScopes(scopes).
		SetCodeChallenge(challenge).
		SetExpiresAt(expiresAt)
	if state != "" {
		q.SetState(state)
	}
	if resource != "" {
		q.SetResource(resource)
	}
	req, err := q.Save(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	return req.ID, nil
}

// GetRequest returns an unexpired pending request.
func (r *OAuthRepository) GetRequest(ctx context.Context, id uuid.UUID, now time.Time) (OAuthRequestOut, error) {
	req, err := r.db.OAuthRequest.Query().
		Where(oauthrequest.ID(id), oauthrequest.ExpiresAtGT(now)).
		WithClient().
		Only(ctx)
	if err != nil {
		return OAuthRequestOut{}, err
	}
	return OAuthRequestOut{
		ID: req.ID, ClientDBID: req.Edges.Client.ID, ClientID: req.Edges.Client.ClientID, ClientName: req.Edges.Client.Name,
		RedirectURI: req.RedirectURI, Scopes: req.Scopes, State: req.State, CodeChallenge: req.CodeChallenge,
		Resource: req.Resource, ExpiresAt: req.ExpiresAt,
	}, nil
}

// TakeRequest atomically deletes a pending request, so a decision can be made
// once. It reports whether this call was the one that removed it.
func (r *OAuthRepository) TakeRequest(ctx context.Context, id uuid.UUID) (bool, error) {
	n, err := r.db.OAuthRequest.Delete().Where(oauthrequest.ID(id)).Exec(ctx)
	return n == 1, err
}

func (r *OAuthRepository) PruneRequests(ctx context.Context, now time.Time) (int, error) {
	return r.db.OAuthRequest.Delete().Where(oauthrequest.ExpiresAtLT(now)).Exec(ctx)
}

// ---- grants ----

// CreateGrant records consent. A user re-authorizing the same client for the same
// collection replaces the earlier grant (and so revokes its tokens), so grants
// never pile up and the newest consent is the only one in force.
func (r *OAuthRepository) CreateGrant(ctx context.Context, userID, groupID, clientDBID uuid.UUID, scopes []string) (uuid.UUID, error) {
	ctx, span := entityTracer().Start(ctx, "repo.OAuthRepository.CreateGrant", trace.WithAttributes(
		attribute.String("user.id", userID.String()), attribute.String("group.id", groupID.String())))
	defer span.End()

	tx, err := r.db.Tx(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.OAuthGrant.Delete().
		Where(oauthgrant.UserID(userID), oauthgrant.GroupID(groupID), oauthgrant.HasClientWith(oauthclient.ID(clientDBID))).
		Exec(ctx); err != nil {
		_ = tx.Rollback()
		return uuid.Nil, err
	}
	g, err := tx.OAuthGrant.Create().
		SetUserID(userID).SetGroupID(groupID).SetClientID(clientDBID).SetScopes(scopes).Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return uuid.Nil, err
	}
	if err := tx.Commit(); err != nil {
		return uuid.Nil, err
	}
	return g.ID, nil
}

func (r *OAuthRepository) ListGrantsByUser(ctx context.Context, userID uuid.UUID) ([]OAuthGrantOut, error) {
	rows, err := r.db.OAuthGrant.Query().
		Where(oauthgrant.UserID(userID)).
		WithClient().
		WithGroup().
		Order(ent.Desc(oauthgrant.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]OAuthGrantOut, 0, len(rows))
	for _, g := range rows {
		out = append(out, OAuthGrantOut{
			ID: g.ID, UserID: g.UserID, GroupID: g.GroupID, GroupName: g.Edges.Group.Name,
			ClientID: g.Edges.Client.ClientID, ClientName: g.Edges.Client.Name,
			Scopes: g.Scopes, CreatedAt: g.CreatedAt, LastUsedAt: g.LastUsedAt,
		})
	}
	return out, nil
}

// DeleteGrantForUser revokes a grant, and every token under it, if it is the
// user's. It returns a not-found error otherwise, indistinguishable from a
// missing grant.
func (r *OAuthRepository) DeleteGrantForUser(ctx context.Context, userID, grantID uuid.UUID) error {
	n, err := r.db.OAuthGrant.Delete().Where(oauthgrant.ID(grantID), oauthgrant.UserID(userID)).Exec(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return &ent.NotFoundError{}
	}
	return nil
}

// RevokeGrant revokes a grant unconditionally. Used when token theft is detected.
func (r *OAuthRepository) RevokeGrant(ctx context.Context, grantID uuid.UUID) error {
	return r.db.OAuthGrant.DeleteOneID(grantID).Exec(ctx)
}

func (r *OAuthRepository) TouchGrant(ctx context.Context, grantID uuid.UUID, at time.Time) error {
	return r.db.OAuthGrant.UpdateOneID(grantID).SetLastUsedAt(at).Exec(ctx)
}

// ---- tokens ----

func (r *OAuthRepository) IssueToken(ctx context.Context, grantID uuid.UUID, kind string, hash []byte, expiresAt time.Time, challenge, redirectURI string) error {
	q := r.db.OAuthToken.Create().
		SetGrantID(grantID).
		SetKind(oauthtoken.Kind(kind)).
		SetTokenHash(hash).
		SetExpiresAt(expiresAt)
	if challenge != "" {
		q.SetCodeChallenge(challenge)
	}
	if redirectURI != "" {
		q.SetRedirectURI(redirectURI)
	}
	return q.Exec(ctx)
}

// GetToken looks a token up by hash, whatever its state; callers decide what
// expiry and reuse mean for them.
func (r *OAuthRepository) GetToken(ctx context.Context, hash []byte) (OAuthTokenOut, error) {
	t, err := r.db.OAuthToken.Query().
		Where(oauthtoken.TokenHash(hash)).
		WithGrant(func(q *ent.OAuthGrantQuery) { q.WithClient() }).
		Only(ctx)
	if err != nil {
		return OAuthTokenOut{}, err
	}
	g := t.Edges.Grant
	return OAuthTokenOut{
		ID: t.ID, Kind: string(t.Kind), ExpiresAt: t.ExpiresAt, UsedAt: t.UsedAt,
		CodeChallenge: t.CodeChallenge, RedirectURI: t.RedirectURI,
		GrantID: g.ID, UserID: g.UserID, GroupID: g.GroupID, Scopes: g.Scopes,
		ClientDBID: g.Edges.Client.ID, ClientID: g.Edges.Client.ClientID, ClientName: g.Edges.Client.Name,
	}, nil
}

// MarkTokenUsed atomically spends a single-use token (an authorization code or a
// refresh token). It reports whether this call was the one that spent it; under
// a race exactly one caller gets true.
func (r *OAuthRepository) MarkTokenUsed(ctx context.Context, id uuid.UUID, at time.Time) (bool, error) {
	n, err := r.db.OAuthToken.Update().
		Where(oauthtoken.ID(id), oauthtoken.UsedAtIsNil()).
		SetUsedAt(at).
		Save(ctx)
	return n == 1, err
}

func (r *OAuthRepository) DeleteExpiredTokens(ctx context.Context, now time.Time) (int, error) {
	return r.db.OAuthToken.Delete().Where(oauthtoken.ExpiresAtLT(now)).Exec(ctx)
}
