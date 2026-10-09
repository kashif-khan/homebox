package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/sysadminsmedia/homebox/backend/internal/core/scopes"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/reporting/eventbus"
	"github.com/sysadminsmedia/homebox/backend/internal/web/mid"
	"github.com/sysadminsmedia/homebox/backend/pkgs/hasher"
)

// testRouter mounts the real route table against the in-memory test app.
func testRouter(t *testing.T, a *app) *chi.Mux {
	t.Helper()
	a.bus = eventbus.New()
	router := chi.NewMux()
	a.mountRoutes(router, errchain.New(mid.Errors(zerolog.Nop())), a.repos)
	return router
}

func walkRoutes(t *testing.T, router *chi.Mux) map[string]struct{} {
	t.Helper()
	routes := map[string]struct{}{}
	require.NoError(t, chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes[method+" "+strings.TrimSuffix(route, "/")] = struct{}{}
		return nil
	}))
	return routes
}

// Every table entry must point at a route that exists, so a rename or removal
// can't leave a stale (and silently useless) classification behind.
func TestAPIKeyRouteScopesPointAtRealRoutes(t *testing.T) {
	a, _ := newAuthTestApp(t)
	routes := walkRoutes(t, testRouter(t, a))

	for key := range apiKeyRouteScopes {
		assert.Contains(t, routes, key, "scope table entry has no matching route")
	}
}

// A read-only key must not be able to reach any mutating route, and nothing but
// a full-access key may reach an unclassified one.
func TestAPIKeyReadOnlyCannotReachMutations(t *testing.T) {
	a, _ := newAuthTestApp(t)
	routes := walkRoutes(t, testRouter(t, a))
	readOnly, err := scopes.Preset(scopes.PresetReadOnly)
	require.NoError(t, err)

	var checked int
	for key := range routes {
		method, pattern, _ := strings.Cut(key, " ")
		if !strings.HasPrefix(pattern, "/api/v1/") {
			continue
		}
		required := requiredAPIKeyScope(method, pattern)
		allowed := required != scopeNever && scopes.Has(readOnly, required)
		if method != http.MethodGet {
			assert.False(t, allowed, "read-only key would be allowed to %s", key)
		}
		checked++
	}
	require.Positive(t, checked)
}

// Public routes carry no auth chain; everything under the authenticated chain
// that is not classified must demand full access.
func TestAPIKeyUnclassifiedRoutesRequireFull(t *testing.T) {
	assert.Equal(t, scopes.Full, requiredAPIKeyScope("POST", "/api/v1/actions/wipe-inventory"))
	assert.Equal(t, scopes.Full, requiredAPIKeyScope("GET", "/api/v1/does-not-exist-yet"))
	assert.Equal(t, scopeNever, requiredAPIKeyScope("POST", "/api/v1/users/self/api-keys"))
}

func TestAPIKeyScopeEnforcementEndToEnd(t *testing.T) {
	hasher.SetAPIKeyPepper([]byte("test-api-key-pepper"))
	a, _ := newAuthTestApp(t)
	router := testRouter(t, a)
	ctx := context.Background()

	usr, err := a.repos.Users.GetOneEmail(ctx, "auth-test@example.com")
	require.NoError(t, err)

	mint := func(t *testing.T, granted []string, pin *uuid.UUID) string {
		t.Helper()
		tok := hasher.GenerateAPIKeyCtx(ctx)
		_, err := a.repos.APIKeys.Create(ctx, usr.ID, "t-"+uuid.NewString(), tok.Hash, nil, granted, pin)
		require.NoError(t, err)
		return tok.Raw
	}
	do := func(token, method, path string, hdr map[string]string) int {
		req := httptest.NewRequest(method, path, strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	ro := mint(t, mustPreset(t, scopes.PresetReadOnly), nil)
	rw := mint(t, mustPreset(t, scopes.PresetReadWrite), nil)
	full := mint(t, []string{scopes.Full}, nil)

	t.Run("ReadOnlyCanRead", func(t *testing.T) {
		assert.Equal(t, http.StatusOK, do(ro, http.MethodGet, "/api/v1/entities", nil))
	})
	t.Run("ReadOnlyCannotWrite", func(t *testing.T) {
		assert.Equal(t, http.StatusForbidden, do(ro, http.MethodPost, "/api/v1/entities", nil))
	})
	t.Run("ReadWriteCannotDelete", func(t *testing.T) {
		assert.Equal(t, http.StatusForbidden, do(rw, http.MethodDelete, "/api/v1/entities/"+uuid.NewString(), nil))
	})
	t.Run("ScopedKeyCannotReachAdminRoutes", func(t *testing.T) {
		assert.Equal(t, http.StatusForbidden, do(rw, http.MethodPost, "/api/v1/actions/wipe-inventory", nil))
		assert.Equal(t, http.StatusForbidden, do(rw, http.MethodGet, "/api/v1/group/exports", nil))
	})
	t.Run("NoKeyCanManageKeys", func(t *testing.T) {
		for _, tok := range []string{ro, rw, full} {
			assert.Equal(t, http.StatusForbidden, do(tok, http.MethodPost, "/api/v1/users/self/api-keys", nil))
			assert.Equal(t, http.StatusForbidden, do(tok, http.MethodGet, "/api/v1/users/self/api-keys", nil))
		}
	})
	t.Run("FullKeyKeepsLegacyAccess", func(t *testing.T) {
		assert.Equal(t, http.StatusOK, do(full, http.MethodGet, "/api/v1/group/exports", nil))
	})

	t.Run("PinnedKeyIsConfinedToItsCollection", func(t *testing.T) {
		own := usr.DefaultGroupID
		other, err := a.repos.Groups.GroupCreate(ctx, "other", usr.ID)
		require.NoError(t, err)

		pinned := mint(t, mustPreset(t, scopes.PresetReadOnly), &own)
		assert.Equal(t, http.StatusOK, do(pinned, http.MethodGet, "/api/v1/entities", nil))
		assert.Equal(t, http.StatusOK, do(pinned, http.MethodGet, "/api/v1/entities", map[string]string{"X-Tenant": own.String()}))
		assert.Equal(t, http.StatusForbidden, do(pinned, http.MethodGet, "/api/v1/entities", map[string]string{"X-Tenant": other.ID.String()}))

		// Pinned to the second collection, the default tenant becomes that one.
		pinnedOther := mint(t, mustPreset(t, scopes.PresetReadOnly), &other.ID)
		assert.Equal(t, http.StatusOK, do(pinnedOther, http.MethodGet, "/api/v1/entities", nil))
		assert.Equal(t, http.StatusForbidden, do(pinnedOther, http.MethodGet, "/api/v1/entities", map[string]string{"X-Tenant": own.String()}))
	})
}

func mustPreset(t *testing.T, name string) []string {
	t.Helper()
	p, err := scopes.Preset(name)
	require.NoError(t, err)
	return p
}
