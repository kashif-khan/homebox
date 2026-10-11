package validate

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
)

func TestValidateWebhookURL(t *testing.T) {
	cfg := &config.NotifierConf{BlockLocalhost: true, BlockBogonNets: true, BlockCloudMetadata: true}

	require.NoError(t, ValidateWebhookURL("https://93.184.216.34/hook", cfg))
	require.NoError(t, ValidateWebhookURL("http://93.184.216.34:8123/api/webhook/abc", cfg))
	// A private LAN address is fine by default: Home Assistant and n8n live there.
	require.NoError(t, ValidateWebhookURL("http://192.168.1.20:5678/webhook/x", cfg))

	for name, raw := range map[string]string{
		"loopback":       "http://127.0.0.1/hook",
		"loopback name":  "http://localhost/hook",
		"cloud metadata": "http://169.254.169.254/latest/meta-data",
		"no scheme":      "example.com/hook",
		"ftp":            "ftp://93.184.216.34/hook",
		"generic":        "generic://93.184.216.34",
		"no host":        "https:///hook",
		"credentials":    "https://user:pw@93.184.216.34/hook",
		"garbage":        "://",
	} {
		assert.Error(t, ValidateWebhookURL(raw, cfg), name)
	}

	assert.Error(t, ValidateWebhookURL("https://93.184.216.34/", nil), "nil config is refused rather than allowing everything")

	strict := &config.NotifierConf{BlockLocalhost: true, BlockLocalNets: true, BlockBogonNets: true}
	assert.Error(t, ValidateWebhookURL("http://192.168.1.20/hook", strict), "operator can block the LAN")
}

func TestWebhookHTTPClientDoesNotFollowRedirects(t *testing.T) {
	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++ }))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	c := WebhookHTTPClient(&config.NotifierConf{}, 2*time.Second)
	resp, err := c.Get(redirector.URL)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusFound, resp.StatusCode, "the 302 is returned, not followed")
	assert.Zero(t, hits)
}
