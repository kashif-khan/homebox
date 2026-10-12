// Package enrich talks to a scrapling-barcode service, which finds product data
// for a barcode or a product name on the web.
package enrich

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/sysadminsmedia/homebox/backend/internal/data/types"
)

// maxResponseBytes caps how much of the service's answer is read.
const maxResponseBytes = 4 << 20

// Schema asks the service to name its fields the way types.LookupCandidate does,
// so the service needs no knowledge of Homebox.
var Schema = map[string]any{"fields": map[string]any{
	"name":         "name",
	"description":  "description",
	"manufacturer": "manufacturer",
	"modelNumber":  "model_number",
	"country":      "country",
	"barcode":      "barcode",
	"imageURL":     "image_url",
	"imageURLs":    "image_urls",
	"sourceURL":    "source_url",
	"confidence":   "confidence",
	"sources":      "sources",
}}

// ErrUnauthorized means the service refused the token.
var ErrUnauthorized = errors.New("the lookup service rejected the token")

// SourceStatus is whether one of the service's sources is working.
type SourceStatus struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// Status is what the service reports about itself.
type Status struct {
	Sources   []SourceStatus `json:"sources"`
	WebSearch bool           `json:"webSearch"`
}

// Client calls one service instance.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// Result is what a lookup produced: the candidates, and the sources that failed
// along the way (a candidate list can be useful even when one source was down).
type Result struct {
	Candidates []types.LookupCandidate
	Errors     map[string]string
	// SourcesQueried names the sources that were able to take the query. Empty
	// means none could, which is different from searching and finding nothing.
	SourcesQueried []string
}

// Lookup asks the service for up to limit candidates matching query.
func (c Client) Lookup(ctx context.Context, query string, limit int) (Result, error) {
	base, err := url.Parse(c.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return Result{}, errors.New("invalid enrich_url")
	}

	body, err := json.Marshal(map[string]any{"query": query, "limit": limit, "schema": Schema})
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/v1/lookup", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return Result{}, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("enrich service returned status code: %d", resp.StatusCode)
	}

	var answer struct {
		Results        []types.LookupCandidate `json:"results"`
		Errors         map[string]string       `json:"errors"`
		SourcesQueried []string                `json:"sources_queried"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&answer); err != nil {
		return Result{}, err
	}

	out := Result{Errors: answer.Errors, SourcesQueried: answer.SourcesQueried}
	for _, cand := range answer.Results {
		if cand.Name != "" {
			out.Candidates = append(out.Candidates, cand)
		}
	}
	return out, nil
}

// Status asks the service which sources it has and whether each is working. It
// is how a connection is tested without searching for anything.
func (c Client) Status(ctx context.Context) (Status, error) {
	base, err := url.Parse(c.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return Status{}, errors.New("invalid enrich_url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/v1/status", nil)
	if err != nil {
		return Status{}, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Status{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		return Status{}, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		return Status{}, fmt.Errorf("enrich service returned status code: %d", resp.StatusCode)
	}
	var out struct {
		Sources   []SourceStatus `json:"sources"`
		WebSearch bool           `json:"web_search"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&out); err != nil {
		return Status{}, err
	}
	return Status{Sources: out.Sources, WebSearch: out.WebSearch}, nil
}
