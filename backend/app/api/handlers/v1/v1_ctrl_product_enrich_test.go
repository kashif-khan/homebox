package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
)

func newEnrichService(t *testing.T, status int, body string) (*httptest.Server, *http.Request, *map[string]any) {
	t.Helper()
	var got http.Request
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r.Clone(r.Context())
		_ = json.NewDecoder(r.Body).Decode(&sent)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &got, &sent
}

func TestLookupEnrichMapsResultsAndSendsSchemaAndToken(t *testing.T) {
	srv, got, sent := newEnrichService(t, http.StatusOK, `{"results":[
		{"name":"DeWalt Drill","description":"18V","manufacturer":"DeWalt","modelNumber":"DCD777","country":"","barcode":"5035048748428",
		 "imageURL":"https://img/x.jpg","sourceURL":"https://shop/x","confidence":0.65,"sources":["websearch","upcitemdb"]},
		{"name":""}],
		"errors":{"websearch":"timed out"}}`)

	conf := config.BarcodeAPIConf{EnrichURL: srv.URL + "/", EnrichToken: "tok"}
	products, err := lookupEnrich(srv.Client(), conf, "5035048748428")
	if err != nil {
		t.Fatal(err)
	}

	if got.URL.Path != "/v1/lookup" || got.Header.Get("Authorization") != "Bearer tok" {
		t.Fatalf("unexpected request: %s %v", got.URL.Path, got.Header)
	}
	if (*sent)["query"] != "5035048748428" || (*sent)["schema"] == nil {
		t.Fatalf("unexpected body: %v", *sent)
	}
	if len(products) != 1 {
		t.Fatalf("expected the nameless result to be dropped, got %d", len(products))
	}
	p := products[0]
	if p.Item.Name != "DeWalt Drill" || p.Manufacturer != "DeWalt" || p.ModelNumber != "DCD777" ||
		p.Confidence != 0.65 || p.SourceURL != "https://shop/x" || p.SearchEngineName != "websearch, upcitemdb" {
		t.Fatalf("unexpected mapping: %+v", p)
	}
}

func TestLookupEnrichErrors(t *testing.T) {
	srv, _, _ := newEnrichService(t, http.StatusInternalServerError, `{}`)
	if _, err := lookupEnrich(srv.Client(), config.BarcodeAPIConf{EnrichURL: srv.URL}, "x"); err == nil {
		t.Fatal("expected an error for a 500")
	}
	for _, bad := range []string{"", "ftp://host", "not a url", "http://"} {
		if _, err := lookupEnrich(srv.Client(), config.BarcodeAPIConf{EnrichURL: bad}, "x"); err == nil {
			t.Fatalf("expected an error for enrich_url %q", bad)
		}
	}
}

func TestHandleProductEnrichNotConfigured(t *testing.T) {
	handler := (&V1Controller{}).HandleProductEnrich(config.BarcodeAPIConf{EnrichTimeout: time.Second}, &config.NotifierConf{})
	rec := httptest.NewRecorder()
	if err := handler(rec, httptest.NewRequest(http.MethodGet, "/v1/products/enrich?q=abc", nil)); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestHandleProductEnrichReturnsCandidatesAndDropsUnfetchableImages(t *testing.T) {
	srv, _, _ := newEnrichService(t, http.StatusOK, `{"results":[{"name":"Thing","imageURL":"http://insecure/x.jpg","confidence":0.3,"sources":["websearch"]}]}`)
	handler := (&V1Controller{}).HandleProductEnrich(
		config.BarcodeAPIConf{EnrichURL: srv.URL, EnrichTimeout: 5 * time.Second}, &config.NotifierConf{})

	rec := httptest.NewRecorder()
	if err := handler(rec, httptest.NewRequest(http.MethodGet, "/v1/products/enrich?q=thing", nil)); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out []repo.BarcodeProduct
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Item.Name != "Thing" || out[0].ImageURL != "" || out[0].ImageBase64 != "" {
		t.Fatalf("unexpected output: %+v", out)
	}
}

func TestHandleProductEnrichUpstreamFailureIsBadGateway(t *testing.T) {
	srv, _, _ := newEnrichService(t, http.StatusInternalServerError, `{}`)
	handler := (&V1Controller{}).HandleProductEnrich(
		config.BarcodeAPIConf{EnrichURL: srv.URL, EnrichTimeout: 5 * time.Second}, &config.NotifierConf{})
	rec := httptest.NewRecorder()
	_ = handler(rec, httptest.NewRequest(http.MethodGet, "/v1/products/enrich?q=thing", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", rec.Code)
	}
}
