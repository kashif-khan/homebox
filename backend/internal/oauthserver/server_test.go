package oauthserver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
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
	"github.com/sysadminsmedia/homebox/backend/internal/mcpserver"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
	_ "github.com/sysadminsmedia/homebox/backend/pkgs/cgofreesqlite"
	"github.com/sysadminsmedia/homebox/backend/pkgs/hasher"
)

// Request field names used throughout the tests.
const (
	keyClientName = "client_name"
	keyClientID   = "client_id"
	keyToken      = "token"

	keyRedirectURIs = "redirect_uris"
	keyRedirectURI  = "redirect_uri"
)

type env struct {
	t     *testing.T
	repos *repo.AllRepos
	svc   *services.AllServices
	oauth *Server
	ts    *httptest.Server
	base  string
	user  repo.UserOut
	group repo.Group
	now   time.Time
}

const redirect = "https://assistant.example/callback"

func newEnv(t *testing.T) *env {
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

	e := &env{t: t, repos: repos, svc: svc, now: time.Now()}

	mux := chi.NewMux()
	e.ts = httptest.NewServer(mux)
	t.Cleanup(e.ts.Close)
	e.base = e.ts.URL

	conf := config.MCPOAuthConf{Enabled: true, AllowDynamicRegistration: true, AccessTokenTTL: time.Hour, RefreshTokenTTL: 24 * time.Hour}
	e.oauth = New(repos, conf, func(*http.Request) string { return e.base }, nil)
	e.oauth.now = func() time.Time { return e.now }
	e.oauth.Mount(mux)
	mux.Handle("/mcp", mcpserver.New(mcpserver.Deps{
		Repos: repos, Services: svc, OAuth: e.oauth, BaseURL: func(*http.Request) string { return e.base }, Version: "test",
		Conf: config.MCPConf{Enabled: true, AllowWrites: true, AllowDelete: true, MaxPageSize: 50, MaxTextLength: 500, MaxResponseBytes: 1 << 20},
	}))

	ctx := context.Background()
	g, err := repos.Groups.GroupCreate(ctx, "home", uuid.Nil)
	require.NoError(t, err)
	pw := "x"
	e.user, err = repos.Users.Create(ctx, repo.UserCreate{Name: "alice", Email: "alice@example.com", Password: &pw, DefaultGroupID: g.ID, IsOwner: true})
	require.NoError(t, err)
	e.group = g
	e.setAccess(scopes.AccessFull)
	return e
}

func (e *env) setAccess(level string) {
	e.t.Helper()
	_, err := e.svc.Group.UpdateGroup(
		services.Context{Context: context.Background(), UID: e.user.ID, GID: e.group.ID, User: &e.user},
		repo.GroupUpdate{Name: e.group.Name, Currency: "USD", MCPAccess: &level},
	)
	require.NoError(e.t, err)
}

var noFollow = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// reply is a fully read response. Helpers return it instead of *http.Response so
// no test can forget to close a body.
type reply struct {
	StatusCode int
	Header     http.Header
}

func (e *env) postJSON(path string, body any) (*reply, map[string]any) {
	e.t.Helper()
	b, _ := json.Marshal(body)
	resp, err := noFollow.Post(e.base+path, "application/json", strings.NewReader(string(b))) //nolint:bodyclose // consume closes it
	require.NoError(e.t, err)
	return consume(e.t, resp)
}

func (e *env) postForm(path string, form url.Values) (*reply, map[string]any) {
	e.t.Helper()
	resp, err := noFollow.PostForm(e.base+path, form) //nolint:bodyclose // consume closes it
	require.NoError(e.t, err)
	return consume(e.t, resp)
}

// get issues a GET without following redirects.
func (e *env) get(path string) (*reply, map[string]any) {
	e.t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, e.base+path, nil)
	require.NoError(e.t, err)
	resp, err := noFollow.Do(req) //nolint:bodyclose // consume closes it
	require.NoError(e.t, err)
	return consume(e.t, resp)
}

// consume reads and closes a response, decoding a JSON object body if there is one.
func consume(t *testing.T, resp *http.Response) (*reply, map[string]any) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return &reply{StatusCode: resp.StatusCode, Header: resp.Header}, m
}

