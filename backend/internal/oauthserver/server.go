// Package oauthserver is a small OAuth 2.1 authorization server whose only job is
// to let AI assistants that cannot hold a static API key (hosted Claude, ChatGPT)
// act for a Homebox user, in one collection, with scopes the user approved.
//
// It implements authorization code + PKCE (S256 only), refresh token rotation
// with reuse detection, dynamic client registration (RFC 7591), authorization
// server and protected resource metadata (RFC 8414, RFC 9728), resource
// indicators (RFC 8707) and revocation (RFC 7009). Clients are public; there are
// no client secrets and no implicit or password grants.
//
// The user's consent happens in the Homebox web UI, not here: /oauth/authorize
// stores the request and redirects to the consent page, which approves or denies
// it through the authenticated API (see Approve and Deny).
package oauthserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/sysadminsmedia/homebox/backend/internal/core/scopes"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/mcpserver"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
	"github.com/sysadminsmedia/homebox/backend/pkgs/hasher"
)

const (
	// Paths. The metadata paths are fixed by RFC 8414 / RFC 9728.
	PathResourceMetadata = mcpserver.ResourceMetadataPath
	PathASMetadata       = "/.well-known/oauth-authorization-server"
	PathAuthorize        = "/oauth/authorize"
	PathToken            = "/oauth/token"
	PathRegister         = "/oauth/register"
	PathRevoke           = "/oauth/revoke"
	// PathConsent is the web UI route that asks the user to approve a request.
	PathConsent = "/oauth/consent"
	// ResourcePath is the protected resource tokens are issued for.
	ResourcePath = "/mcp"

	grantAuthorizationCode = "authorization_code"
	grantRefreshToken      = "refresh_token"

	// fieldRefreshToken is the request parameter and response member carrying a
	// refresh token. It happens to equal the grant type's name; they are distinct
	// things in the protocol.
	fieldRefreshToken = "refresh_token"

	accessPrefix  = "hbo_"
	refreshPrefix = "hbr_"

	requestTTL = 10 * time.Minute
	codeTTL    = time.Minute

	maxClients = 5000
)

var verifierPattern = regexp.MustCompile(`^[A-Za-z0-9\-._~]{43,128}$`)

// Server is the authorization server.
type Server struct {
	repos *repo.AllRepos
	conf  config.MCPOAuthConf
	// baseURL returns the trusted public URL of the instance for a request, or "".
	baseURL func(*http.Request) string
	now     func() time.Time

	mu         sync.Mutex
	lastPrune  time.Time
	lastTouch  map[uuid.UUID]time.Time
	regLimiter *registrationLimiter
	tokLimiter *registrationLimiter
	clientIP   func(*http.Request) string
}

// New builds the server. baseURL must only return values the operator trusts
// (see v1.SecureBaseURL); the issuer and every endpoint URL derive from it.
//
// clientIP identifies the caller for rate limiting; pass the app's own resolver,
// which only trusts proxy headers when the operator enabled that. Nil falls back
// to the connection's address.
func New(repos *repo.AllRepos, conf config.MCPOAuthConf, baseURL func(*http.Request) string, clientIP func(*http.Request) string) *Server {
	if clientIP == nil {
		clientIP = remoteIP
	}
	return &Server{
		repos: repos, conf: conf, baseURL: baseURL, now: time.Now, clientIP: clientIP,
		lastTouch:  map[uuid.UUID]time.Time{},
		regLimiter: newRegistrationLimiter(30, time.Hour),
		tokLimiter: newRegistrationLimiter(120, time.Minute),
	}
}

// Mount registers the public endpoints.
func (s *Server) Mount(mux interface {
	Handle(pattern string, h http.Handler)
}) {
	wrap := s.limitTokenEndpoints
	mux.Handle(PathResourceMetadata, cors(http.HandlerFunc(s.handleResourceMetadata)))
	mux.Handle(PathASMetadata, cors(http.HandlerFunc(s.handleASMetadata)))
	mux.Handle(PathAuthorize, http.HandlerFunc(s.handleAuthorize))
	mux.Handle(PathToken, cors(wrap(http.HandlerFunc(s.handleToken))))
	mux.Handle(PathRevoke, cors(wrap(http.HandlerFunc(s.handleRevoke))))
	if s.conf.AllowDynamicRegistration {
		mux.Handle(PathRegister, cors(wrap(http.HandlerFunc(s.handleRegister))))
	}
}

