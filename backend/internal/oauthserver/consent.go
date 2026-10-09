package oauthserver

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"net/url"

	"github.com/google/uuid"
	"github.com/sysadminsmedia/homebox/backend/internal/core/scopes"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
)

// ConsentScope is one requested permission, described for the consent screen.
type ConsentScope struct {
	Scope       string `json:"scope"`
	Description string `json:"description"`
	Mutating    bool   `json:"mutating"`
}

// ConsentCollection is a collection the user could connect the application to.
type ConsentCollection struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	// MCPAccess is the owner's ceiling: off, read, write or full. "off" means the
	// application cannot be connected to this collection yet.
	MCPAccess string `json:"mcpAccess"`
	// Allowed are the requested scopes this collection's ceiling currently permits.
	Allowed []string `json:"allowed"`
}

// ConsentInfo is everything the consent screen shows.
type ConsentInfo struct {
	ClientName string `json:"clientName"`
	ClientID   string `json:"clientId"`
	// RedirectHost is where the user will be sent back to. Shown so a user can
	// notice an application calling itself something it isn't.
	RedirectHost string              `json:"redirectHost"`
	Scopes       []ConsentScope      `json:"scopes"`
	Collections  []ConsentCollection `json:"collections"`
	ExpiresAt    time.Time           `json:"expiresAt"`
}

// ErrRequestGone is returned for an unknown, expired or already-decided request.
var ErrRequestGone = validate.NewRequestError(errors.New("this authorization request has expired or was already handled; start again from the application"), http.StatusGone)

func isMutating(s string) bool { return !scopes.ReadOnly([]string{s}) }

func (s *Server) loadRequest(ctx context.Context, id uuid.UUID) (repo.OAuthRequestOut, error) {
	req, err := s.repos.OAuth.GetRequest(ctx, id, s.now())
	if err != nil {
		if ent.IsNotFound(err) {
			return repo.OAuthRequestOut{}, ErrRequestGone
		}
		return repo.OAuthRequestOut{}, err
	}
	return req, nil
}

// RequestInfo describes a pending request to the signed-in user who must decide it.
func (s *Server) RequestInfo(ctx context.Context, user *repo.UserOut, id uuid.UUID) (ConsentInfo, error) {
	req, err := s.loadRequest(ctx, id)
	if err != nil {
		return ConsentInfo{}, err
	}

	host := req.RedirectURI
	if u, err := url.Parse(req.RedirectURI); err == nil {
		host = u.Host
		if host == "" {
			host = u.Scheme + "://"
		}
	}

	info := ConsentInfo{ClientName: req.ClientName, ClientID: req.ClientID, RedirectHost: host, ExpiresAt: req.ExpiresAt}
	for _, sc := range req.Scopes {
		info.Scopes = append(info.Scopes, ConsentScope{Scope: sc, Description: scopes.Description(sc), Mutating: isMutating(sc)})
	}
	for _, gid := range user.GroupIDs {
		g, err := s.repos.Groups.GroupByID(ctx, gid)
		if err != nil {
			return ConsentInfo{}, err
		}
		info.Collections = append(info.Collections, ConsentCollection{
			ID: g.ID, Name: g.Name, MCPAccess: g.MCPAccess,
			Allowed: scopes.Intersect(req.Scopes, scopes.Ceiling(g.MCPAccess)),
		})
	}
	return info, nil
}

// Approve records the user's consent and returns the URL to send their browser
// to: the client's redirect URI carrying an authorization code.
//
// The user may narrow the requested scopes but never widen them, and may only
// pick a collection they belong to and whose owner has opened it to assistants.
func (s *Server) Approve(ctx context.Context, user *repo.UserOut, id, groupID uuid.UUID, chosen []string, issuer string) (string, error) {
	req, err := s.loadRequest(ctx, id)
	if err != nil {
		return "", err
	}

	if !slices.Contains(user.GroupIDs, groupID) {
		return "", validate.NewRequestError(errors.New("you are not a member of that collection"), http.StatusForbidden)
	}
	g, err := s.repos.Groups.GroupByID(ctx, groupID)
	if err != nil {
		return "", err
	}
	if g.MCPAccess == scopes.AccessOff {
		return "", validate.NewRequestError(errors.New("AI assistant access is turned off for this collection; ask its owner to enable it"), http.StatusForbidden)
	}

	if len(chosen) == 0 {
		chosen = req.Scopes
	}
	chosen, err = scopes.Normalize(chosen)
	if err != nil {
		return "", validate.NewRequestError(err, http.StatusUnprocessableEntity)
	}
	if !scopes.Subset(chosen, req.Scopes) {
		return "", validate.NewRequestError(errors.New("you can only approve permissions the application asked for"), http.StatusUnprocessableEntity)
	}
	if len(scopes.Intersect(chosen, scopes.Ceiling(g.MCPAccess))) == 0 {
		return "", validate.NewRequestError(errors.New("this collection's AI access level allows none of the chosen permissions"), http.StatusUnprocessableEntity)
	}

	// Decide once. The request is deleted atomically, so a double-click or a
	// replayed approve cannot mint two codes.
	won, err := s.repos.OAuth.TakeRequest(ctx, id)
	if err != nil {
		return "", err
	}
	if !won {
		return "", ErrRequestGone
	}

	grantID, err := s.repos.OAuth.CreateGrant(ctx, user.ID, groupID, req.ClientDBID, chosen)
	if err != nil {
		return "", err
	}
	code, hash := newSecret("hbk_")
	if err := s.repos.OAuth.IssueToken(ctx, grantID, "code", hash, s.now().Add(codeTTL), req.CodeChallenge, req.RedirectURI); err != nil {
		return "", err
	}
	return withParams(req.RedirectURI, map[string]string{"code": code, "state": req.State, "iss": issuer})
}

// Deny refuses a request and returns the URL that reports access_denied to the client.
func (s *Server) Deny(ctx context.Context, id uuid.UUID, issuer string) (string, error) {
	req, err := s.loadRequest(ctx, id)
	if err != nil {
		return "", err
	}
	if won, err := s.repos.OAuth.TakeRequest(ctx, id); err != nil {
		return "", err
	} else if !won {
		return "", ErrRequestGone
	}
	return withParams(req.RedirectURI, map[string]string{
		"error": "access_denied", "error_description": "the user declined", "state": req.State, "iss": issuer,
	})
}