func (e *env) register(name string, redirects ...string) string {
	e.t.Helper()
	if len(redirects) == 0 {
		redirects = []string{redirect}
	}
	resp, body := e.postJSON(PathRegister, map[string]any{keyClientName: name, keyRedirectURIs: redirects})
	require.Equal(e.t, http.StatusCreated, resp.StatusCode, "%v", body)
	return body[keyClientID].(string)
}

func pkce() (verifier, challenge string) {
	verifier = strings.Repeat("v", 20) + uuid.NewString() + uuid.NewString()[:8]
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func (e *env) authorize(clientID, challenge, scope string, extra url.Values) *reply {
	e.t.Helper()
	q := url.Values{
		"response_type": {"code"}, keyClientID: {clientID}, keyRedirectURI: {redirect}, "state": {"st-123"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "scope": {scope},
	}
	for k, v := range extra {
		q[k] = v
	}
	resp, _ := e.get(PathAuthorize + "?" + q.Encode())
	return resp
}

func requestID(t *testing.T, resp *reply) uuid.UUID {
	t.Helper()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, PathConsent, loc.Path)
	id, err := uuid.Parse(loc.Query().Get("request"))
	require.NoError(t, err)
	return id
}

// fullFlow walks register → authorize → approve → token and returns the tokens.
func (e *env) fullFlow(scope string, approve []string) (access, refresh, clientID string) {
	e.t.Helper()
	clientID = e.register("Test Assistant")
	verifier, challenge := pkce()
	id := requestID(e.t, e.authorize(clientID, challenge, scope, nil))

	dest, err := e.oauth.Approve(context.Background(), &e.user, id, e.group.ID, approve, e.base)
	require.NoError(e.t, err)
	u, err := url.Parse(dest)
	require.NoError(e.t, err)
	assert.Equal(e.t, "st-123", u.Query().Get("state"))
	assert.Equal(e.t, e.base, u.Query().Get("iss"))
	code := u.Query().Get("code")
	require.NotEmpty(e.t, code)

	resp, body := e.postForm(PathToken, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, keyRedirectURI: {redirect},
		keyClientID: {clientID}, "code_verifier": {verifier},
	})
	require.Equal(e.t, http.StatusOK, resp.StatusCode, "%v", body)
	assert.Equal(e.t, "Bearer", body["token_type"])
	assert.Equal(e.t, "no-store", resp.Header.Get("Cache-Control"))
	return body["access_token"].(string), body["refresh_token"].(string), clientID
}

type rt struct{ token string }

func (r rt) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+r.token)
	return http.DefaultTransport.RoundTrip(req)
}

func (e *env) tools(token string) ([]string, error) {
	c := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cs, err := c.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint: e.base + "/mcp", HTTPClient: &http.Client{Transport: rt{token}}, DisableStandaloneSSE: true, MaxRetries: -1,
	}, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = cs.Close() }()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, t := range res.Tools {
		names = append(names, t.Name)
	}
	return names, nil
}

// ---------------------------------------------------------------------------

func TestDiscovery(t *testing.T) {
	e := newEnv(t)

	resp, as := e.get(PathASMetadata)
	assert.Equal(t, e.base, as["issuer"])
	assert.Equal(t, e.base+PathAuthorize, as["authorization_endpoint"])
	assert.Equal(t, e.base+PathToken, as["token_endpoint"])
	assert.Equal(t, e.base+PathRegister, as["registration_endpoint"])
	assert.Equal(t, []any{"S256"}, as["code_challenge_methods_supported"], "plain PKCE must not be offered")
	assert.Equal(t, []any{"none"}, as["token_endpoint_auth_methods_supported"])
	assert.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))

	_, pr := e.get(PathResourceMetadata)
	assert.Equal(t, e.base+"/mcp", pr["resource"])
	assert.Equal(t, []any{e.base}, pr["authorization_servers"])

	// A 401 from /mcp points clients at the metadata.
	resp, _ = e.postJSON("/mcp", map[string]any{})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("WWW-Authenticate"), `resource_metadata="`+e.base+PathResourceMetadata+`"`)
}

func TestDiscoveryRefusesWithoutTrustedURL(t *testing.T) {
	e := newEnv(t)
	e.oauth.baseURL = func(*http.Request) string { return "" }
	for _, p := range []string{PathASMetadata, PathResourceMetadata, PathAuthorize} {
		resp, _ := e.get(p)
		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, p)
	}
}

