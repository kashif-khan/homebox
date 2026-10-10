package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/sysadminsmedia/homebox/backend/internal/core/currencies"
	"github.com/sysadminsmedia/homebox/backend/internal/core/scopes"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services/reporting/eventbus"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
	_ "github.com/sysadminsmedia/homebox/backend/pkgs/cgofreesqlite"
	"github.com/sysadminsmedia/homebox/backend/pkgs/hasher"
)

// Tool argument names used throughout the tests.
const (
	argParentID = "parentId"
	argItemID   = "itemId"
	argConfirm  = "confirm"

	argDefaultLocationID = "defaultLocationId"
	argTemplateID        = "templateId"
)

type env struct {
	t     *testing.T
	repos *repo.AllRepos
	svc   *services.AllServices
	srv   *Server
	ts    *httptest.Server
	conf  config.MCPConf
}

func defaultConf() config.MCPConf {
	return config.MCPConf{
		Enabled: true, AllowWrites: true, AllowDelete: true,
		MaxPageSize: 50, MaxTextLength: 200, MaxResponseBytes: 256 * 1024,
	}
}

func newEnv(t *testing.T, conf config.MCPConf) *env {
	t.Helper()
	hasher.SetAPIKeyPepper([]byte("test-api-key-pepper"))

	client, err := ent.Open("sqlite3", "file:"+uuid.NewString()+"?mode=memory&cache=shared&_fk=1&_time_format=sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Schema.Create(context.Background()))

	bus := eventbus.New()
	go func() { _ = bus.Run(context.Background()) }()
	repos := repo.New(client, bus, config.Storage{PrefixPath: "/", ConnString: "file://" + os.TempDir()}, "mem://{{ .Topic }}", config.Thumbnail{}, nil)
	defaults, _ := currencies.CollectionCurrencies(currencies.CollectDefaults())
	svc := services.New(repos, services.WithCurrencies(defaults))

	e := &env{t: t, repos: repos, svc: svc, conf: conf}
	e.srv = New(Deps{Repos: repos, Services: svc, Conf: conf, Version: "test"})
	e.ts = httptest.NewServer(e.srv)
	t.Cleanup(e.ts.Close)
	return e
}

type tenant struct {
	user  repo.UserOut
	group repo.Group
}

// newTenant creates a user who owns a fresh collection.
func (e *env) newTenant(name string) tenant {
	e.t.Helper()
	ctx := context.Background()
	g, err := e.repos.Groups.GroupCreate(ctx, name+"-collection", uuid.Nil)
	require.NoError(e.t, err)
	pw := "x"
	u, err := e.repos.Users.Create(ctx, repo.UserCreate{
		Name: name, Email: name + "@example.com", Password: &pw, DefaultGroupID: g.ID, IsOwner: true,
	})
	require.NoError(e.t, err)
	return tenant{user: u, group: g}
}

// setAccess is what a collection owner does in the UI.
func (e *env) setAccess(tn tenant, level string) {
	e.t.Helper()
	_, err := e.svc.Group.UpdateGroup(
		services.Context{Context: context.Background(), UID: tn.user.ID, GID: tn.group.ID, User: &tn.user},
		repo.GroupUpdate{Name: tn.group.Name, Currency: "USD", MCPAccess: &level},
	)
	require.NoError(e.t, err)
}

func (e *env) key(tn tenant, granted []string, pin *uuid.UUID) string {
	e.t.Helper()
	tok := hasher.GenerateAPIKey()
	_, err := e.repos.APIKeys.Create(context.Background(), tn.user.ID, "test-"+uuid.NewString()[:8], tok.Hash, nil, granted, pin)
	require.NoError(e.t, err)
	return tok.Raw
}

type headerRT struct {
	token string
	extra map[string]string
}

func (h headerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if h.token != "" {
		r.Header.Set("Authorization", "Bearer "+h.token)
	}
	for k, v := range h.extra {
		r.Header.Set(k, v)
	}
	return http.DefaultTransport.RoundTrip(r)
}

func (e *env) connect(token string, extra map[string]string) (*mcp.ClientSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	e.t.Cleanup(cancel)
	c := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	return c.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             e.ts.URL,
		HTTPClient:           &http.Client{Transport: headerRT{token: token, extra: extra}},
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
}

func (e *env) session(token string) *mcp.ClientSession {
	e.t.Helper()
	cs, err := e.connect(token, nil)
	require.NoError(e.t, err)
	e.t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	require.NoError(t, err)
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	slices.Sort(names)
	return names
}

// callTool invokes a tool and decodes its structured output into v. It returns the
// tool-level error text, if any.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, v any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err, "protocol error calling %s", name)
	if res.IsError {
		require.NotEmpty(t, res.Content)
		return res.Content[0].(*mcp.TextContent).Text
	}
	if v != nil {
		b, err := json.Marshal(res.StructuredContent)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, v))
	}
	return ""
}

func httpStatus(t *testing.T, e *env, token, origin string) (int, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.ts.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode, resp.Header
}

// ---------------------------------------------------------------------------