// ---- helpers ----

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// These endpoints authenticate with PKCE and bearer tokens, never ambient
		// cookies, so cross-origin access carries no CSRF risk and browser-based MCP
		// clients (such as the MCP Inspector) need it.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, MCP-Protocol-Version")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type oauthError struct {
	Error       string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, oauthError{Error: code, Description: desc})
}

// base returns the trusted public URL, or writes a 503 and returns "".
func (s *Server) base(w http.ResponseWriter, r *http.Request) string {
	var b string
	if s.baseURL != nil {
		b = strings.TrimRight(s.baseURL(r), "/")
	}
	if b == "" {
		writeError(w, http.StatusServiceUnavailable, "server_error",
			"the server's public URL is not configured: set HBOX_OPTIONS_HOSTNAME (or enable HBOX_OPTIONS_TRUST_PROXY behind a proxy)")
	}
	return b
}

func newSecret(prefix string) (raw string, hash []byte) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	raw = prefix + base64.RawURLEncoding.EncodeToString(b)
	return raw, hasher.HashAPIKey(raw)
}

func newClientID() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return "hbc_" + base64.RawURLEncoding.EncodeToString(b)
}

// ---- discovery ----

func (s *Server) handleResourceMetadata(w http.ResponseWriter, r *http.Request) {
	b := s.base(w, r)
	if b == "" {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 b + ResourcePath,
		"authorization_servers":    []string{b},
		"scopes_supported":         scopes.All(),
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Homebox",
	})
}

func (s *Server) handleASMetadata(w http.ResponseWriter, r *http.Request) {
	b := s.base(w, r)
	if b == "" {
		return
	}
	doc := map[string]any{
		"issuer":                                         b,
		"authorization_endpoint":                         b + PathAuthorize,
		"token_endpoint":                                 b + PathToken,
		"revocation_endpoint":                            b + PathRevoke,
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{grantAuthorizationCode, grantRefreshToken},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"scopes_supported":                               scopes.All(),
		"authorization_response_iss_parameter_supported": true,
	}
	if s.conf.AllowDynamicRegistration {
		doc["registration_endpoint"] = b + PathRegister
	}
	writeJSON(w, http.StatusOK, doc)
}

// ---- registration (RFC 7591) ----