func TestRegistration(t *testing.T) {
	e := newEnv(t)

	t.Run("Accepts", func(t *testing.T) {
		for _, u := range []string{
			"https://claude.ai/api/mcp/auth_callback",
			"http://127.0.0.1:51234/cb",
			"http://localhost:8080/cb",
			"http://[::1]:9000/cb",
			"cursor://anysphere.cursor-retrieval/oauth/callback",
			"com.example.app:/oauth2redirect",
		} {
			resp, body := e.postJSON(PathRegister, map[string]any{keyClientName: "x", keyRedirectURIs: []string{u}})
			assert.Equal(t, http.StatusCreated, resp.StatusCode, "%s: %v", u, body)
		}
	})
	t.Run("Rejects", func(t *testing.T) {
		for _, u := range []string{
			"http://evil.example/cb", "javascript:alert(1)", "data:text/html,x", "file:///etc/passwd",
			"https://x.example/cb#frag", "https://user:pw@x.example/cb", "/relative", "", "myapp://x",
			"https://x.example/cb\r\nSet-Cookie: a=b",
		} {
			resp, body := e.postJSON(PathRegister, map[string]any{keyClientName: "x", keyRedirectURIs: []string{u}})
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "%q should be rejected: %v", u, body)
			assert.Equal(t, "invalid_redirect_uri", body["error"])
		}
	})
	t.Run("OnlyPublicClients", func(t *testing.T) {
		resp, body := e.postJSON(PathRegister, map[string]any{keyClientName: "x", keyRedirectURIs: []string{redirect}, "token_endpoint_auth_method": "client_secret_basic"})
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, "invalid_client_metadata", body["error"])
		resp, _ = e.postJSON(PathRegister, map[string]any{keyClientName: "x", keyRedirectURIs: []string{redirect}, "grant_types": []string{"password"}})
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		resp, _ = e.postJSON(PathRegister, map[string]any{keyClientName: "x", keyRedirectURIs: []string{redirect}, "response_types": []string{keyToken}})
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
	t.Run("CleansDisplayName", func(t *testing.T) {
		_, body := e.postJSON(PathRegister, map[string]any{keyClientName: "Evil\x00\x1b[31m\nApp" + strings.Repeat("x", 300), keyRedirectURIs: []string{redirect}})
		name := body[keyClientName].(string)
		assert.NotContains(t, name, "\x00")
		assert.NotContains(t, name, "\n")
		assert.LessOrEqual(t, len(name), 100)
	})
	t.Run("RateLimited", func(t *testing.T) {
		e2 := newEnv(t)
		var last int
		for i := 0; i < 31; i++ {
			resp, _ := e2.postJSON(PathRegister, map[string]any{keyClientName: "x", keyRedirectURIs: []string{redirect}})
			last = resp.StatusCode
		}
		assert.Equal(t, http.StatusTooManyRequests, last)
	})
	t.Run("DisabledWhenConfiguredOff", func(t *testing.T) {
		e2 := newEnv(t)
		mux := chi.NewMux()
		off := New(e2.repos, config.MCPOAuthConf{AllowDynamicRegistration: false}, func(*http.Request) string { return e2.base }, nil)
		off.Mount(mux)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, PathRegister, strings.NewReader("{}")))
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

func TestAuthorizeValidation(t *testing.T) {
	e := newEnv(t)
	clientID := e.register("Assistant")
	_, challenge := pkce()

	t.Run("UnknownClientDoesNotRedirect", func(t *testing.T) {
		resp := e.authorize("nope", challenge, "", nil)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Empty(t, resp.Header.Get("Location"))
	})
	t.Run("UnregisteredRedirectDoesNotRedirect", func(t *testing.T) {
		resp := e.authorize(clientID, challenge, "", url.Values{keyRedirectURI: {"https://attacker.example/steal"}})
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Empty(t, resp.Header.Get("Location"))
	})
	t.Run("PrefixOfRegisteredRedirectIsNotAMatch", func(t *testing.T) {
		resp := e.authorize(clientID, challenge, "", url.Values{keyRedirectURI: {redirect + "/../evil"}})
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	redirectsWith := func(resp *reply, code string) {
		t.Helper()
		require.Equal(t, http.StatusFound, resp.StatusCode)
		loc, _ := url.Parse(resp.Header.Get("Location"))
		assert.Equal(t, "assistant.example", loc.Host)
		assert.Equal(t, code, loc.Query().Get("error"))
		assert.Equal(t, "st-123", loc.Query().Get("state"))
	}
	t.Run("PKCERequired", func(t *testing.T) {
		redirectsWith(e.authorize(clientID, "", "", nil), "invalid_request")
	})
	t.Run("PlainPKCERefused", func(t *testing.T) {
		redirectsWith(e.authorize(clientID, challenge, "", url.Values{"code_challenge_method": {"plain"}}), "invalid_request")
	})
	t.Run("OnlyCodeFlow", func(t *testing.T) {
		redirectsWith(e.authorize(clientID, challenge, "", url.Values{"response_type": {keyToken}}), "unsupported_response_type")
	})
	t.Run("UnknownScope", func(t *testing.T) {
		redirectsWith(e.authorize(clientID, challenge, "items:read root:everything", nil), "invalid_scope")
	})
	t.Run("WildcardScopeRefused", func(t *testing.T) {
		redirectsWith(e.authorize(clientID, challenge, "*", nil), "invalid_scope")
	})
	t.Run("WrongResourceRefused", func(t *testing.T) {
		redirectsWith(e.authorize(clientID, challenge, "", url.Values{"resource": {"https://other.example/mcp"}}), "invalid_target")
	})
	t.Run("RightResourceAccepted", func(t *testing.T) {
		requestID(t, e.authorize(clientID, challenge, "items:read", url.Values{"resource": {e.base + "/mcp"}}))
	})
	t.Run("DefaultsToReadOnly", func(t *testing.T) {
		id := requestID(t, e.authorize(clientID, challenge, "", nil))
		info, err := e.oauth.RequestInfo(context.Background(), &e.user, id)
		require.NoError(t, err)
		for _, s := range info.Scopes {
			assert.False(t, s.Mutating, s.Scope)
		}
	})
	t.Run("LoopbackPortMayVary", func(t *testing.T) {
		lb := e.register("CLI", "http://127.0.0.1/cb")
		resp := e.authorize(lb, challenge, "items:read", url.Values{keyRedirectURI: {"http://127.0.0.1:54321/cb"}})
		requestID(t, resp)
		resp = e.authorize(lb, challenge, "items:read", url.Values{keyRedirectURI: {"http://127.0.0.1:54321/other"}})
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestConsent(t *testing.T) {
	e := newEnv(t)
	clientID := e.register("Claude")
	_, challenge := pkce()
	ctx := context.Background()

	newReq := func(scope string) uuid.UUID {
		return requestID(t, e.authorize(clientID, challenge, scope, nil))
	}

	t.Run("InfoShowsWhatTheUserNeedsToDecide", func(t *testing.T) {
		id := newReq("items:read items:write")
		info, err := e.oauth.RequestInfo(ctx, &e.user, id)
		require.NoError(t, err)
		assert.Equal(t, "Claude", info.ClientName)
		assert.Equal(t, "assistant.example", info.RedirectHost)
		require.Len(t, info.Scopes, 2)
		require.Len(t, info.Collections, 1)
		assert.Equal(t, scopes.AccessFull, info.Collections[0].MCPAccess)
	})
	t.Run("OffCollectionCannotBeConnected", func(t *testing.T) {
		e.setAccess(scopes.AccessOff)
		defer e.setAccess(scopes.AccessFull)
		_, err := e.oauth.Approve(ctx, &e.user, newReq("items:read"), e.group.ID, nil, e.base)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "turned off")
	})
	t.Run("CannotWidenScopes", func(t *testing.T) {
		_, err := e.oauth.Approve(ctx, &e.user, newReq("items:read"), e.group.ID, []string{"items:read", "items:delete"}, e.base)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "asked for")
	})
	t.Run("CannotPickSomeoneElsesCollection", func(t *testing.T) {
		other, err := e.repos.Groups.GroupCreate(ctx, "not-mine", uuid.Nil)
		require.NoError(t, err)
		_, err = e.oauth.Approve(ctx, &e.user, newReq("items:read"), other.ID, nil, e.base)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a member")
	})
	t.Run("CeilingThatAllowsNothingIsRefused", func(t *testing.T) {
		e.setAccess(scopes.AccessRead)
		defer e.setAccess(scopes.AccessFull)
		_, err := e.oauth.Approve(ctx, &e.user, newReq("items:write"), e.group.ID, nil, e.base)
		require.Error(t, err)
	})
	t.Run("DecidedOnce", func(t *testing.T) {
		id := newReq("items:read")
		_, err := e.oauth.Approve(ctx, &e.user, id, e.group.ID, nil, e.base)
		require.NoError(t, err)
		_, err = e.oauth.Approve(ctx, &e.user, id, e.group.ID, nil, e.base)
		require.ErrorIs(t, err, ErrRequestGone)
		_, err = e.oauth.Deny(ctx, id, e.base)
		require.ErrorIs(t, err, ErrRequestGone)
	})
	t.Run("DenyReportsAccessDenied", func(t *testing.T) {
		dest, err := e.oauth.Deny(ctx, newReq("items:read"), e.base)
		require.NoError(t, err)
		u, _ := url.Parse(dest)
		assert.Equal(t, "access_denied", u.Query().Get("error"))
		assert.Equal(t, "st-123", u.Query().Get("state"))
		assert.Empty(t, u.Query().Get("code"))
	})
	t.Run("ExpiredRequestIsGone", func(t *testing.T) {
		id := newReq("items:read")
		e.now = e.now.Add(requestTTL + time.Minute)
		defer func() { e.now = time.Now() }()
		_, err := e.oauth.RequestInfo(ctx, &e.user, id)
		require.ErrorIs(t, err, ErrRequestGone)
	})
}

func TestTokenExchangeAndMCP(t *testing.T) {
	e := newEnv(t)
	access, _, _ := e.fullFlow("items:read items:write", nil)

	names, err := e.tools(access)
	require.NoError(t, err)
	assert.Contains(t, names, "search_items")
	assert.Contains(t, names, "create_item")
	assert.NotContains(t, names, "delete_item", "delete was never requested or granted")

	t.Run("UserCanNarrowAtConsent", func(t *testing.T) {
		access, _, _ := e.fullFlow("items:read items:write", []string{"items:read"})
		names, err := e.tools(access)
		require.NoError(t, err)
		assert.Contains(t, names, "search_items")
		assert.NotContains(t, names, "create_item")
	})

	t.Run("OwnerCeilingAppliesToLiveTokens", func(t *testing.T) {
		e.setAccess(scopes.AccessRead)
		defer e.setAccess(scopes.AccessFull)
		names, err := e.tools(access)
		require.NoError(t, err)
		assert.Contains(t, names, "search_items")
		assert.NotContains(t, names, "create_item", "lowering the ceiling takes effect without re-consent")

		e.setAccess(scopes.AccessOff)
		_, err = e.tools(access)
		require.Error(t, err, "turning MCP off for the collection cuts off live tokens")
	})

	t.Run("RejectsGarbageAndAPIKeysAsOAuthTokens", func(t *testing.T) {
		_, err := e.tools("hbo_notarealtoken")
		require.Error(t, err)
		_, err = e.tools("random")
		require.Error(t, err)
	})

	t.Run("ExpiredAccessTokenRejected", func(t *testing.T) {
		e.now = e.now.Add(2 * time.Hour)
		defer func() { e.now = time.Now() }()
		_, err := e.tools(access)
		require.Error(t, err)
	})
}

func TestTokenEndpointRejections(t *testing.T) {
	e := newEnv(t)
	clientID := e.register("Assistant")
	verifier, challenge := pkce()
	ctx := context.Background()

	code := func() string {
		id := requestID(t, e.authorize(clientID, challenge, "items:read", nil))
		dest, err := e.oauth.Approve(ctx, &e.user, id, e.group.ID, nil, e.base)
		require.NoError(t, err)
		u, _ := url.Parse(dest)
		return u.Query().Get("code")
	}
	exchange := func(c string, over map[string]string) (int, map[string]any) {
		f := url.Values{"grant_type": {"authorization_code"}, "code": {c}, keyRedirectURI: {redirect}, keyClientID: {clientID}, "code_verifier": {verifier}}
		for k, v := range over {
			f.Set(k, v)
		}
		resp, body := e.postForm(PathToken, f)
		return resp.StatusCode, body
	}

	t.Run("WrongVerifier", func(t *testing.T) {
		st, body := exchange(code(), map[string]string{"code_verifier": strings.Repeat("x", 50)})
		assert.Equal(t, http.StatusBadRequest, st)
		assert.Equal(t, "invalid_grant", body["error"])
	})
	t.Run("MalformedVerifier", func(t *testing.T) {
		st, _ := exchange(code(), map[string]string{"code_verifier": "short"})
		assert.Equal(t, http.StatusBadRequest, st)
	})
	t.Run("WrongClient", func(t *testing.T) {
		st, _ := exchange(code(), map[string]string{keyClientID: e.register("Other")})
		assert.Equal(t, http.StatusBadRequest, st)
	})
	t.Run("WrongRedirect", func(t *testing.T) {
		st, _ := exchange(code(), map[string]string{keyRedirectURI: "https://assistant.example/other"})
		assert.Equal(t, http.StatusBadRequest, st)
	})
	t.Run("UnknownCode", func(t *testing.T) {
		st, _ := exchange("hbk_unknown", nil)
		assert.Equal(t, http.StatusBadRequest, st)
	})
	t.Run("AccessTokenIsNotACode", func(t *testing.T) {
		access, _, _ := e.fullFlow("items:read", nil)
		st, _ := exchange(access, nil)
		assert.Equal(t, http.StatusBadRequest, st)
	})
	t.Run("ExpiredCode", func(t *testing.T) {
		c := code()
		e.now = e.now.Add(codeTTL + time.Second)
		defer func() { e.now = time.Now() }()
		st, _ := exchange(c, nil)
		assert.Equal(t, http.StatusBadRequest, st)
	})
	t.Run("UnsupportedGrant", func(t *testing.T) {
		resp, body := e.postForm(PathToken, url.Values{"grant_type": {"password"}, "username": {"a"}, "password": {"b"}})
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Equal(t, "unsupported_grant_type", body["error"])
	})
	t.Run("WrongResource", func(t *testing.T) {
		st, body := exchange(code(), map[string]string{"resource": "https://other.example/mcp"})
		assert.Equal(t, http.StatusBadRequest, st)
		assert.Equal(t, "invalid_target", body["error"])
	})
	t.Run("GETNotAllowed", func(t *testing.T) {
		resp, _ := e.get(PathToken)
		assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	})

	t.Run("CodeReplayRevokesTheGrant", func(t *testing.T) {
		c := code()
		st, body := exchange(c, nil)
		require.Equal(t, http.StatusOK, st, "%v", body)
		access := body["access_token"].(string)
		_, err := e.tools(access)
		require.NoError(t, err)

		st, _ = exchange(c, nil)
		assert.Equal(t, http.StatusBadRequest, st, "a spent code can't be used again")
		_, err = e.tools(access)
		require.Error(t, err, "replaying a code means it leaked: tokens issued from it are revoked")
	})
}

func TestRefreshRotationAndTheftDetection(t *testing.T) {
	e := newEnv(t)
	access1, refresh1, clientID := e.fullFlow("items:read items:write", nil)

	refresh := func(rt string, cid string) (int, map[string]any) {
		resp, body := e.postForm(PathToken, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, keyClientID: {cid}})
		return resp.StatusCode, body
	}

	st, _ := refresh(refresh1, "someone-else")
	assert.Equal(t, http.StatusBadRequest, st, "refresh tokens are bound to their client")

	st, body := refresh(refresh1, clientID)
	require.Equal(t, http.StatusOK, st, "%v", body)
	access2, refresh2 := body["access_token"].(string), body["refresh_token"].(string)
	assert.NotEqual(t, access1, access2)
	assert.NotEqual(t, refresh1, refresh2, "refresh tokens rotate")
	assert.Equal(t, "items:read items:write", body["scope"])

	_, err := e.tools(access2)
	require.NoError(t, err)

	// A stale refresh token shows up again: someone kept a copy. Everything dies.
	st, body = refresh(refresh1, clientID)
	assert.Equal(t, http.StatusBadRequest, st)
	assert.Contains(t, body["error_description"], "revoked")
	_, err = e.tools(access2)
	require.Error(t, err, "theft detection revokes the newest access token too")
	st, _ = refresh(refresh2, clientID)
	assert.Equal(t, http.StatusBadRequest, st, "and the newest refresh token")
}

func TestRevocation(t *testing.T) {
	e := newEnv(t)

	t.Run("RevokeEndpoint", func(t *testing.T) {
		access, refresh, clientID := e.fullFlow("items:read", nil)
		resp, _ := e.postForm(PathRevoke, url.Values{keyToken: {refresh}, keyClientID: {clientID}})
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		_, err := e.tools(access)
		require.Error(t, err, "revoking one token ends the whole grant")
	})
	t.Run("OtherClientsCannotRevoke", func(t *testing.T) {
		access, refresh, _ := e.fullFlow("items:read", nil)
		other := e.register("Other")
		resp, _ := e.postForm(PathRevoke, url.Values{keyToken: {refresh}, keyClientID: {other}})
		assert.Equal(t, http.StatusOK, resp.StatusCode, "no oracle: always 200")
		_, err := e.tools(access)
		require.NoError(t, err, "but nothing was revoked")
	})
	t.Run("UnknownTokenIsStill200", func(t *testing.T) {
		resp, _ := e.postForm(PathRevoke, url.Values{keyToken: {"hbo_nothing"}})
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
	t.Run("UserDisconnectsFromSettings", func(t *testing.T) {
		access, _, _ := e.fullFlow("items:read", nil)
		grants, err := e.repos.OAuth.ListGrantsByUser(context.Background(), e.user.ID)
		require.NoError(t, err)
		require.NotEmpty(t, grants)
		assert.Equal(t, "Test Assistant", grants[0].ClientName)
		assert.Equal(t, "home", grants[0].GroupName)

		// somebody else's grant id is not found
		require.Error(t, e.repos.OAuth.DeleteGrantForUser(context.Background(), uuid.New(), grants[0].ID))
		_, err = e.tools(access)
		require.NoError(t, err)

		require.NoError(t, e.repos.OAuth.DeleteGrantForUser(context.Background(), e.user.ID, grants[0].ID))
		_, err = e.tools(access)
		require.Error(t, err)
	})
	t.Run("ReconsentReplacesTheOldGrant", func(t *testing.T) {
		e2 := newEnv(t)
		clientID := e2.register("Claude")
		_, challenge := pkce()
		for i := 0; i < 3; i++ {
			id := requestID(t, e2.authorize(clientID, challenge, "items:read", nil))
			_, err := e2.oauth.Approve(context.Background(), &e2.user, id, e2.group.ID, nil, e2.base)
			require.NoError(t, err)
		}
		grants, err := e2.repos.OAuth.ListGrantsByUser(context.Background(), e2.user.ID)
		require.NoError(t, err)
		assert.Len(t, grants, 1)
	})
	t.Run("DeletingTheUserRemovesTheirGrants", func(t *testing.T) {
		e2 := newEnv(t)
		access, _, _ := e2.fullFlow("items:read", nil)
		require.NoError(t, e2.svc.User.DeleteSelf(context.Background(), e2.user.ID))
		_, err := e2.tools(access)
		require.Error(t, err)
	})
}

func TestRedirectURIHelpers(t *testing.T) {
	assert.True(t, redirectMatches("https://a.example/cb", "https://a.example/cb"))
	assert.False(t, redirectMatches("https://a.example/cb", "https://a.example/cb2"))
	assert.False(t, redirectMatches("https://a.example/cb", "https://a.example:8443/cb"))
	assert.True(t, redirectMatches("http://127.0.0.1/cb", "http://127.0.0.1:9999/cb"))
	assert.False(t, redirectMatches("http://127.0.0.1/cb", "http://localhost:9999/cb"), "loopback hostnames must match")
	assert.False(t, redirectMatches("https://a.example/cb", "https://a.example:99/cb"), "port freedom is loopback-only")

	require.NoError(t, validateRedirectURI("https://ok.example/cb", []string{"ok.example"}))
	require.Error(t, validateRedirectURI("https://other.example/cb", []string{"ok.example"}))

	got, err := withParams("https://a.example/cb?x=1", map[string]string{"code": "c", "state": ""})
	require.NoError(t, err)
	assert.Equal(t, "https://a.example/cb?code=c&x=1", got, "empty values are omitted, existing query kept")
}
