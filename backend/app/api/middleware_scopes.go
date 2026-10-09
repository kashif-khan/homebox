package main

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/sysadminsmedia/homebox/backend/internal/core/scopes"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// scopeNever marks routes no API key may call, whatever its scopes. These are
// the credential-management and session-bound routes: a key must not be able to
// mint further keys, change the owner's password, or end the owner's sessions.
const scopeNever scopes.Scope = "!never"

// apiKeyRouteScopes maps "METHOD /chi/route/pattern" to the scope an API key
// needs to call it.
//
// The table is default-deny. A route that is absent needs the full-access
// scope, so a newly added route is unreachable for a restricted key until
// someone deliberately classifies it here. TestAPIKeyRouteScopesCoverEveryRoute
// fails if an entry points at a route that no longer exists.
var apiKeyRouteScopes = map[string]scopes.Scope{
	// ---- credential management & session-bound: never reachable by a key ----
	"GET /api/v1/users/self/api-keys":         scopeNever,
	"POST /api/v1/users/self/api-keys":        scopeNever,
	"DELETE /api/v1/users/self/api-keys/{id}": scopeNever,
	"POST /api/v1/users/logout":               scopeNever,
	"POST /api/v1/users/logout/all":           scopeNever,
	"GET /api/v1/users/refresh":               scopeNever,
	"PUT /api/v1/users/self/change-password":  scopeNever,
	"PUT /api/v1/users/self":                  scopeNever,
	"DELETE /api/v1/users/self":               scopeNever,

	// ---- collection:read ----
	"GET /api/v1/users/self":                       scopes.CollectionRead,
	"GET /api/v1/groups":                           scopes.CollectionRead,
	"GET /api/v1/groups/all":                       scopes.CollectionRead,
	"GET /api/v1/groups/members":                   scopes.CollectionRead,
	"GET /api/v1/groups/statistics":                scopes.CollectionRead,
	"GET /api/v1/groups/statistics/purchase-price": scopes.CollectionRead,
	"GET /api/v1/groups/statistics/locations":      scopes.CollectionRead,
	"GET /api/v1/groups/statistics/tags":           scopes.CollectionRead,

	// ---- items:read ----
	"GET /api/v1/tags":                         scopes.ItemsRead,
	"GET /api/v1/tags/{id}":                    scopes.ItemsRead,
	"GET /api/v1/entity-types":                 scopes.ItemsRead,
	"GET /api/v1/entities":                     scopes.ItemsRead,
	"GET /api/v1/entities/export":              scopes.ItemsRead,
	"GET /api/v1/entities/fields":              scopes.ItemsRead,
	"GET /api/v1/entities/fields/values":       scopes.ItemsRead,
	"GET /api/v1/entities/tree":                scopes.ItemsRead,
	"GET /api/v1/entities/{id}":                scopes.ItemsRead,
	"GET /api/v1/entities/{id}/path":           scopes.ItemsRead,
	"GET /api/v1/templates":                    scopes.ItemsRead,
	"GET /api/v1/templates/{id}":               scopes.ItemsRead,
	"GET /api/v1/assets/{id}":                  scopes.ItemsRead,
	"GET /api/v1/products/search-from-barcode": scopes.ItemsRead,
	"GET /api/v1/qrcode":                       scopes.ItemsRead,
	"GET /api/v1/labelmaker/entity/{id}":       scopes.ItemsRead,
	"GET /api/v1/labelmaker/location/{id}":     scopes.ItemsRead,
	"GET /api/v1/labelmaker/item/{id}":         scopes.ItemsRead,
	"GET /api/v1/labelmaker/asset/{id}":        scopes.ItemsRead,
	"GET /api/v1/reporting/bill-of-materials":  scopes.ItemsRead,

	// ---- items:write ----
	"POST /api/v1/tags":                       scopes.ItemsWrite,
	"PUT /api/v1/tags/{id}":                   scopes.ItemsWrite,
	"POST /api/v1/entity-types":               scopes.ItemsWrite,
	"PUT /api/v1/entity-types/{id}":           scopes.ItemsWrite,
	"POST /api/v1/entities":                   scopes.ItemsWrite,
	"PUT /api/v1/entities/{id}":               scopes.ItemsWrite,
	"PATCH /api/v1/entities/{id}":             scopes.ItemsWrite,
	"POST /api/v1/entities/{id}/duplicate":    scopes.ItemsWrite,
	"POST /api/v1/templates":                  scopes.ItemsWrite,
	"PUT /api/v1/templates/{id}":              scopes.ItemsWrite,
	"POST /api/v1/templates/{id}/create-item": scopes.ItemsWrite,

	// ---- items:delete ----
	"DELETE /api/v1/tags/{id}":         scopes.ItemsDelete,
	"DELETE /api/v1/entity-types/{id}": scopes.ItemsDelete,
	"DELETE /api/v1/entities/{id}":     scopes.ItemsDelete,
	"DELETE /api/v1/templates/{id}":    scopes.ItemsDelete,

	// ---- attachments ----
	"GET /api/v1/entities/{id}/attachments/{attachment_id}":    scopes.AttachmentsRead,
	"POST /api/v1/entities/{id}/attachments":                   scopes.AttachmentsWrite,
	"POST /api/v1/entities/{id}/attachments/external":          scopes.AttachmentsWrite,
	"PUT /api/v1/entities/{id}/attachments/{attachment_id}":    scopes.AttachmentsWrite,
	"DELETE /api/v1/entities/{id}/attachments/{attachment_id}": scopes.AttachmentsWrite,

	// ---- maintenance ----
	"GET /api/v1/entities/{id}/maintenance":  scopes.MaintenanceRead,
	"GET /api/v1/maintenance":                scopes.MaintenanceRead,
	"POST /api/v1/entities/{id}/maintenance": scopes.MaintenanceWrite,
	"PUT /api/v1/maintenance/{id}":           scopes.MaintenanceWrite,
	"DELETE /api/v1/maintenance/{id}":        scopes.MaintenanceWrite,
}