type registrationRequest struct {
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "invalid_request", "POST required")
		return
	}
	if !s.regLimiter.allow(s.clientIP(r), s.now()) {
		w.Header().Set("Retry-After", "3600")
		writeError(w, http.StatusTooManyRequests, "invalid_request", "too many registrations")
		return
	}

	var req registrationRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_client_metadata", "body must be JSON")
		return
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 10 {
		writeError(w, http.StatusBadRequest, "invalid_redirect_uri", "provide 1-10 redirect_uris")
		return
	}
	for _, u := range req.RedirectURIs {
		if err := validateRedirectURI(u, s.conf.AllowedRedirectHosts); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
			return
		}
	}
	if m := req.TokenEndpointAuthMethod; m != "" && m != "none" {
		writeError(w, http.StatusBadRequest, "invalid_client_metadata", "only public clients (token_endpoint_auth_method=none) are supported")
		return
	}
	for _, g := range req.GrantTypes {
		if g != grantAuthorizationCode && g != grantRefreshToken {
			writeError(w, http.StatusBadRequest, "invalid_client_metadata", "unsupported grant_type "+g)
			return
		}
	}
	for _, t := range req.ResponseTypes {
		if t != "code" {
			writeError(w, http.StatusBadRequest, "invalid_client_metadata", "unsupported response_type "+t)
			return
		}
	}

	name := cleanName(req.ClientName)
	if name == "" {
		name = "Unnamed application"
	}

	ctx := r.Context()
	s.maybePrune(ctx)
	if n, err := s.repos.OAuth.CountClients(ctx); err == nil && n >= maxClients {
		writeError(w, http.StatusServiceUnavailable, "server_error", "client registry is full")
		return
	}

	clientID := newClientID()
	if _, err := s.repos.OAuth.CreateClient(ctx, clientID, name, req.RedirectURIs); err != nil {
		log.Err(err).Msg("oauth: register client")
		writeError(w, http.StatusInternalServerError, "server_error", "could not register client")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  clientID,
		"client_id_issued_at":        s.now().Unix(),
		"client_name":                name,
		"redirect_uris":              req.RedirectURIs,
		"grant_types":                []string{grantAuthorizationCode, grantRefreshToken},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

// cleanName makes a client-supplied display name safe to show on a consent
// screen: no control characters, bounded length.
func cleanName(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= 100 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

// ---- authorize ----

func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	b := s.base(w, r)
	if b == "" {
		return
	}
	q := r.URL.Query()
	ctx := r.Context()
	s.maybePrune(ctx)

	// Until the client and redirect URI are verified, errors must NOT redirect:
	// the redirect target is unproven and could be an attacker's.
	client, err := s.repos.OAuth.GetClient(ctx, q.Get("client_id"))
	if err != nil {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return
	}
	redirectURI := q.Get("redirect_uri")
	if redirectURI == "" && len(client.RedirectURIs) == 1 {
		redirectURI = client.RedirectURIs[0]
	}
	registered := false
	for _, ru := range client.RedirectURIs {
		if redirectMatches(ru, redirectURI) {
			registered = true
			break
		}
	}
	if !registered {
		http.Error(w, "redirect_uri does not match a registered URI", http.StatusBadRequest)
		return
	}

	state := q.Get("state")
	fail := func(code, desc string) {
		target, err := withParams(redirectURI, map[string]string{"error": code, "error_description": desc, "state": state, "iss": b})
		if err != nil {
			http.Error(w, desc, http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, target, http.StatusFound)
	}

	if q.Get("response_type") != "code" {
		fail("unsupported_response_type", "only response_type=code is supported")
		return
	}
	challenge := q.Get("code_challenge")
	if challenge == "" || q.Get("code_challenge_method") != "S256" {
		fail("invalid_request", "PKCE is required: send code_challenge with code_challenge_method=S256")
		return
	}
	if len(challenge) < 43 || len(challenge) > 128 {
		fail("invalid_request", "code_challenge has an invalid length")
		return
	}
	if res := q.Get("resource"); res != "" && res != b+ResourcePath {
		fail("invalid_target", "unknown resource; this server issues tokens for "+b+ResourcePath)
		return
	}

	var requested []string
	if sc := strings.Fields(q.Get("scope")); len(sc) > 0 {
		requested = sc
	}
	granted, err := scopes.Normalize(requested)
	if err != nil || scopesContainFull(granted) {
		fail("invalid_scope", "unknown or disallowed scope")
		return
	}

	id, err := s.repos.OAuth.CreateRequest(ctx, client.ID, redirectURI, granted, state, challenge, q.Get("resource"), s.now().Add(requestTTL))
	if err != nil {
		log.Err(err).Msg("oauth: create authorization request")
		fail("server_error", "could not start the authorization")
		return
	}
	http.Redirect(w, r, b+PathConsent+"?request="+url.QueryEscape(id.String()), http.StatusFound)
}

func scopesContainFull(list []string) bool {
	for _, s := range list {
		if s == scopes.Full {
			return true
		}
	}
	return false
}

// ---- token ----

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "invalid_request", "POST required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "body must be application/x-www-form-urlencoded")
		return
	}
	if b := s.base(w, r); b == "" {
		return
	} else if res := r.PostForm.Get("resource"); res != "" && res != b+ResourcePath {
		writeError(w, http.StatusBadRequest, "invalid_target", "unknown resource")
		return
	}

	s.maybePrune(r.Context())
	switch r.PostForm.Get("grant_type") {
	case grantAuthorizationCode:
		s.exchangeCode(w, r)
	case grantRefreshToken:
		s.exchangeRefresh(w, r)
	default:
		writeError(w, http.StatusBadRequest, "unsupported_grant_type", "use authorization_code or refresh_token")
	}
}

