package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	v1 "github.com/sysadminsmedia/homebox/backend/app/api/handlers/v1"
	"github.com/sysadminsmedia/homebox/backend/internal/mcpserver"
)

// trustedBaseURL is the instance's public URL as the operator configured it, or
// "" when none can be trusted. It must never be taken from an unvalidated
// request header.
func (a *app) trustedBaseURL(r *http.Request) string {
	return v1.SecureBaseURL(r, &a.conf.Options)
}

// mountMCP serves the Model Context Protocol endpoint. It sits outside /api/v1
// because it is a different protocol with its own authentication (API keys
// only; never session cookies), which the endpoint performs itself.
func (a *app) mountMCP(r chi.Router) {
	srv := mcpserver.New(mcpserver.Deps{
		Repos:    a.repos,
		Services: a.services,
		Conf:     a.conf.MCP,
		Version:  version,
		BaseURL:  a.trustedBaseURL,
	})

	r.Handle("/mcp", srv)
	log.Info().
		Bool("allow_writes", a.conf.MCP.AllowWrites).
		Bool("allow_delete", a.conf.MCP.AllowDelete).
		Int("rate_limit_per_minute", a.conf.MCP.RateLimitPerMinute).
		Msg("MCP server enabled at /mcp")
}