// requiredAPIKeyScope returns the scope needed for a route. Unlisted routes
// require scopes.Full.
func requiredAPIKeyScope(method, pattern string) scopes.Scope {
	if s, ok := apiKeyRouteScopes[method+" "+pattern]; ok {
		return s
	}
	return scopes.Full
}

// mwScopes enforces API key scopes. Session-token requests pass through
// untouched, as scopes only restrict API keys.
//
// WARNING: This middleware _MUST_ be called after mwAuthToken.
func (a *app) mwScopes(next errchain.Handler) errchain.Handler {
	return errchain.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		grant, isKey := services.UseAPIKeyGrant(r.Context())
		if !isKey {
			return next.ServeHTTP(w, r)
		}

		spanCtx, span := mwTracer().Start(r.Context(), "middleware.mwScopes")
		defer span.End()

		var pattern string
		if rc := chi.RouteContext(r.Context()); rc != nil {
			pattern = rc.RoutePattern()
		}
		required := requiredAPIKeyScope(r.Method, pattern)
		span.SetAttributes(
			attribute.String("api_key.id", grant.ID.String()),
			attribute.String("scope.route", r.Method+" "+pattern),
			attribute.String("scope.required", required),
		)

		if required == scopeNever {
			return denyScope(span, "forbidden_never", errors.New("this action is not available to API keys"))
		}
		if !scopes.Has(grant.Scopes, required) {
			return denyScope(span, "forbidden_scope", fmt.Errorf("api key is missing the %q scope", describeRequired(required)))
		}

		span.SetAttributes(attribute.String("scope.outcome", "ok"))
		return next.ServeHTTP(w, r.WithContext(spanCtx))
	})
}

func describeRequired(s scopes.Scope) string {
	if s == scopes.Full {
		return "full access"
	}
	return s
}

func denyScope(span trace.Span, outcome string, err error) error {
	span.SetAttributes(attribute.String("scope.outcome", outcome))
	return validate.NewRequestError(err, http.StatusForbidden)
}
