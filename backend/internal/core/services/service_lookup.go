package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/sysadminsmedia/homebox/backend/internal/core/enrich"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/attachment"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/data/types"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
)

const (
	lookupMaxCandidates = 5
	lookupMaxAttempts   = 3
	lookupPollInterval  = 15 * time.Second
	lookupMaxImageBytes = 8 << 20
	lookupMaxQuery      = 200
)

var (
	// ErrLookupDisabled means no product lookup service is configured.
	ErrLookupDisabled = errors.New("product lookup is not configured")
	// ErrLookupNotForLocations: locations have nothing on the web to be found.
	ErrLookupNotForLocations = errors.New("locations cannot be looked up")
	// ErrLookupNotReady means the lookup has not produced candidates to apply.
	ErrLookupNotReady = errors.New("lookup has no results to apply")
	// ErrLookupInvalid wraps problems with what the caller asked to apply.
	ErrLookupInvalid = errors.New("invalid lookup selection")
)

// errNoSourceCanSearch is shown when the lookup service had nothing able to take
// the query, which happens for a name when web search is switched off there.
const errNoSourceCanSearch = "No source can search by name. Turn on web search in the lookup app, or look the item up by its barcode."

// LookupFields are the item details a candidate can fill in. "image" adds the
// candidate's picture as a photo of the item.
var LookupFields = []string{"name", "description", "manufacturer", "modelNumber", "image"}

// LookupService finds product details for items in the background and holds them
// for the user to review. Nothing a lookup finds reaches an item until the user
// applies it.
type LookupService struct {
	repos    *repo.AllRepos
	entities *EntityService
	conf     config.BarcodeAPIConf
	client   enrich.Client
	// images downloads candidate pictures. The addresses come from web pages, so
	// this client enforces the SSRF policy.
	images *http.Client
	wake   chan struct{}
}

func NewLookupService(repos *repo.AllRepos, entities *EntityService, conf config.BarcodeAPIConf, images *http.Client) *LookupService {
	timeout := conf.EnrichTimeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	return &LookupService{
		repos:    repos,
		entities: entities,
		conf:     conf,
		client:   enrich.Client{BaseURL: conf.EnrichURL, Token: conf.EnrichToken, HTTP: &http.Client{Timeout: timeout}},
		images:   images,
		wake:     make(chan struct{}, 1),
	}
}

// Enabled reports whether a lookup service is configured.
func (s *LookupService) Enabled() bool { return s != nil && s.conf.EnrichURL != "" }

// Auto reports whether new items are looked up without being asked.
func (s *LookupService) Auto() bool { return s.Enabled() && s.conf.EnrichAuto }

// LookupQuery is what an item is searched for: the maker and model when both are
// known, as that is what pins a product down, and otherwise whatever there is.
func LookupQuery(e repo.EntityOut) string {
	name, maker, model := strings.TrimSpace(e.Name), strings.TrimSpace(e.Manufacturer), strings.TrimSpace(e.ModelNumber)
	var parts []string
	if maker != "" && model != "" {
		parts = []string{maker, model}
	} else {
		for _, p := range []string{maker, model, name} {
			if p != "" {
				parts = append(parts, p)
			}
		}
	}
	q := strings.Join(parts, " ")
	if len(q) > lookupMaxQuery {
		q = q[:lookupMaxQuery]
	}
	return q
}