func TestAuthentication(t *testing.T) {
	e := newEnv(t, defaultConf())
	tn := e.newTenant("alice")
	e.setAccess(tn, scopes.AccessRead)
	good := e.key(tn, []string{scopes.Full}, nil)

	t.Run("NoTokenIs401WithChallenge", func(t *testing.T) {
		code, hdr := httpStatus(t, e, "", "")
		assert.Equal(t, http.StatusUnauthorized, code)
		assert.Contains(t, hdr.Get("WWW-Authenticate"), "Bearer")
	})
	t.Run("UnknownKeyIs401", func(t *testing.T) {
		code, _ := httpStatus(t, e, "hb_definitely-not-a-key", "")
		assert.Equal(t, http.StatusUnauthorized, code)
	})
	t.Run("NonKeyTokenIs401", func(t *testing.T) {
		code, _ := httpStatus(t, e, "some-session-token", "")
		assert.Equal(t, http.StatusUnauthorized, code, "session tokens must not work on /mcp")
	})
	t.Run("ValidKeyIs200", func(t *testing.T) {
		code, _ := httpStatus(t, e, good, "")
		assert.Equal(t, http.StatusOK, code)
	})
	t.Run("BrowserOriginRejectedByDefault", func(t *testing.T) {
		code, _ := httpStatus(t, e, good, "https://evil.example")
		assert.Equal(t, http.StatusForbidden, code)
	})
	t.Run("ExpiredKeyIs401", func(t *testing.T) {
		tok := hasher.GenerateAPIKey()
		past := time.Now().Add(-time.Hour)
		_, err := e.repos.APIKeys.Create(context.Background(), tn.user.ID, "old", tok.Hash, &past, []string{scopes.Full}, nil)
		require.NoError(t, err)
		code, _ := httpStatus(t, e, tok.Raw, "")
		assert.Equal(t, http.StatusUnauthorized, code)
	})
}

func TestAllowedOrigin(t *testing.T) {
	conf := defaultConf()
	conf.AllowedOrigins = []string{"https://app.example/"}
	e := newEnv(t, conf)
	tn := e.newTenant("alice")
	e.setAccess(tn, scopes.AccessRead)
	k := e.key(tn, []string{scopes.Full}, nil)

	code, _ := httpStatus(t, e, k, "https://app.example")
	assert.Equal(t, http.StatusOK, code)
	code, _ = httpStatus(t, e, k, "https://other.example")
	assert.Equal(t, http.StatusForbidden, code)
}

func TestCollectionMustOptIn(t *testing.T) {
	e := newEnv(t, defaultConf())
	tn := e.newTenant("alice")
	k := e.key(tn, []string{scopes.Full}, nil)

	// Default is off: even a full-access key is refused, with an actionable reason.
	code, _ := httpStatus(t, e, k, "")
	assert.Equal(t, http.StatusForbidden, code)
	_, err := e.connect(k, nil)
	require.Error(t, err)

	e.setAccess(tn, scopes.AccessRead)
	code, _ = httpStatus(t, e, k, "")
	assert.Equal(t, http.StatusOK, code)

	// The owner can switch it back off and the very next request is refused.
	e.setAccess(tn, scopes.AccessOff)
	code, _ = httpStatus(t, e, k, "")
	assert.Equal(t, http.StatusForbidden, code)
}

func TestToolVisibilityFollowsEffectiveScopes(t *testing.T) {
	e := newEnv(t, defaultConf())
	tn := e.newTenant("alice")
	full := e.key(tn, []string{scopes.Full}, nil)
	ro := e.key(tn, mustPreset(t, scopes.PresetReadOnly), nil)
	rw := e.key(tn, mustPreset(t, scopes.PresetReadWrite), nil)

	writeTools := []string{"create_item", "update_item", "move_item", "create_location", "create_tag", "update_item_tags", "add_maintenance_entry"}
	deleteTools := []string{"delete_item", "delete_tag", "delete_maintenance_entry"}

	t.Run("ReadCeilingHidesEverythingMutating", func(t *testing.T) {
		e.setAccess(tn, scopes.AccessRead)
		for _, tok := range []string{full, rw} {
			names := toolNames(t, e.session(tok))
			assert.Contains(t, names, "search_items")
			for _, w := range append(writeTools, deleteTools...) {
				assert.NotContains(t, names, w, "%s must be hidden under a read ceiling", w)
			}
		}
	})
	t.Run("WriteCeilingHidesDelete", func(t *testing.T) {
		e.setAccess(tn, scopes.AccessWrite)
		names := toolNames(t, e.session(full))
		for _, w := range writeTools {
			assert.Contains(t, names, w)
		}
		for _, d := range deleteTools {
			assert.NotContains(t, names, d)
		}
	})
	t.Run("FullCeilingStillNeedsDeleteScopeOnTheKey", func(t *testing.T) {
		e.setAccess(tn, scopes.AccessFull)
		assert.Contains(t, toolNames(t, e.session(full)), "delete_item")
		assert.NotContains(t, toolNames(t, e.session(rw)), "delete_item", "read-write key has no items:delete")
		assert.NotContains(t, toolNames(t, e.session(ro)), "create_item")
	})
	t.Run("OperatorCapsBeatOwnerSettings", func(t *testing.T) {
		conf := defaultConf()
		conf.AllowWrites = false
		e2 := newEnv(t, conf)
		t2 := e2.newTenant("bob")
		e2.setAccess(t2, scopes.AccessFull)
		names := toolNames(t, e2.session(e2.key(t2, []string{scopes.Full}, nil)))
		assert.Contains(t, names, "search_items")
		assert.NotContains(t, names, "create_item")
		assert.NotContains(t, names, "delete_item")

		conf.AllowWrites, conf.AllowDelete = true, false
		e3 := newEnv(t, conf)
		t3 := e3.newTenant("carol")
		e3.setAccess(t3, scopes.AccessFull)
		names = toolNames(t, e3.session(e3.key(t3, []string{scopes.Full}, nil)))
		assert.Contains(t, names, "create_item")
		assert.NotContains(t, names, "delete_item")
		assert.NotContains(t, names, "delete_tag")
		assert.NotContains(t, names, "delete_maintenance_entry", "AllowDelete=false must remove every deleting tool")
	})
}

