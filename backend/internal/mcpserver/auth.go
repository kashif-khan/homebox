package mcpserver

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/sysadminsmedia/homebox/backend/internal/core/scopes"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/pkgs/hasher"
)

// Credential kinds recorded on a Principal and in the audit log.
const (
	CredentialAPIKey = "api_key"
	CredentialOAuth  = "oauth"
)

// Principal is who is calling and what they may do. It is built once per HTTP
// request by authenticate and is the only source of authority for tools: they
// never consult the raw credential again.
type Principal struct {
	User *repo.UserOut
	// GroupID is the collection the call acts in. Every query is scoped to it.
	GroupID uuid.UUID
	// Scopes are the effective scopes: the credential's own scopes, narrowed by
	// the collection owner's ceiling and the operator's instance caps.
	Scopes []string

	CredentialKind string
	CredentialID   uuid.UUID
	CredentialName string
}

type principalKey struct{}

func withPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFrom returns the authenticated principal for a request context.
func PrincipalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}

// OAuthVerifier validates an OAuth access token issued by this server.
type OAuthVerifier interface {
	// Verify returns the grant behind an access token, or ErrInvalidToken.
	Verify(ctx context.Context, token string) (*OAuthGrant, error)
}

// OAuthGrant is what a verified OAuth access token stands for.
type OAuthGrant struct {
	ID       uuid.UUID
	UserID   uuid.UUID
	GroupID  uuid.UUID
	Scopes   []string
	ClientID string
	Name     string
}

// ErrInvalidToken is returned by an OAuthVerifier for unknown, expired or
// revoked tokens.
var ErrInvalidToken = errors.New("invalid token")

// authError is an authentication or authorization failure with the HTTP status
// to report. Descriptions are safe to show to the caller.
type authError struct {
	status int
	code   string
	msg    string
}

func (e *authError) Error() string { return e.msg }

func unauthorized(msg string) *authError {
	return &authError{status: http.StatusUnauthorized, code: "invalid_token", msg: msg}
}

func forbidden(msg string) *authError {
	return &authError{status: http.StatusForbidden, code: "insufficient_scope", msg: msg}
}

// bearerToken extracts the token from an Authorization header. Query strings and
// cookies are deliberately not accepted: they leak into logs and referers, and
// cookies would open the endpoint to cross-site requests.
func bearerToken(r *http.Request) string {
	fields := strings.Fields(r.Header.Get("Authorization"))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "bearer") {
		return ""
	}
	return fields[1]
}

// authenticate resolves a request to a Principal.
//
// Order matters: identify the credential, resolve the collection, then narrow
// the credential's scopes by the collection owner's ceiling and the operator's
// caps. Failing any step denies the request before a tool is even listed.
func (s *Server) authenticate(r *http.Request) (*Principal, *authError) {
	ctx := r.Context()

	token := bearerToken(r)
	if token == "" {
		return nil, unauthorized("a bearer token is required")
	}

	var (
		usr     repo.UserOut
		granted []string
		pinned  *uuid.UUID
		p       Principal
	)

	switch {
	case strings.HasPrefix(token, hasher.APIKeyPrefix):
		u, grant, err := s.deps.Repos.APIKeys.GetGrantFromToken(ctx, hasher.HashAPIKey(token))
		if err != nil {
			if ent.IsNotFound(err) {
				return nil, unauthorized("unknown or expired API key")
			}
			log.Err(err).Msg("mcp: api key lookup failed")
			return nil, &authError{status: http.StatusInternalServerError, code: "server_error", msg: "authentication is unavailable"}
		}
		usr, granted, pinned = u, grant.Scopes, grant.GroupID
		p.CredentialKind, p.CredentialID, p.CredentialName = CredentialAPIKey, grant.ID, grant.Name

		if err := s.deps.Repos.APIKeys.TouchLastUsed(ctx, grant.ID, time.Now()); err != nil {
			log.Warn().Err(err).Str("api_key.id", grant.ID.String()).Msg("mcp: failed to update api key last_used_at")
		}

	case s.deps.OAuth != nil:
		g, err := s.deps.OAuth.Verify(ctx, token)
		if err != nil {
			if errors.Is(err, ErrInvalidToken) {
				return nil, unauthorized("unknown, expired or revoked access token")
			}
			log.Err(err).Msg("mcp: oauth token verification failed")
			return nil, &authError{status: http.StatusInternalServerError, code: "server_error", msg: "authentication is unavailable"}
		}
		u, err := s.deps.Repos.Users.GetOneID(ctx, g.UserID)
		if err != nil {
			return nil, unauthorized("the user behind this token no longer exists")
		}
		usr, granted = u, g.Scopes
		gid := g.GroupID
		pinned = &gid
		p.CredentialKind, p.CredentialID, p.CredentialName = CredentialOAuth, g.ID, g.Name

	default:
		return nil, unauthorized("unrecognised token")
	}

	// ---- collection ----
	gid := usr.DefaultGroupID
	if pinned != nil {
		gid = *pinned
	}
	if h := r.Header.Get("X-Tenant"); h != "" {
		want, err := uuid.Parse(h)
		if err != nil {
			return nil, &authError{status: http.StatusBadRequest, code: "invalid_request", msg: "invalid X-Tenant header"}
		}
		if pinned != nil && want != *pinned {
			return nil, forbidden("this credential is pinned to a different collection")
		}
		gid = want
	}
	member := false
	for _, g := range usr.GroupIDs {
		if g == gid {
			member = true
			break
		}
	}
	if !member {
		return nil, forbidden("you do not have access to this collection")
	}

	group, err := s.deps.Repos.Groups.GroupByID(ctx, gid)
	if err != nil {
		log.Err(err).Msg("mcp: collection lookup failed")
		return nil, &authError{status: http.StatusInternalServerError, code: "server_error", msg: "authentication is unavailable"}
	}

	// ---- effective scopes ----
	ceiling := scopes.Ceiling(group.MCPAccess)
	if !s.deps.Conf.AllowWrites {
		ceiling = scopes.Without(ceiling, scopes.Mutating()...)
	} else if !s.deps.Conf.AllowDelete {
		ceiling = scopes.Without(ceiling, scopes.ItemsDelete)
	}
	effective := scopes.Intersect(granted, ceiling)
	if len(effective) == 0 {
		if group.MCPAccess == scopes.AccessOff {
			return nil, forbidden("AI assistant access is turned off for this collection; ask its owner to enable it")
		}
		return nil, forbidden("this credential has no permissions that this collection allows")
	}

	if ok, retry := s.limiter.allow(p.CredentialID); !ok {
		return nil, &authError{status: http.StatusTooManyRequests, code: "rate_limited", msg: "rate limit exceeded; retry in " + retry.Round(time.Second).String()}
	}

	p.User, p.GroupID, p.Scopes = &usr, gid, effective
	return &p, nil
}