// Request queues a lookup for an item, replacing any earlier one.
func (s *LookupService) Request(ctx Context, entityID uuid.UUID) (repo.EntityLookupOut, error) {
	if !s.Enabled() {
		return repo.EntityLookupOut{}, ErrLookupDisabled
	}
	e, err := s.repos.Entities.GetOneByGroup(ctx, ctx.GID, entityID)
	if err != nil {
		return repo.EntityLookupOut{}, err
	}
	if e.EntityType != nil && e.EntityType.IsLocation {
		return repo.EntityLookupOut{}, ErrLookupNotForLocations
	}
	query := LookupQuery(e)
	if query == "" {
		return repo.EntityLookupOut{}, fmt.Errorf("%w: the item has nothing to search for", ErrLookupInvalid)
	}
	out, err := s.repos.EntityLookups.Request(ctx, ctx.GID, entityID, query)
	if err != nil {
		return repo.EntityLookupOut{}, err
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return out, nil
}

// RequestForNew queues a lookup for an item that was just created, when lookups
// are automatic. It never fails the creation: the worst case is no lookup.
func (s *LookupService) RequestForNew(ctx Context, e repo.EntityOut) {
	if !s.Auto() {
		return
	}
	if _, err := s.Request(ctx, e.ID); err != nil && !errors.Is(err, ErrLookupNotForLocations) {
		log.Warn().Err(err).Str("entity", e.ID.String()).Msg("could not queue product lookup for new item")
	}
}

// Run works through queued lookups until ctx ends. Lookups live in the database,
// so ones queued before a restart are picked up again.
func (s *LookupService) Run(ctx context.Context) error {
	if !s.Enabled() {
		return nil
	}
	ticker := time.NewTicker(lookupPollInterval)
	defer ticker.Stop()
	for {
		for ctx.Err() == nil {
			worked, err := s.runOne(ctx)
			if err != nil {
				log.Error().Err(err).Msg("product lookup worker")
			}
			if !worked || err != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-s.wake:
		case <-ticker.C:
		}
	}
}

// runOne handles the oldest queued lookup, and reports whether there was one.
func (s *LookupService) runOne(ctx context.Context) (bool, error) {
	job, err := s.repos.EntityLookups.Claim(ctx)
	if err != nil || job == nil {
		return false, err
	}
	if job.Attempts > lookupMaxAttempts {
		return true, s.repos.EntityLookups.Fail(ctx, job.ID, "Gave up after repeated attempts.")
	}

	result, err := s.client.Lookup(ctx, job.Query, lookupMaxCandidates)
	if err != nil {
		log.Warn().Err(err).Str("entity", job.EntityID.String()).Msg("product lookup failed")
		return true, s.repos.EntityLookups.Fail(ctx, job.ID, "The lookup service could not be reached.")
	}
	for source, msg := range result.Errors {
		log.Warn().Str("source", source).Msg("product lookup source: " + msg)
	}
	if len(result.Candidates) == 0 && len(result.SourcesQueried) == 0 {
		return true, s.repos.EntityLookups.Fail(ctx, job.ID, errNoSourceCanSearch)
	}
	candidates := make([]types.LookupCandidate, 0, len(result.Candidates))
	for _, c := range result.Candidates {
		if !strings.HasPrefix(c.ImageURL, "https://") {
			c.ImageURL = ""
		}
		candidates = append(candidates, c)
	}
	return true, s.repos.EntityLookups.Complete(ctx, job.ID, candidates)
}

// LookupApply says which candidate to use and which of its details to take.
type LookupApply struct {
	Candidate int      `json:"candidate"`
	Fields    []string `json:"fields"`
}

// Apply fills the chosen details of a candidate into the item, then closes the
// review. Details not listed are left exactly as they are.
func (s *LookupService) Apply(ctx Context, entityID uuid.UUID, in LookupApply) (repo.EntityOut, error) {
	if len(in.Fields) == 0 {
		return repo.EntityOut{}, fmt.Errorf("%w: choose at least one detail", ErrLookupInvalid)
	}
	for _, f := range in.Fields {
		if !slices.Contains(LookupFields, f) {
			return repo.EntityOut{}, fmt.Errorf("%w: unknown detail %q", ErrLookupInvalid, f)
		}
	}

	lk, err := s.repos.EntityLookups.Get(ctx, ctx.GID, entityID)
	if err != nil {
		return repo.EntityOut{}, err
	}
	if lk.Status != "done" {
		return repo.EntityOut{}, ErrLookupNotReady
	}
	if in.Candidate < 0 || in.Candidate >= len(lk.Candidates) {
		return repo.EntityOut{}, fmt.Errorf("%w: no such match", ErrLookupInvalid)
	}
	cand := lk.Candidates[in.Candidate]

	e, err := s.repos.Entities.GetOneByGroup(ctx, ctx.GID, entityID)
	if err != nil {
		return repo.EntityOut{}, err
	}

	// Fetched first, so a picture that cannot be had stops the whole thing
	// before the item has been touched.
	var image []byte
	var imageName string
	if slices.Contains(in.Fields, "image") {
		if cand.ImageURL == "" {
			return repo.EntityOut{}, fmt.Errorf("%w: this match has no picture", ErrLookupInvalid)
		}
		if image, imageName, err = s.fetchImage(ctx, cand.ImageURL); err != nil {
			return repo.EntityOut{}, fmt.Errorf("could not download the picture: %w", err)
		}
	}

	update := e.AsUpdate()
	for _, f := range in.Fields {
		switch f {
		case "name":
			if cand.Name != "" {
				update.Name = truncate(cand.Name, 255)
			}
		case "description":
			update.Description = truncate(cand.Description, 1000)
		case "manufacturer":
			update.Manufacturer = cand.Manufacturer
		case "modelNumber":
			update.ModelNumber = cand.ModelNumber
		}
	}
	if len(in.Fields) > 1 || in.Fields[0] != "image" {
		if _, err := s.repos.Entities.UpdateByGroup(ctx, ctx.GID, update); err != nil {
			return repo.EntityOut{}, err
		}
	}

	if image != nil {
		hasPrimary := false
		for _, a := range e.Attachments {
			hasPrimary = hasPrimary || (a.Primary && a.Type == attachment.TypePhoto.String())
		}
		if _, err := s.entities.AttachmentAdd(ctx, entityID, imageName, attachment.TypePhoto, !hasPrimary, bytes.NewReader(image)); err != nil {
			return repo.EntityOut{}, err
		}
	}

	if err := s.repos.EntityLookups.Delete(ctx, ctx.GID, entityID); err != nil {
		log.Warn().Err(err).Msg("could not close the lookup after applying it")
	}
	return s.repos.Entities.GetOneByGroup(ctx, ctx.GID, entityID)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

func (s *LookupService) fetchImage(ctx context.Context, imageURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := s.images.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, lookupMaxImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(raw) > lookupMaxImageBytes {
		return nil, "", errors.New("picture is too large")
	}
	switch http.DetectContentType(raw) {
	case "image/jpeg":
		return raw, "product.jpg", nil
	case "image/png":
		return raw, "product.png", nil
	case "image/webp":
		return raw, "product.webp", nil
	case "image/gif":
		return raw, "product.gif", nil
	}
	return nil, "", errors.New("not a supported picture")
}
