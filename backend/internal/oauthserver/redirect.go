package oauthserver

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Redirect URIs are where an authorization code is delivered, so a bad one
// hands a code to an attacker. Validation is strict: https, a loopback address
// for native apps (RFC 8252), or a private-use scheme owned by a known app.
// Everything else — including every scheme a browser would execute — is refused.

var schemePattern = regexp.MustCompile(`^[a-z][a-z0-9+.\-]*$`)

// dangerousSchemes are never acceptable, whatever the client claims.
var dangerousSchemes = []string{"javascript", "data", "file", "vbscript", "about", "blob", "ftp", "ws", "wss", "http"}

// knownAppSchemes are private-use schemes registered by popular MCP clients that
// lack a reverse-domain form. Schemes containing a dot are accepted as
// reverse-domain private-use schemes per RFC 8252 §7.1.
var knownAppSchemes = []string{"cursor", "vscode", "vscode-insiders", "windsurf", "claude", "chatgpt", "zed"}

const maxRedirectURILen = 2048

func isLoopbackHost(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// validateRedirectURI reports why a URI may not be registered, or nil.
func validateRedirectURI(raw string, allowedHosts []string) error {
	if raw == "" || len(raw) > maxRedirectURILen {
		return errors.New("redirect_uri must be 1-2048 characters")
	}
	if strings.ContainsAny(raw, "\r\n\t ") {
		return errors.New("redirect_uri must not contain whitespace")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return errors.New("redirect_uri must be an absolute URI")
	}
	if u.Fragment != "" || strings.Contains(raw, "#") {
		return errors.New("redirect_uri must not contain a fragment")
	}
	if u.User != nil {
		return errors.New("redirect_uri must not contain credentials")
	}

	scheme := strings.ToLower(u.Scheme)
	switch {
	case scheme == "https":
		if u.Hostname() == "" {
			return errors.New("redirect_uri needs a host")
		}
		if len(allowedHosts) > 0 && !slices.ContainsFunc(allowedHosts, func(h string) bool {
			return strings.EqualFold(h, u.Hostname())
		}) {
			return errors.New("redirect_uri host is not allowed on this server")
		}
		return nil
	case scheme == "http":
		if !isLoopbackHost(u.Hostname()) {
			return errors.New("http redirect_uri is only allowed for loopback addresses")
		}
		return nil
	case slices.Contains(dangerousSchemes, scheme):
		return errors.New("redirect_uri scheme is not allowed")
	case !schemePattern.MatchString(scheme):
		return errors.New("redirect_uri scheme is invalid")
	case strings.Contains(scheme, ".") || slices.Contains(knownAppSchemes, scheme):
		return nil
	default:
		return errors.New("custom redirect_uri schemes must be reverse-domain (e.g. com.example.app)")
	}
}

// redirectMatches compares a requested redirect URI to a registered one: exact,
// except that loopback redirects may differ in port, since native apps pick a
// free port at run time (RFC 8252 §7.3).
func redirectMatches(registered, requested string) bool {
	if registered == requested {
		return true
	}
	ru, err1 := url.Parse(registered)
	qu, err2 := url.Parse(requested)
	if err1 != nil || err2 != nil {
		return false
	}
	if !strings.EqualFold(ru.Scheme, "http") || !isLoopbackHost(ru.Hostname()) {
		return false
	}
	return strings.EqualFold(qu.Scheme, "http") &&
		strings.EqualFold(ru.Hostname(), qu.Hostname()) &&
		ru.Path == qu.Path && ru.RawQuery == qu.RawQuery
}

// withParams returns uri with the given query parameters appended.
func withParams(uri string, params map[string]string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	q := u.Query()
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
