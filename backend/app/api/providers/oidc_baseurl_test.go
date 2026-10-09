package providers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
)

func TestOIDCProviderGetBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		options config.Options
		headers map[string]string
		want    string
	}{
		{
			name:    "hostname without scheme takes the scheme from the proxy",
			options: config.Options{Hostname: "homebox.example.com", TrustProxy: true},
			headers: map[string]string{"X-Forwarded-Proto": "https"},
			want:    "https://homebox.example.com",
		},
		{
			name:    "hostname with scheme is used as it is",
			options: config.Options{Hostname: "https://homebox.example.com", TrustProxy: true},
			want:    "https://homebox.example.com",
		},
		{
			name:    "trailing slash is dropped",
			options: config.Options{Hostname: "https://homebox.example.com/"},
			want:    "https://homebox.example.com",
		},
		{
			name:    "forwarded host is used without a hostname",
			options: config.Options{TrustProxy: true},
			headers: map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-Host": "proxy.example.com"},
			want:    "https://proxy.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &OIDCProvider{options: &tt.options}
			r := httptest.NewRequest(http.MethodGet, "http://backend.local/", nil)
			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}

			if got := p.getBaseURL(r); got != tt.want {
				t.Fatalf("getBaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