func (s *Server) exchangeCode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f := r.PostForm
	invalid := func(desc string) { writeError(w, http.StatusBadRequest, "invalid_grant", desc) }

	code := f.Get("code")
	tok, err := s.repos.OAuth.GetToken(ctx, hasher.HashAPIKey(code))
	if err != nil || tok.Kind != "code" {
		invalid("the authorization code is invalid or expired")
		return
	}
	// A code is spent exactly once. Seeing it again means it leaked, so the grant
	// it produced is revoked along with every token already issued from it.
	if tok.UsedAt != nil {
		_ = s.repos.OAuth.RevokeGrant(ctx, tok.GrantID)
		invalid("the authorization code was already used")
		return
	}
	if !s.now().Before(tok.ExpiresAt) {
		invalid("the authorization code is invalid or expired")
		return
	}
	if f.Get("client_id") != tok.ClientID {
		invalid("the code was issued to a different client")
		return
	}
	if f.Get("redirect_uri") != tok.RedirectURI {
		invalid("redirect_uri does not match the authorization request")
		return
	}
	verifier := f.Get("code_verifier")
	if !verifierPattern.MatchString(verifier) {
		invalid("code_verifier is missing or malformed")
		return
	}
	sum := sha256.Sum256([]byte(verifier))
	if subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(tok.CodeChallenge)) != 1 {
		invalid("PKCE verification failed")
		return
	}
	won, err := s.repos.OAuth.MarkTokenUsed(ctx, tok.ID, s.now())
	if err != nil {
		log.Err(err).Msg("oauth: spend code")
		writeError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	if !won { // lost a race with a second use of the same code
		_ = s.repos.OAuth.RevokeGrant(ctx, tok.GrantID)
		invalid("the authorization code was already used")
		return
	}
	s.issuePair(w, r, tok)
}

