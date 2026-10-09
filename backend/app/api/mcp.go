package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	v1 "github.com/sysadminsmedia/homebox/backend/app/api/handlers/v1"
	"github.com/sysadminsmedia/homebox/backend/internal/mcpserver"
	"github.com/sysadminsmedia/homebox/backend/internal/oauthserver"
)

// trustedBaseURL is the instance's public URL as the operator configured it, or
// "" when none can be trusted. OAuth issuer and endpoint URLs derive from it, so
// it must never be taken from an unvalidated request header.
func (a *app) trustedBaseURL(r *http.Request) string {
	return v1.SecureBaseURL(r, &a.conf.Options)
}

func (a *app) newOAuthServer() *oauthserver.Server {
	if !a.conf.MCP.Enabled || !a.conf.MCP.Oauth.Enabled {
		return nil
	}
	return oauthserver.New(a.repos, a.conf.MCP.Oauth, a.trustedBaseURL, func(r *http.Request) string {
		return extractClientIP(r, a.conf.Options.TrustProxy)
	})
}

// mountMCP serves the Model Context Protocol endpoint. It sits outside /api/v1
// because it is a different protocol with its own authentication (API keys and
// OAuth access tokens only; never session cookies), which the endpoint performs
// itself.
func (a *app) mountMCP(r chi.Router, oauth *oauthserver.Server) {
	deps := mcpserver.Deps{
		Repos:    a.repos,
		Services: a.services,
		Conf:     a.conf.MCP,
		Version:  version,
		BaseURL:  a.trustedBaseURL,
	}
	if oauth != nil {
		deps.OAuth = oauth // only when set: a typed-nil would defeat the nil check
		oauth.Mount(r)
	}

	r.Handle("/mcp", mcpserver.New(deps))
	log.Info().
		Bool("oauth", oauth != nil).
		Bool("allow_writes", a.conf.MCP.AllowWrites).
		Bool("allow_delete", a.conf.MCP.AllowDelete).
		Int("rate_limit_per_minute", a.conf.MCP.RateLimitPerMinute).
		Msg("MCP server enabled at /mcp")
}