func TestToolAnnotations(t *testing.T) {
	e := newEnv(t, defaultConf())
	tn := e.newTenant("alice")
	e.setAccess(tn, scopes.AccessFull)
	cs := e.session(e.key(tn, []string{scopes.Full}, nil))

	res, err := cs.ListTools(context.Background(), nil)
	require.NoError(t, err)
	by := map[string]*mcp.Tool{}
	for _, tl := range res.Tools {
		by[tl.Name] = tl
	}
	assert.True(t, by["search_items"].Annotations.ReadOnlyHint)
	require.NotNil(t, by["delete_item"].Annotations.DestructiveHint)
	assert.True(t, *by["delete_item"].Annotations.DestructiveHint)
	assert.False(t, by["delete_item"].Annotations.ReadOnlyHint)
	require.NotNil(t, by["create_item"].Annotations.DestructiveHint)
	assert.False(t, *by["create_item"].Annotations.DestructiveHint)
	for name, tl := range by {
		require.NotNil(t, tl.Annotations.OpenWorldHint, name)
		assert.False(t, *tl.Annotations.OpenWorldHint, name)
	}
}

func TestInventoryLifecycle(t *testing.T) {
	e := newEnv(t, defaultConf())
	tn := e.newTenant("alice")
	e.setAccess(tn, scopes.AccessFull)
	cs := e.session(e.key(tn, []string{scopes.Full}, nil))

	var who WhoAmIOut
	require.Empty(t, callTool(t, cs, "whoami", nil, &who))
	assert.Equal(t, tn.group.ID.String(), who.CollectionID)
	assert.True(t, who.CanWrite)
	assert.True(t, who.CanDelete)

	// locations
	var garage, shelf ItemDetail
	require.Empty(t, callTool(t, cs, "create_location", map[string]any{"name": "Garage"}, &garage))
	require.Empty(t, callTool(t, cs, "create_location", map[string]any{"name": "Shelf A", argParentID: garage.ID}, &shelf))
	assert.Equal(t, "location", garage.Kind)
	require.NotNil(t, shelf.Parent)
	assert.Equal(t, garage.ID, shelf.Parent.ID)

	var roots ItemList
	require.Empty(t, callTool(t, cs, "list_locations", nil, &roots))
	require.Len(t, roots.Items, 1)
	assert.Equal(t, "Garage", roots.Items[0].Name)
	var inner ItemList
	require.Empty(t, callTool(t, cs, "list_locations", map[string]any{argParentID: garage.ID}, &inner))
	require.Len(t, inner.Items, 1)
	assert.Equal(t, "Shelf A", inner.Items[0].Name)

	// tags
	var tag TagInfo
	require.Empty(t, callTool(t, cs, "create_tag", map[string]any{"name": "tools", "color": "#ff0000"}, &tag))
	var tags ListTagsOut
	require.Empty(t, callTool(t, cs, "list_tags", nil, &tags))
	require.Len(t, tags.Tags, 1)

	// create + read item
	var drill ItemDetail
	require.Empty(t, callTool(t, cs, "create_item", map[string]any{
		"name": "Cordless drill", argParentID: garage.ID, "quantity": 2, "tagIds": []string{tag.ID}, "manufacturer": "Acme",
	}, &drill))
	assert.Equal(t, "item", drill.Kind)
	assert.InDelta(t, 2, drill.Quantity, 0.0001)
	require.Len(t, drill.Tags, 1)

	var found ItemList
	require.Empty(t, callTool(t, cs, "search_items", map[string]any{"query": "drill"}, &found))
	require.Len(t, found.Items, 1)
	assert.Equal(t, drill.ID, found.Items[0].ID)
	require.Empty(t, callTool(t, cs, "search_items", map[string]any{"tagIds": []string{tag.ID}}, &found))
	assert.Len(t, found.Items, 1)
	require.Empty(t, callTool(t, cs, "search_items", map[string]any{"query": "nonexistent-xyz"}, &found))
	assert.Empty(t, found.Items)

	// partial update keeps everything else
	var upd ItemDetail
	require.Empty(t, callTool(t, cs, "update_item", map[string]any{
		"id": drill.ID, "notes": "bought at a garage sale", "warrantyExpires": "2030-01-02", "purchasePrice": 49.5,
	}, &upd))
	assert.Equal(t, "Cordless drill", upd.Name)
	assert.Equal(t, "Acme", upd.Manufacturer)
	assert.InDelta(t, 2, upd.Quantity, 0.0001)
	assert.Equal(t, "bought at a garage sale", upd.Notes)
	assert.Equal(t, "2030-01-02", upd.WarrantyExpires)
	assert.InDelta(t, 49.5, upd.PurchasePrice, 0.001)
	assert.Len(t, upd.Tags, 1, "an update that doesn't mention tags must keep them")

	// move
	var moved ItemDetail
	require.Empty(t, callTool(t, cs, "move_item", map[string]any{"id": drill.ID, argParentID: shelf.ID}, &moved))
	require.NotNil(t, moved.Parent)
	assert.Equal(t, shelf.ID, moved.Parent.ID)
	assert.Contains(t, callTool(t, cs, "move_item", map[string]any{"id": drill.ID, argParentID: drill.ID}, nil), "itself")

	// tag add/remove
	var tag2 TagInfo
	require.Empty(t, callTool(t, cs, "create_tag", map[string]any{"name": "power", argParentID: tag.ID}, &tag2))
	var tagged ItemDetail
	require.Empty(t, callTool(t, cs, "update_item_tags", map[string]any{"id": drill.ID, "add": []string{tag2.ID}}, &tagged))
	assert.Len(t, tagged.Tags, 2)
	require.Empty(t, callTool(t, cs, "update_item_tags", map[string]any{"id": drill.ID, "remove": []string{tag.ID}}, &tagged))
	require.Len(t, tagged.Tags, 1)
	assert.Equal(t, tag2.ID, tagged.Tags[0].ID)

	var renamed TagInfo
	require.Empty(t, callTool(t, cs, "update_tag", map[string]any{"id": tag2.ID, "name": "power tools"}, &renamed))
	assert.Equal(t, "power tools", renamed.Name)
	assert.Equal(t, tag.ID, renamed.ParentID, "unmentioned fields are kept")

	// get_item on a location lists sub-locations; its items come from search_items
	var garageNow ItemDetail
	require.Empty(t, callTool(t, cs, "get_item", map[string]any{"id": garage.ID}, &garageNow))
	require.Len(t, garageNow.Children, 1)
	assert.Equal(t, shelf.ID, garageNow.Children[0].ID)
	var onShelf ItemList
	require.Empty(t, callTool(t, cs, "search_items", map[string]any{argParentID: shelf.ID}, &onShelf))
	require.Len(t, onShelf.Items, 1)
	assert.Equal(t, drill.ID, onShelf.Items[0].ID)

	// maintenance
	var m MaintenanceInfo
	soon := time.Now().UTC().AddDate(0, 0, 5).Format("2006-01-02")
	require.Empty(t, callTool(t, cs, "add_maintenance_entry", map[string]any{
		argItemID: drill.ID, "name": "Replace chuck", "scheduledDate": soon, "cost": 12.5,
	}, &m))
	var due MaintenanceOut
	require.Empty(t, callTool(t, cs, "list_maintenance", map[string]any{"dueWithinDays": 30}, &due))
	require.Len(t, due.Entries, 1)
	assert.Equal(t, "Replace chuck", due.Entries[0].Name)
	require.Empty(t, callTool(t, cs, "list_maintenance", map[string]any{"dueWithinDays": 2}, &due))
	assert.Empty(t, due.Entries, "entry due in 5 days is outside a 2-day window")

	var done MaintenanceInfo
	today := time.Now().UTC().Format("2006-01-02")
	require.Empty(t, callTool(t, cs, "update_maintenance_entry", map[string]any{argItemID: drill.ID, "id": m.ID, "completedDate": today}, &done))
	assert.Equal(t, today, done.CompletedDate)
	assert.Equal(t, "Replace chuck", done.Name)
	require.Empty(t, callTool(t, cs, "list_maintenance", map[string]any{"dueWithinDays": 30}, &due))
	assert.Empty(t, due.Entries, "completed entries are no longer due")
	assert.Contains(t, callTool(t, cs, "add_maintenance_entry", map[string]any{argItemID: drill.ID, "name": "x"}, nil), "completedDate or scheduledDate")

	// warranties
	var w WarrantyOut
	require.Empty(t, callTool(t, cs, "list_warranties_expiring", map[string]any{"days": 30}, &w))
	assert.Empty(t, w.Items)
	soonWarranty := time.Now().UTC().AddDate(0, 0, 10).Format("2006-01-02")
	require.Empty(t, callTool(t, cs, "update_item", map[string]any{"id": drill.ID, "warrantyExpires": soonWarranty}, nil))
	require.Empty(t, callTool(t, cs, "list_warranties_expiring", map[string]any{"days": 30}, &w))
	require.Len(t, w.Items, 1)
	assert.Equal(t, drill.ID, w.Items[0].Item.ID)
	assert.InDelta(t, 10, w.Items[0].DaysLeft, 1)

	// stats + types
	var st StatsOut
	require.Empty(t, callTool(t, cs, "get_statistics", nil, &st))
	assert.Equal(t, 1, st.TotalItems)
	assert.Equal(t, 2, st.TotalLocations)
	var types ListEntityTypesOut
	require.Empty(t, callTool(t, cs, "list_entity_types", nil, &types))
	assert.NotEmpty(t, types.Types)

	// destructive tools demand explicit confirmation
	msg := callTool(t, cs, "delete_item", map[string]any{"id": drill.ID}, nil)
	assert.Contains(t, msg, argConfirm, "omitting confirm is rejected by the input schema")
	msg = callTool(t, cs, "delete_item", map[string]any{"id": drill.ID, argConfirm: false}, nil)
	assert.Contains(t, msg, "confirm=true")
	require.Empty(t, callTool(t, cs, "get_item", map[string]any{"id": drill.ID}, nil), "unconfirmed delete must not delete")

	require.Empty(t, callTool(t, cs, "delete_maintenance_entry", map[string]any{"id": m.ID, argConfirm: true}, nil))
	require.Empty(t, callTool(t, cs, "delete_item", map[string]any{"id": drill.ID, argConfirm: true}, nil))
	assert.Equal(t, "not found", callTool(t, cs, "get_item", map[string]any{"id": drill.ID}, nil))
	require.Empty(t, callTool(t, cs, "delete_tag", map[string]any{"id": tag2.ID, argConfirm: true}, nil))
	require.Empty(t, callTool(t, cs, "list_tags", nil, &tags))
	assert.Len(t, tags.Tags, 1)
}

