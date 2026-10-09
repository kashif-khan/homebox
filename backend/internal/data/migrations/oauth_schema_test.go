package migrations_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/apikey"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/oauthclient"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/oauthtoken"
)

// exerciseMCPSchema writes through ent into every table and column the MCP work
// added and checks the cascades that revocation depends on. It runs against a
// database built purely from the SQL migrations, so any drift between the
// migrations and the ent schema fails here, on both dialects.
func exerciseMCPSchema(t *testing.T, c *ent.Client) {
	t.Helper()
	ctx := context.Background()

	// groups.mcp_access defaults to off.
	g, err := c.Group.Create().SetName("mcp-g").Save(ctx)
	require.NoError(t, err)
	require.Equal(t, "off", g.McpAccess)
	g, err = c.Group.UpdateOneID(g.ID).SetMcpAccess("write").Save(ctx)
	require.NoError(t, err)
	require.Equal(t, "write", g.McpAccess)

	u, err := c.User.Create().SetName("u").SetEmail("mcp-" + uuid.NewString() + "@example.com").
		SetPassword("x").SetIsSuperuser(false).SetSuperuser(false).SetActivatedOn(time.Now()).Save(ctx)
	require.NoError(t, err)

	// api_keys.scopes / group_id
	k, err := c.APIKey.Create().SetUserID(u.ID).SetName("k").SetToken([]byte(uuid.NewString())).
		SetScopes([]string{scopeItemsRead, "items:write"}).SetGroupID(g.ID).Save(ctx)
	require.NoError(t, err)
	got, err := c.APIKey.Get(ctx, k.ID)
	require.NoError(t, err)
	require.Equal(t, []string{scopeItemsRead, "items:write"}, got.Scopes)
	require.Equal(t, g.ID, *got.GroupID)

	// oauth_*
	cl, err := c.OAuthClient.Create().SetClientID("hbc_" + uuid.NewString()).SetName("Claude").
		SetRedirectUris([]string{"https://claude.ai/cb"}).Save(ctx)
	require.NoError(t, err)

	_, err = c.OAuthRequest.Create().SetClientID(cl.ID).SetRedirectURI("https://claude.ai/cb").
		SetScopes([]string{scopeItemsRead}).SetState("s").SetCodeChallenge("c").SetExpiresAt(time.Now().Add(time.Minute)).Save(ctx)
	require.NoError(t, err)

	gr, err := c.OAuthGrant.Create().SetUserID(u.ID).SetGroupID(g.ID).SetClientID(cl.ID).
		SetScopes([]string{scopeItemsRead}).Save(ctx)
	require.NoError(t, err)
	for _, kind := range []oauthtoken.Kind{oauthtoken.KindCode, oauthtoken.KindAccess, oauthtoken.KindRefresh} {
		_, err = c.OAuthToken.Create().SetGrantID(gr.ID).SetKind(kind).SetTokenHash([]byte(uuid.NewString())).
			SetExpiresAt(time.Now().Add(time.Hour)).Save(ctx)
		require.NoError(t, err, string(kind))
	}
	dup := []byte("dup-hash")
	_, err = c.OAuthToken.Create().SetGrantID(gr.ID).SetKind("access").SetTokenHash(dup).SetExpiresAt(time.Now()).Save(ctx)
	require.NoError(t, err)
	_, err = c.OAuthToken.Create().SetGrantID(gr.ID).SetKind("access").SetTokenHash(dup).SetExpiresAt(time.Now()).Save(ctx)
	require.Error(t, err, "token hashes are unique")

	// Revoking a grant removes its tokens.
	require.NoError(t, c.OAuthGrant.DeleteOneID(gr.ID).Exec(ctx))
	n, err := c.OAuthToken.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, n)

	// Deleting a collection removes the grants and API keys pinned to it.
	gr2, err := c.OAuthGrant.Create().SetUserID(u.ID).SetGroupID(g.ID).SetClientID(cl.ID).SetScopes([]string{scopeItemsRead}).Save(ctx)
	require.NoError(t, err)
	require.NoError(t, c.Group.DeleteOneID(g.ID).Exec(ctx))
	exists, err := c.OAuthGrant.Query().Exist(ctx)
	require.NoError(t, err)
	require.False(t, exists, "grant %s survived its collection", gr2.ID)
	exists, err = c.APIKey.Query().Where(apikey.ID(k.ID)).Exist(ctx)
	require.NoError(t, err)
	require.False(t, exists)

	// Deleting a client removes its requests and grants.
	require.NoError(t, c.OAuthClient.DeleteOneID(cl.ID).Exec(ctx))
	n, err = c.OAuthRequest.Query().Count(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
	exists, err = c.OAuthClient.Query().Where(oauthclient.ID(cl.ID)).Exist(ctx)
	require.NoError(t, err)
	require.False(t, exists)
}
