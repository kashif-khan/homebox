package validate

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
)

// ValidateWebhookURL checks the destination of an outbound webhook: an http or
// https URL whose host passes the same allow/block network policy as notifiers
// (loopback, bogon and cloud-metadata addresses are refused by default).
func ValidateWebhookURL(raw string, cfg *config.NotifierConf) error {
	if cfg == nil {
		return fmt.Errorf("notifier configuration is nil, cannot validate URL")
	}
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid webhook URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("webhook URL must start with http:// or https://")
	}
	if u.Hostname() == "" {
		return fmt.Errorf("webhook URL has no host")
	}
	if u.User != nil {
		return fmt.Errorf("webhook URL must not contain a username or password; use the signing secret instead")
	}
	return validateHostAgainstPolicy(u.Hostname(), cfg)
}

// WebhookHTTPClient returns the client outbound webhooks are sent with. It dials
// only addresses that pass the network policy (re-checked at connect time, which
// closes the DNS-rebinding window) and never follows a redirect: a receiver that
// answers 3xx is reported as a failure rather than chased somewhere unchecked.
func WebhookHTTPClient(cfg *config.NotifierConf, timeout time.Duration) *http.Client {
	c := NotifierHTTPClient(cfg)
	c.Timeout = timeout
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c
}