func TestInputValidation(t *testing.T) {
	e := newEnv(t, defaultConf())
	tn := e.newTenant("alice")
	e.setAccess(tn, scopes.AccessFull)
	cs := e.session(e.key(tn, []string{scopes.Full}, nil))

	assert.Contains(t, callTool(t, cs, "get_item", map[string]any{"id": "not-a-uuid"}, nil), "UUID")
	assert.Contains(t, callTool(t, cs, "create_item", map[string]any{"name": "   "}, nil), "name")
	assert.Contains(t, callTool(t, cs, "create_item", map[string]any{"name": strings.Repeat("a", 256)}, nil), "name")
	assert.Contains(t, callTool(t, cs, "create_item", map[string]any{"name": "ok", argParentID: "nope"}, nil), "UUID")
	assert.Contains(t, callTool(t, cs, "update_item", map[string]any{"id": uuid.NewString(), "name": "x"}, nil), "not found")
	assert.Contains(t, callTool(t, cs, "search_items", map[string]any{"kind": "bogus"}, nil), "kind")
	assert.Contains(t, callTool(t, cs, "search_items", map[string]any{"orderBy": "price; drop table"}, nil), "orderBy")
	assert.Contains(t, callTool(t, cs, "add_maintenance_entry", map[string]any{argItemID: uuid.NewString(), "name": "x", "scheduledDate": "01/02/2030"}, nil), "YYYY-MM-DD")
}

