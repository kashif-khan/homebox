package validate

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
)

// ValidateLookupURL checks the address of a product lookup service that a
// collection owner typed in: an http or https URL whose host passes the same
// network policy as notifiers and webhooks. Addresses set in the server's own
// environment are the operator's and are not checked.
func ValidateLookupURL(raw string, cfg *config.NotifierConf) error {
	if cfg == nil {
		return fmt.Errorf("notifier configuration is nil, cannot validate URL")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("that is not a valid address")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("the address must start with http:// or https://")
	}
	if u.Hostname() == "" {
		return fmt.Errorf("the address has no host")
	}
	if u.User != nil {
		return fmt.Errorf("the address must not contain a username or password; use the token instead")
	}
	return validateHostAgainstPolicy(u.Hostname(), cfg)
}

// LookupHTTPClient returns the client used to call a lookup service whose address
// came from a collection owner. It dials only addresses that pass the network
// policy, checked again at connect time, and does not follow redirects.
func LookupHTTPClient(cfg *config.NotifierConf, timeout time.Duration) *http.Client {
	return WebhookHTTPClient(cfg, timeout)
}