func (s *Server) exchangeRefresh(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f := r.PostForm
	invalid := func(desc string) { writeError(w, http.StatusBadRequest, "invalid_grant", desc) }

	tok, err := s.repos.OAuth.GetToken(ctx, hasher.HashAPIKey(f.Get(fieldRefreshToken)))
	if err != nil || tok.Kind != "refresh" {
		invalid("the refresh token is invalid or expired")
		return
	}
	if f.Get("client_id") != tok.ClientID {
		invalid("the refresh token was issued to a different client")
		return
	}
	// Refresh tokens rotate. Presenting an already-rotated one means either the
	// client or a thief holds a stale copy; revoke the grant so neither keeps access.
	if tok.UsedAt != nil {
		_ = s.repos.OAuth.RevokeGrant(ctx, tok.GrantID)
		invalid("the refresh token was already used; the grant has been revoked")
		return
	}
	if !s.now().Before(tok.ExpiresAt) {
		invalid("the refresh token is invalid or expired")
		return
	}
	won, err := s.repos.OAuth.MarkTokenUsed(ctx, tok.ID, s.now())
	if err != nil {
		log.Err(err).Msg("oauth: rotate refresh token")
		writeError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	if !won {
		_ = s.repos.OAuth.RevokeGrant(ctx, tok.GrantID)
		invalid("the refresh token was already used; the grant has been revoked")
		return
	}
	s.issuePair(w, r, tok)
}

func (s *Server) issuePair(w http.ResponseWriter, r *http.Request, from repo.OAuthTokenOut) {
	ctx := r.Context()
	now := s.now()

	access, accessHash := newSecret(accessPrefix)
	refresh, refreshHash := newSecret(refreshPrefix)
	accessTTL, refreshTTL := s.conf.AccessTokenTTL, s.conf.RefreshTokenTTL
	if accessTTL <= 0 {
		accessTTL = time.Hour
	}
	if refreshTTL <= 0 {
		refreshTTL = 30 * 24 * time.Hour
	}

	if err := s.repos.OAuth.IssueToken(ctx, from.GrantID, "access", accessHash, now.Add(accessTTL), "", ""); err != nil {
		log.Err(err).Msg("oauth: issue access token")
		writeError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	if err := s.repos.OAuth.IssueToken(ctx, from.GrantID, "refresh", refreshHash, now.Add(refreshTTL), "", ""); err != nil {
		log.Err(err).Msg("oauth: issue refresh token")
		writeError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":    access,
		"token_type":      "Bearer",
		"expires_in":      int(accessTTL.Seconds()),
		fieldRefreshToken: refresh,
		"scope":           strings.Join(from.Scopes, " "),
	})
}

// ---- revoke (RFC 7009) ----

func (s *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "invalid_request", "POST required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "")
		return
	}
	// Revoking either token of a pair ends the whole grant: a half-revoked
	// connection is never what the caller meant. Unknown tokens succeed silently,
	// per the RFC, so the endpoint can't be used to probe for valid ones.
	if tok, err := s.repos.OAuth.GetToken(r.Context(), hasher.HashAPIKey(r.PostForm.Get("token"))); err == nil {
		if cid := r.PostForm.Get("client_id"); cid == "" || cid == tok.ClientID {
			_ = s.repos.OAuth.RevokeGrant(r.Context(), tok.GrantID)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

// ---- resource server side ----

// Verify implements mcpserver.OAuthVerifier.
func (s *Server) Verify(ctx context.Context, token string) (*mcpserver.OAuthGrant, error) {
	if !strings.HasPrefix(token, accessPrefix) {
		return nil, mcpserver.ErrInvalidToken
	}
	tok, err := s.repos.OAuth.GetToken(ctx, hasher.HashAPIKey(token))
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, mcpserver.ErrInvalidToken
		}
		return nil, err
	}
	if tok.Kind != "access" || !s.now().Before(tok.ExpiresAt) {
		return nil, mcpserver.ErrInvalidToken
	}
	s.touch(ctx, tok.GrantID)
	return &mcpserver.OAuthGrant{
		ID: tok.GrantID, UserID: tok.UserID, GroupID: tok.GroupID, Scopes: tok.Scopes,
		ClientID: tok.ClientID, Name: tok.ClientName,
	}, nil
}

// touch records use at most once a minute per grant, so a chatty assistant does
// not turn every request into a write.
func (s *Server) touch(ctx context.Context, grantID uuid.UUID) {
	s.mu.Lock()
	last := s.lastTouch[grantID]
	now := s.now()
	if now.Sub(last) < time.Minute {
		s.mu.Unlock()
		return
	}
	s.lastTouch[grantID] = now
	if len(s.lastTouch) > 10000 {
		s.lastTouch = map[uuid.UUID]time.Time{grantID: now}
	}
	s.mu.Unlock()
	if err := s.repos.OAuth.TouchGrant(ctx, grantID, now); err != nil && !ent.IsNotFound(err) {
		log.Warn().Err(err).Msg("oauth: failed to update grant last_used_at")
	}
}

// maybePrune clears expired requests and tokens and abandoned client
// registrations, at most every ten minutes, from whichever request comes first.
func (s *Server) maybePrune(ctx context.Context) {
	s.mu.Lock()
	now := s.now()
	if now.Sub(s.lastPrune) < 10*time.Minute {
		s.mu.Unlock()
		return
	}
	s.lastPrune = now
	s.mu.Unlock()

	// Detached so a cancelled request doesn't abandon half the sweep.
	go func() {
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, _ = s.repos.OAuth.PruneRequests(bg, now)
		_, _ = s.repos.OAuth.DeleteExpiredTokens(bg, now.Add(-24*time.Hour))
		_, _ = s.repos.OAuth.PruneUnusedClients(bg, now.Add(-24*time.Hour))
	}()
}

// ---- registration rate limit ----

type registrationLimiter struct {
	limit  int
	window time.Duration
	mu     sync.Mutex
	hits   map[string][]time.Time
}

func newRegistrationLimiter(limit int, window time.Duration) *registrationLimiter {
	return &registrationLimiter{limit: limit, window: window, hits: map[string][]time.Time{}}
}

func (l *registrationLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := now.Add(-l.window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	if len(l.hits) > 10000 {
		for k, v := range l.hits {
			if len(v) == 0 || v[len(v)-1].Before(cut) {
				delete(l.hits, k)
			}
		}
	}
	return true
}

// limitTokenEndpoints throttles the token and revocation endpoints per client
// address. Legitimate assistants make a handful of calls an hour; this only
// bites on guessing.
func (s *Server) limitTokenEndpoints(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && !s.tokLimiter.allow(s.clientIP(r), s.now()) {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "invalid_request", "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func remoteIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}