func TestPaginationAndTruncation(t *testing.T) {
	conf := defaultConf()
	conf.MaxPageSize = 3
	conf.MaxTextLength = 50
	e := newEnv(t, conf)
	tn := e.newTenant("alice")
	e.setAccess(tn, scopes.AccessFull)
	cs := e.session(e.key(tn, []string{scopes.Full}, nil))

	for i := 0; i < 7; i++ {
		require.Empty(t, callTool(t, cs, "create_item", map[string]any{"name": "widget " + string(rune('a'+i))}, nil))
	}

	var p1, p3 ItemList
	require.Empty(t, callTool(t, cs, "search_items", map[string]any{"pageSize": 1000, "orderBy": "name"}, &p1))
	assert.Len(t, p1.Items, 3, "page size is capped by the server")
	assert.Equal(t, 3, p1.PageSize)
	assert.Equal(t, 7, p1.Total)
	assert.True(t, p1.HasMore)
	require.Empty(t, callTool(t, cs, "search_items", map[string]any{"page": 3, "pageSize": 3, "orderBy": "name"}, &p3))
	assert.Len(t, p3.Items, 1)
	assert.False(t, p3.HasMore)

	var big ItemDetail
	require.Empty(t, callTool(t, cs, "create_item", map[string]any{"name": "novel", "description": strings.Repeat("é", 300)}, &big))
	assert.Contains(t, big.Description, "truncated")
	assert.Less(t, len([]rune(big.Description)), 120, "long text is clipped (rune-safely) with a marker")
}

func TestResultSizeCap(t *testing.T) {
	conf := defaultConf()
	conf.MaxTextLength = 0
	conf.MaxResponseBytes = 3500
	e := newEnv(t, conf)
	tn := e.newTenant("alice")
	e.setAccess(tn, scopes.AccessFull)
	cs := e.session(e.key(tn, []string{scopes.Full}, nil))
	for i := 0; i < 5; i++ {
		require.Empty(t, callTool(t, cs, "create_item", map[string]any{
			"name": strings.Repeat("n", 250) + string(rune('a'+i)), "description": strings.Repeat("d", 900),
		}, nil), "a single item fits under the cap")
	}
	assert.Contains(t, callTool(t, cs, "search_items", nil, nil), "too large")
}

// The core multi-tenancy guarantee: nothing one user holds is reachable by
// another, by id, by search or by listing.
func TestTenantIsolation(t *testing.T) {
	e := newEnv(t, defaultConf())
	alice, bob := e.newTenant("alice"), e.newTenant("bob")
	e.setAccess(alice, scopes.AccessFull)
	e.setAccess(bob, scopes.AccessFull)
	a := e.session(e.key(alice, []string{scopes.Full}, nil))
	b := e.session(e.key(bob, []string{scopes.Full}, nil))

	var secretLoc, secret ItemDetail
	require.Empty(t, callTool(t, a, "create_location", map[string]any{"name": "Alice vault"}, &secretLoc))
	require.Empty(t, callTool(t, a, "create_item", map[string]any{"name": "Alice's passport", argParentID: secretLoc.ID}, &secret))
	var aTag TagInfo
	require.Empty(t, callTool(t, a, "create_tag", map[string]any{"name": "alice-private"}, &aTag))
	var aMaint MaintenanceInfo
	require.Empty(t, callTool(t, a, "add_maintenance_entry", map[string]any{argItemID: secret.ID, "name": "renew", "scheduledDate": "2031-01-01"}, &aMaint))

	// reads
	assert.Equal(t, "not found", callTool(t, b, "get_item", map[string]any{"id": secret.ID}, nil))
	var res ItemList
	require.Empty(t, callTool(t, b, "search_items", map[string]any{"query": "passport", "kind": "any"}, &res))
	assert.Empty(t, res.Items)
	require.Empty(t, callTool(t, b, "list_locations", nil, &res))
	assert.Empty(t, res.Items)
	var tags ListTagsOut
	require.Empty(t, callTool(t, b, "list_tags", nil, &tags))
	assert.Empty(t, tags.Tags)
	var m MaintenanceOut
	require.Empty(t, callTool(t, b, "list_maintenance", map[string]any{"status": "both"}, &m))
	assert.Empty(t, m.Entries)
	require.Empty(t, callTool(t, b, "list_maintenance", map[string]any{argItemID: secret.ID, "status": "both"}, &m))
	assert.Empty(t, m.Entries)

	// writes by id must fail and change nothing
	assert.NotEmpty(t, callTool(t, b, "update_item", map[string]any{"id": secret.ID, "name": "pwned"}, nil))
	assert.NotEmpty(t, callTool(t, b, "move_item", map[string]any{"id": secret.ID, argParentID: secretLoc.ID}, nil))
	assert.NotEmpty(t, callTool(t, b, "delete_item", map[string]any{"id": secret.ID, argConfirm: true}, nil))
	assert.NotEmpty(t, callTool(t, b, "delete_tag", map[string]any{"id": aTag.ID, argConfirm: true}, nil))
	assert.NotEmpty(t, callTool(t, b, "delete_maintenance_entry", map[string]any{"id": aMaint.ID, argConfirm: true}, nil))
	assert.NotEmpty(t, callTool(t, b, "add_maintenance_entry", map[string]any{argItemID: secret.ID, "name": "x", "scheduledDate": "2031-01-01"}, nil))
	assert.NotEmpty(t, callTool(t, b, "update_maintenance_entry", map[string]any{argItemID: secret.ID, "id": aMaint.ID, "name": "x"}, nil))

	// cross-references: Bob can't hang his item off Alice's location, or tag with Alice's tag
	assert.NotEmpty(t, callTool(t, b, "create_item", map[string]any{"name": "sneaky", argParentID: secretLoc.ID}, nil))
	assert.NotEmpty(t, callTool(t, b, "create_item", map[string]any{"name": "sneaky2", "tagIds": []string{aTag.ID}}, nil))
	var mine ItemDetail
	require.Empty(t, callTool(t, b, "create_item", map[string]any{"name": "bob item"}, &mine))
	assert.NotEmpty(t, callTool(t, b, "move_item", map[string]any{"id": mine.ID, argParentID: secretLoc.ID}, nil))
	assert.NotEmpty(t, callTool(t, b, "update_item_tags", map[string]any{"id": mine.ID, "add": []string{aTag.ID}}, nil))

	// Alice's data is untouched
	var aTags ListTagsOut
	require.Empty(t, callTool(t, a, "list_tags", nil, &aTags))
	assert.Len(t, aTags.Tags, 1, "Bob's delete_tag must not have removed Alice's tag")
	var still ItemDetail
	require.Empty(t, callTool(t, a, "get_item", map[string]any{"id": secret.ID}, &still))
	assert.Equal(t, "Alice's passport", still.Name)

	// resources honour the same isolation
	_, err := b.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "homebox://item/" + secret.ID})
	require.Error(t, err)

	// Bob can't escape to Alice's collection with a header, either.
	cs, err := e.connect(e.key(bob, []string{scopes.Full}, nil), map[string]string{"X-Tenant": alice.group.ID.String()})
	if err == nil {
		_ = cs.Close()
	}
	require.Error(t, err)
}

