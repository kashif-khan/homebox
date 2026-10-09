package services

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/sysadminsmedia/homebox/backend/internal/core/scopes"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
	"github.com/sysadminsmedia/homebox/backend/pkgs/hasher"
)

func TestCreateAPIKey_ScopeResolution(t *testing.T) {
	ctx := context.Background()
	usr := newTestUserWithPassword(t, "api-key-scopes")

	t.Run("DefaultsToReadOnly", func(t *testing.T) {
		out, err := tSvc.User.CreateAPIKey(ctx, usr.ID, repo.APIKeyCreate{Name: "default"})
		require.NoError(t, err)
		assert.True(t, scopes.ReadOnly(out.Scopes))
		assert.False(t, scopes.Has(out.Scopes, scopes.Full))
	})

	t.Run("PresetExpands", func(t *testing.T) {
		out, err := tSvc.User.CreateAPIKey(ctx, usr.ID, repo.APIKeyCreate{Name: "rw", Preset: scopes.PresetReadWrite})
		require.NoError(t, err)
		assert.True(t, scopes.Has(out.Scopes, scopes.ItemsWrite))
		assert.False(t, scopes.Has(out.Scopes, scopes.ItemsDelete))
	})

	t.Run("ExplicitScopesWinOverPreset", func(t *testing.T) {
		out, err := tSvc.User.CreateAPIKey(ctx, usr.ID, repo.APIKeyCreate{
			Name: "explicit", Preset: scopes.PresetFull, Scopes: []string{scopes.ItemsRead},
		})
		require.NoError(t, err)
		assert.Equal(t, []string{scopes.ItemsRead}, out.Scopes)
	})

	t.Run("UnknownScopeRejected", func(t *testing.T) {
		_, err := tSvc.User.CreateAPIKey(ctx, usr.ID, repo.APIKeyCreate{Name: "bad", Scopes: []string{"root:everything"}})
		var reqErr *validate.RequestError
		require.ErrorAs(t, err, &reqErr)
		assert.Equal(t, http.StatusUnprocessableEntity, reqErr.Status)
	})

	t.Run("ScopesSurviveRoundTrip", func(t *testing.T) {
		out, err := tSvc.User.CreateAPIKey(ctx, usr.ID, repo.APIKeyCreate{Name: "rt", Scopes: []string{scopes.MaintenanceWrite, scopes.ItemsRead}})
		require.NoError(t, err)
		_, grant, err := tRepos.APIKeys.GetGrantFromToken(ctx, hasher.HashAPIKey(out.Token))
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{scopes.ItemsRead, scopes.MaintenanceWrite}, grant.Scopes)
	})
}

func TestCreateAPIKey_GroupPinRequiresMembership(t *testing.T) {
	ctx := context.Background()
	usr := newTestUserWithPassword(t, "api-key-pin")

	foreign, err := tRepos.Groups.GroupCreate(ctx, "someone-elses", uuid.Nil)
	require.NoError(t, err)

	_, err = tSvc.User.CreateAPIKey(ctx, usr.ID, repo.APIKeyCreate{Name: "steal", GroupID: &foreign.ID})
	var reqErr *validate.RequestError
	require.ErrorAs(t, err, &reqErr, "pinning to a collection the user isn't in must fail")
	assert.Equal(t, http.StatusNotFound, reqErr.Status)

	own := usr.DefaultGroupID
	out, err := tSvc.User.CreateAPIKey(ctx, usr.ID, repo.APIKeyCreate{Name: "mine", GroupID: &own})
	require.NoError(t, err)
	require.NotNil(t, out.GroupID)
	assert.Equal(t, own, *out.GroupID)
}