func TestCollectionPinAndTenantSwitch(t *testing.T) {
	e := newEnv(t, defaultConf())
	tn := e.newTenant("alice")
	second, err := e.repos.Groups.GroupCreate(context.Background(), "second", tn.user.ID)
	require.NoError(t, err)
	e.setAccess(tn, scopes.AccessFull)
	_, err = e.svc.Group.UpdateGroup(
		services.Context{Context: context.Background(), UID: tn.user.ID, GID: second.ID, User: &tn.user},
		repo.GroupUpdate{Name: "second", Currency: "USD", MCPAccess: ptr(scopes.AccessFull)},
	)
	require.NoError(t, err)
	// the user record was loaded before joining "second"; refresh group membership
	tn.user, err = e.repos.Users.GetOneID(context.Background(), tn.user.ID)
	require.NoError(t, err)

	own := tn.group.ID
	pinned := e.session(e.key(tn, []string{scopes.Full}, &own))
	var who WhoAmIOut
	require.Empty(t, callTool(t, pinned, "whoami", nil, &who))
	assert.Equal(t, own.String(), who.CollectionID)

	_, err = e.connect(e.key(tn, []string{scopes.Full}, &own), map[string]string{"X-Tenant": second.ID.String()})
	require.Error(t, err, "a pinned key must not switch collection")

	// an unpinned key can choose among the user's collections, but only those
	cs, err := e.connect(e.key(tn, []string{scopes.Full}, nil), map[string]string{"X-Tenant": second.ID.String()})
	require.NoError(t, err)
	defer func() { _ = cs.Close() }()
	require.Empty(t, callTool(t, cs, "whoami", nil, &who))
	assert.Equal(t, second.ID.String(), who.CollectionID)

	_, err = e.connect(e.key(tn, []string{scopes.Full}, nil), map[string]string{"X-Tenant": uuid.NewString()})
	require.Error(t, err)
}

func TestResources(t *testing.T) {
	e := newEnv(t, defaultConf())
	tn := e.newTenant("alice")
	e.setAccess(tn, scopes.AccessFull)
	cs := e.session(e.key(tn, []string{scopes.Full}, nil))

	var loc ItemDetail
	require.Empty(t, callTool(t, cs, "create_location", map[string]any{"name": "Attic"}, &loc))
	require.Empty(t, callTool(t, cs, "create_tag", map[string]any{"name": "seasonal"}, nil))

	read := func(uri string) string {
		res, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri})
		require.NoError(t, err)
		require.Len(t, res.Contents, 1)
		return res.Contents[0].Text
	}
	assert.Contains(t, read("homebox://locations"), "Attic")
	assert.Contains(t, read("homebox://tags"), "seasonal")
	assert.Contains(t, read("homebox://item/"+loc.ID), "Attic")

	_, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "homebox://item/" + uuid.NewString()})
	require.Error(t, err)
}

func TestRateLimit(t *testing.T) {
	conf := defaultConf()
	conf.RateLimitPerMinute = 3
	e := newEnv(t, conf)
	tn := e.newTenant("alice")
	e.setAccess(tn, scopes.AccessRead)
	k := e.key(tn, []string{scopes.Full}, nil)
	other := e.key(tn, []string{scopes.Full}, nil)

	for i := 0; i < 3; i++ {
		code, _ := httpStatus(t, e, k, "")
		require.Equal(t, http.StatusOK, code)
	}
	code, hdr := httpStatus(t, e, k, "")
	assert.Equal(t, http.StatusTooManyRequests, code)
	assert.NotEmpty(t, hdr.Get("Retry-After"))

	code, _ = httpStatus(t, e, other, "")
	assert.Equal(t, http.StatusOK, code, "limits are per credential")
}

func TestLimiterWindow(t *testing.T) {
	l := newLimiter(2)
	now := time.Unix(1000, 0)
	l.now = func() time.Time { return now }
	id := uuid.New()
	ok, _ := l.allow(id)
	assert.True(t, ok)
	ok, _ = l.allow(id)
	assert.True(t, ok)
	ok, retry := l.allow(id)
	assert.False(t, ok)
	assert.Positive(t, retry)
	now = now.Add(61 * time.Second)
	ok, _ = l.allow(id)
	assert.True(t, ok, "window resets")
	assert.Nil(t, newLimiter(0), "zero disables limiting")
	ok, _ = (*limiter)(nil).allow(id)
	assert.True(t, ok)
}

func TestClipIsRuneSafe(t *testing.T) {
	assert.Equal(t, "short", clip("short", 10))
	got := clip(strings.Repeat("日", 20), 5)
	assert.True(t, strings.HasPrefix(got, "日日日日日…"))
	assert.Equal(t, "whatever", clip("whatever", 0), "zero means unlimited")
}

func mustPreset(t *testing.T, name string) []string {
	t.Helper()
	p, err := scopes.Preset(name)
	require.NoError(t, err)
	return p
}

func ptr[T any](v T) *T { return &v }

func TestTemplateLifecycle(t *testing.T) {
	e := newEnv(t, defaultConf())
	tn := e.newTenant("alice")
	e.setAccess(tn, scopes.AccessFull)
	cs := e.session(e.key(tn, []string{scopes.Full}, nil))

	var tag TagInfo
	require.Empty(t, callTool(t, cs, "create_tag", map[string]any{"name": "tools"}, &tag))
	var shed ItemDetail
	require.Empty(t, callTool(t, cs, "create_location", map[string]any{"name": "Shed"}, &shed))

	var tpl TemplateDetail
	require.Empty(t, callTool(t, cs, "create_template", map[string]any{
		"name": "Power tool", "description": "Corded and cordless tools",
		"defaultName": "Drill", "defaultQuantity": 1, "defaultManufacturer": "Acme",
		"defaultWarrantyDetails": "2 years, receipt needed", "defaultInsured": true,
		argDefaultLocationID: shed.ID, "defaultTagIds": []string{tag.ID},
		"customFields": []map[string]any{{"name": "Voltage", "textValue": "18V"}, {"name": "Batteries", "type": "number"}},
	}, &tpl))
	assert.Equal(t, "Power tool", tpl.Name)
	assert.Equal(t, "Acme", tpl.DefaultManufacturer)
	assert.True(t, tpl.DefaultInsured)
	require.NotNil(t, tpl.DefaultLocation)
	assert.Equal(t, shed.ID, tpl.DefaultLocation.ID)
	require.Len(t, tpl.DefaultTags, 1)
	require.Len(t, tpl.CustomFields, 2)
	assert.Equal(t, "text", tpl.CustomFields[0].Type, "type defaults to text")

	var list ListTemplatesOut
	require.Empty(t, callTool(t, cs, "list_templates", nil, &list))
	require.Len(t, list.Templates, 1)
	var got TemplateDetail
	require.Empty(t, callTool(t, cs, "get_template", map[string]any{"id": tpl.ID}, &got))
	assert.Equal(t, tpl.ID, got.ID)

	// a partial update keeps everything it doesn't mention
	var upd TemplateDetail
	require.Empty(t, callTool(t, cs, "update_template", map[string]any{"id": tpl.ID, "defaultManufacturer": "Bosch", "defaultQuantity": 3}, &upd))
	assert.Equal(t, "Bosch", upd.DefaultManufacturer)
	assert.InDelta(t, 3, upd.DefaultQuantity, 0.0001)
	assert.Equal(t, "Power tool", upd.Name)
	assert.Equal(t, "Drill", upd.DefaultName)
	assert.Equal(t, "2 years, receipt needed", upd.DefaultWarrantyDetails)
	assert.True(t, upd.DefaultInsured)
	assert.Len(t, upd.DefaultTags, 1)
	assert.Len(t, upd.CustomFields, 2)
	require.NotNil(t, upd.DefaultLocation)

	// explicitly clearing and replacing (fresh value: omitted fields must not linger)
	var cleared TemplateDetail
	require.Empty(t, callTool(t, cs, "update_template", map[string]any{
		"id": tpl.ID, argDefaultLocationID: "", "defaultTagIds": []string{},
		"customFields": []map[string]any{{"name": "Serial"}},
	}, &cleared))
	assert.Nil(t, cleared.DefaultLocation)
	assert.Empty(t, cleared.DefaultTags)
	require.Len(t, cleared.CustomFields, 1)
	assert.Equal(t, "Serial", cleared.CustomFields[0].Name)

	// items from a template get its defaults and can override quantity
	var item ItemDetail
	require.Empty(t, callTool(t, cs, "create_item_from_template", map[string]any{
		argTemplateID: tpl.ID, "name": "Garage drill", argParentID: shed.ID, "quantity": 2,
	}, &item))
	assert.Equal(t, "Garage drill", item.Name)
	assert.Equal(t, "Bosch", item.Manufacturer)
	assert.True(t, item.Insured)
	assert.Equal(t, "2 years, receipt needed", item.WarrantyDetails)
	assert.InDelta(t, 2, item.Quantity, 0.0001)
	require.NotNil(t, item.Parent)
	assert.Equal(t, shed.ID, item.Parent.ID)
	require.Len(t, item.Fields, 1)
	assert.Equal(t, "Serial", item.Fields[0].Name)

	// deleting needs confirmation and leaves existing items alone
	assert.Contains(t, callTool(t, cs, "delete_template", map[string]any{"id": tpl.ID, argConfirm: false}, nil), "confirm=true")
	require.Empty(t, callTool(t, cs, "get_template", map[string]any{"id": tpl.ID}, nil))
	require.Empty(t, callTool(t, cs, "delete_template", map[string]any{"id": tpl.ID, argConfirm: true}, nil))
	assert.Equal(t, "not found", callTool(t, cs, "get_template", map[string]any{"id": tpl.ID}, nil))
	require.Empty(t, callTool(t, cs, "get_item", map[string]any{"id": item.ID}, nil))
}

func TestTemplateToolPermissions(t *testing.T) {
	e := newEnv(t, defaultConf())
	tn := e.newTenant("alice")
	full := e.key(tn, []string{scopes.Full}, nil)

	e.setAccess(tn, scopes.AccessRead)
	names := toolNames(t, e.session(full))
	assert.Contains(t, names, "list_templates")
	assert.Contains(t, names, "get_template")
	for _, w := range []string{"create_template", "update_template", "create_item_from_template", "delete_template"} {
		assert.NotContains(t, names, w, w)
	}
	e.setAccess(tn, scopes.AccessWrite)
	names = toolNames(t, e.session(full))
	assert.Contains(t, names, "create_template")
	assert.Contains(t, names, "create_item_from_template")
	assert.NotContains(t, names, "delete_template")
	e.setAccess(tn, scopes.AccessFull)
	assert.Contains(t, toolNames(t, e.session(full)), "delete_template")
}

func TestTemplateValidation(t *testing.T) {
	e := newEnv(t, defaultConf())
	tn := e.newTenant("alice")
	e.setAccess(tn, scopes.AccessFull)
	cs := e.session(e.key(tn, []string{scopes.Full}, nil))

	assert.Contains(t, callTool(t, cs, "create_template", map[string]any{"name": "  "}, nil), "name")
	assert.Contains(t, callTool(t, cs, "create_template", map[string]any{"name": "x", "customFields": []map[string]any{{"name": "f", "type": "color"}}}, nil), "type")
	assert.Contains(t, callTool(t, cs, "create_template", map[string]any{"name": "x", "customFields": []map[string]any{{"name": " "}}}, nil), "field names")
	assert.Contains(t, callTool(t, cs, "create_template", map[string]any{"name": "x", argDefaultLocationID: "nope"}, nil), "UUID")
	assert.Contains(t, callTool(t, cs, "create_item_from_template", map[string]any{argTemplateID: uuid.NewString(), "name": "n", argParentID: uuid.NewString()}, nil), "not found")
	var tpl TemplateDetail
	require.Empty(t, callTool(t, cs, "create_template", map[string]any{"name": "ok"}, &tpl))
	assert.Contains(t, callTool(t, cs, "create_item_from_template", map[string]any{argTemplateID: tpl.ID, "name": "n"}, nil), "parentId")
}

func TestTemplateTenantIsolation(t *testing.T) {
	e := newEnv(t, defaultConf())
	alice, bob := e.newTenant("alice"), e.newTenant("bob")
	e.setAccess(alice, scopes.AccessFull)
	e.setAccess(bob, scopes.AccessFull)
	a := e.session(e.key(alice, []string{scopes.Full}, nil))
	b := e.session(e.key(bob, []string{scopes.Full}, nil))

	var aTag TagInfo
	require.Empty(t, callTool(t, a, "create_tag", map[string]any{"name": "alice-secret-tag"}, &aTag))
	var aLoc ItemDetail
	require.Empty(t, callTool(t, a, "create_location", map[string]any{"name": "Alice vault"}, &aLoc))
	var aTpl TemplateDetail
	require.Empty(t, callTool(t, a, "create_template", map[string]any{"name": "Alice template", "defaultManufacturer": "Secret Co"}, &aTpl))

	var list ListTemplatesOut
	require.Empty(t, callTool(t, b, "list_templates", nil, &list))
	assert.Empty(t, list.Templates)
	assert.Equal(t, "not found", callTool(t, b, "get_template", map[string]any{"id": aTpl.ID}, nil))
	assert.NotEmpty(t, callTool(t, b, "update_template", map[string]any{"id": aTpl.ID, "name": "pwned"}, nil))
	assert.NotEmpty(t, callTool(t, b, "delete_template", map[string]any{"id": aTpl.ID, argConfirm: true}, nil))

	// Bob can't reference Alice's tag or location in his own template…
	assert.NotEmpty(t, callTool(t, b, "create_template", map[string]any{"name": "t", "defaultTagIds": []string{aTag.ID}}, nil))
	assert.NotEmpty(t, callTool(t, b, "create_template", map[string]any{"name": "t", argDefaultLocationID: aLoc.ID}, nil))
	var bTpl TemplateDetail
	require.Empty(t, callTool(t, b, "create_template", map[string]any{"name": "Bob template"}, &bTpl))
	assert.NotEmpty(t, callTool(t, b, "update_template", map[string]any{"id": bTpl.ID, "defaultTagIds": []string{aTag.ID}}, nil))
	assert.NotEmpty(t, callTool(t, b, "update_template", map[string]any{"id": bTpl.ID, argDefaultLocationID: aLoc.ID}, nil))

	// …nor use Alice's template, or put an item in Alice's location.
	assert.NotEmpty(t, callTool(t, b, "create_item_from_template", map[string]any{argTemplateID: aTpl.ID, "name": "n", argParentID: uuid.NewString()}, nil))
	assert.NotEmpty(t, callTool(t, b, "create_item_from_template", map[string]any{argTemplateID: bTpl.ID, "name": "n", argParentID: aLoc.ID}, nil))

	var still TemplateDetail
	require.Empty(t, callTool(t, a, "get_template", map[string]any{"id": aTpl.ID}, &still))
	assert.Equal(t, "Alice template", still.Name)
	assert.Equal(t, "Secret Co", still.DefaultManufacturer)
}
