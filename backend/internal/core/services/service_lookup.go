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
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entitylookup"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/data/types"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
)

const (
	lookupMaxCandidates = 5
	lookupMaxImages     = 5
	lookupMaxAttempts   = 3
	lookupPollInterval  = 15 * time.Second
	lookupMaxImageBytes = 8 << 20
	lookupMaxQuery      = 200
	lookupTestTimeout   = 10 * time.Second
	lookupDefaultWait   = 90 * time.Second
	// lookupBulkLimit is how many items one bulk request queues. The rest are
	// still there for the next one.
	lookupBulkLimit = 500
	lookupListLimit = 200
)

var (
	// ErrLookupDisabled means no product lookup service is configured.
	ErrLookupDisabled = errors.New("product lookup is not configured")
	// ErrLookupNotForLocations: locations have nothing on the web to be found.
	ErrLookupNotForLocations = errors.New("locations cannot be looked up")
	// ErrLookupNotReady means the lookup has not produced candidates to apply.
	ErrLookupNotReady = errors.New("lookup has no results to apply")
	// ErrLookupInvalid wraps problems with what the caller asked for.
	ErrLookupInvalid = errors.New("invalid lookup selection")
	// ErrLookupPicture means a picture of a match could not be had: the site
	// refused, was down, or sent something that is not a picture.
	ErrLookupPicture = errors.New("the picture could not be downloaded")
)

// lookupUserAgent identifies Homebox to the sites pictures are fetched from, which
// often refuse Go's default.
const lookupUserAgent = "Homebox product lookup (+https://github.com/sysadminsmedia/homebox)"

// lookupAccept asks for the formats everything can show. Sites that serve AVIF to
// anything that does not say otherwise would otherwise send that.
const lookupAccept = "image/png,image/jpeg,image/webp,image/gif;q=0.9"

// LookupFields are the item details a candidate can fill in. "image" adds one of
// the candidate's pictures as a photo of the item.
var LookupFields = []string{"name", "description", "manufacturer", "modelNumber", "image"}

// LookupMissing are the details bulk lookups can be aimed at.
var LookupMissing = []string{"description", "manufacturer", "modelNumber", "photo"}

// errNoSourceCanSearch is shown when the lookup service had nothing able to take
// the query, which happens for a name when web search is switched off there.
const errNoSourceCanSearch = "No source can search by name. Turn on web search in the lookup app, or look the item up by its barcode."

// LookupService finds product details for items in the background and holds them
// for the user to review. Nothing a lookup finds reaches an item until the user
// applies it.
//
// Which service to ask is a setting of the collection, set by its owner in the
// web UI. Where a collection has not set one, the server's own setting is used,
// so an operator can still configure it once for everybody.
type LookupService struct {
	repos    *repo.AllRepos
	entities *EntityService
	env      config.BarcodeAPIConf
	notifier *config.NotifierConf
	// serverHTTP calls the service named in the server's own settings, which are
	// the operator's and trusted. ownerHTTP calls one an owner typed in, so it
	// enforces the network policy.
	serverHTTP *http.Client
	ownerHTTP  *http.Client
	// images downloads candidate pictures. The addresses come from web pages, so
	// this client enforces the SSRF policy.
	images *http.Client
	wake   chan struct{}
}

func NewLookupService(repos *repo.AllRepos, entities *EntityService, env config.BarcodeAPIConf, notifier *config.NotifierConf, images *http.Client) *LookupService {
	timeout := env.EnrichTimeout
	if timeout <= 0 {
		timeout = lookupDefaultWait
	}
	if notifier == nil {
		// Same policy as the configuration defaults: loopback is refused too.
		notifier = defaultNotifierConf()
		notifier.BlockLocalhost = true
	}
	return &LookupService{
		repos:      repos,
		entities:   entities,
		env:        env,
		notifier:   notifier,
		serverHTTP: &http.Client{Timeout: timeout},
		ownerHTTP:  validate.LookupHTTPClient(notifier, timeout),
		images:     images,
		wake:       make(chan struct{}, 1),
	}
}

// lookupTarget is where a collection's lookups go.
type lookupTarget struct {
	client     enrich.Client
	auto       bool
	fromServer bool
}

func (t lookupTarget) configured() bool { return t.client.BaseURL != "" }

// target works out which service a collection uses.
func (s *LookupService) target(ctx context.Context, gid uuid.UUID) (lookupTarget, error) {
	set, err := s.repos.EntityLookups.GetSettings(ctx, gid)
	if err != nil {
		return lookupTarget{}, err
	}
	if set.URL != "" {
		return lookupTarget{client: enrich.Client{BaseURL: set.URL, Token: set.Token, HTTP: s.ownerHTTP}, auto: set.Auto}, nil
	}
	if s.env.EnrichURL != "" {
		return lookupTarget{
			client:     enrich.Client{BaseURL: s.env.EnrichURL, Token: s.env.EnrichToken, HTTP: s.serverHTTP},
			auto:       s.env.EnrichAuto,
			fromServer: true,
		}, nil
	}
	return lookupTarget{}, nil
}

// LookupSettingsOut is a collection's lookup settings as the UI sees them. The
// token is never sent back, only whether there is one.
type LookupSettingsOut struct {
	URL        string `json:"url"`
	TokenSet   bool   `json:"tokenSet"`
	Auto       bool   `json:"auto"`
	Configured bool   `json:"configured"`
	// FromServer is true when the address comes from the server's own settings
	// rather than from the collection.
	FromServer bool `json:"fromServer"`
}

// LookupSettingsIn is what the settings form sends. A nil Token keeps the stored
// one; an empty one clears it.
type LookupSettingsIn struct {
	URL   string  `json:"url"   validate:"max=2083"`
	Token *string `json:"token" validate:"omitempty,max=512" extensions:"x-nullable"`
	Auto  bool    `json:"auto"`
}

// LookupTestOut is the result of trying a connection.
type LookupTestOut struct {
	OK        bool                  `json:"ok"`
	Message   string                `json:"message"`
	Sources   []enrich.SourceStatus `json:"sources"`
	WebSearch bool                  `json:"webSearch"`
}

// Settings returns a collection's lookup settings.
func (s *LookupService) Settings(ctx Context) (LookupSettingsOut, error) {
	set, err := s.repos.EntityLookups.GetSettings(ctx, ctx.GID)
	if err != nil {
		return LookupSettingsOut{}, err
	}
	t, err := s.target(ctx, ctx.GID)
	if err != nil {
		return LookupSettingsOut{}, err
	}
	return LookupSettingsOut{
		URL: t.client.BaseURL, TokenSet: t.client.Token != "" || set.Token != "",
		Auto: t.auto, Configured: t.configured(), FromServer: t.fromServer,
	}, nil
}

// SaveSettings stores a collection's lookup settings. An empty address switches
// the collection back to the server's setting, if there is one.
func (s *LookupService) SaveSettings(ctx Context, in LookupSettingsIn) (LookupSettingsOut, error) {
	in.URL = strings.TrimSpace(in.URL)
	if in.URL != "" {
		if err := validate.ValidateLookupURL(in.URL, s.notifier); err != nil {
			return LookupSettingsOut{}, fmt.Errorf("%w: %s", ErrLookupInvalid, err.Error())
		}
	}
	token := in.Token
	if in.URL == "" {
		empty := ""
		token = &empty // nothing to authenticate to
	}
	if err := s.repos.EntityLookups.SetSettings(ctx, ctx.GID, in.URL, token, in.Auto && in.URL != ""); err != nil {
		return LookupSettingsOut{}, err
	}
	return s.Settings(ctx)
}

// TestConnection asks a lookup service whether it is up, accepts the token and
// has working sources, using the form's values when given and otherwise what is
// already in use. It never fails: a connection that does not work is the answer.
func (s *LookupService) TestConnection(ctx Context, in LookupSettingsIn) LookupTestOut {
	bounded, cancel := context.WithTimeout(ctx.Context, lookupTestTimeout)
	defer cancel()
	ctx.Context = bounded
	in.URL = strings.TrimSpace(in.URL)

	var client enrich.Client
	if in.URL != "" {
		if err := validate.ValidateLookupURL(in.URL, s.notifier); err != nil {
			return LookupTestOut{Message: capitalize(err.Error()) + "."}
		}
		token := ""
		if in.Token != nil {
			token = *in.Token
		} else if set, err := s.repos.EntityLookups.GetSettings(ctx, ctx.GID); err == nil {
			token = set.Token
		}
		client = enrich.Client{BaseURL: in.URL, Token: token, HTTP: s.ownerHTTP}
	} else {
		t, err := s.target(ctx, ctx.GID)
		if err != nil || !t.configured() {
			return LookupTestOut{Message: "Enter the address of the lookup service first."}
		}
		client = t.client
	}

	status, err := client.Status(ctx)
	switch {
	case errors.Is(err, enrich.ErrUnauthorized):
		return LookupTestOut{Message: "The lookup service is there, but it rejected the token."}
	case err != nil:
		log.Debug().Err(err).Msg("product lookup connection test failed")
		return LookupTestOut{Message: "Could not reach the lookup service. Check the address, and that it is running."}
	}
	msg := "Connected."
	if !status.WebSearch {
		msg = "Connected, but web search is off, so items can only be found by barcode."
	}
	return LookupTestOut{OK: true, Message: msg, Sources: status.Sources, WebSearch: status.WebSearch}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Configured reports whether a collection has a lookup service to ask.
func (s *LookupService) Configured(ctx context.Context, gid uuid.UUID) bool {
	t, err := s.target(ctx, gid)
	return err == nil && t.configured()
}

// LookupQuery is what an item is searched for: the maker and model when both are
// known, as that is what pins a product down, and otherwise whatever there is.
func LookupQuery(e repo.EntityOut) string {
	return lookupQuery(e.Name, e.Manufacturer, e.ModelNumber)
}

func lookupQuery(name, maker, model string) string {
	name, maker, model = strings.TrimSpace(name), strings.TrimSpace(maker), strings.TrimSpace(model)
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

func (s *LookupService) wakeWorker() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Request queues a lookup for an item, replacing any earlier one.
func (s *LookupService) Request(ctx Context, entityID uuid.UUID) (repo.EntityLookupOut, error) {
	if !s.Configured(ctx, ctx.GID) {
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
	s.wakeWorker()
	return out, nil
}

// RequestForNew queues a lookup for an item that was just created, when its
// collection looks new items up automatically. It never fails the creation: the
// worst case is no lookup.
func (s *LookupService) RequestForNew(ctx Context, e repo.EntityOut) {
	t, err := s.target(ctx, ctx.GID)
	if err != nil || !t.configured() || !t.auto {
		return
	}
	if _, err := s.Request(ctx, e.ID); err != nil && !errors.Is(err, ErrLookupNotForLocations) {
		log.Warn().Err(err).Str("entity", e.ID.String()).Msg("could not queue product lookup for new item")
	}
}

// LookupQueueOut says what a bulk request found and did.
type LookupQueueOut struct {
	// Matched is how many items are missing the chosen details and have no lookup.
	Matched int `json:"matched"`
	// Queued is how many of them were queued by this request.
	Queued int `json:"queued"`
}

// QueueMissing queues lookups for the items that are missing any of the chosen
// details (description, manufacturer, modelNumber, photo), skipping locations,
// archived items and ones that already have a lookup. With dryRun it only counts.
func (s *LookupService) QueueMissing(ctx Context, missing []string, dryRun bool) (LookupQueueOut, error) {
	if len(missing) == 0 {
		return LookupQueueOut{}, fmt.Errorf("%w: choose at least one detail", ErrLookupInvalid)
	}
	for _, m := range missing {
		if !slices.Contains(LookupMissing, m) {
			return LookupQueueOut{}, fmt.Errorf("%w: unknown detail %q", ErrLookupInvalid, m)
		}
	}
	if !dryRun && !s.Configured(ctx, ctx.GID) {
		return LookupQueueOut{}, ErrLookupDisabled
	}

	subjects, total, err := s.repos.EntityLookups.Missing(ctx, ctx.GID, missing, lookupBulkLimit)
	if err != nil {
		return LookupQueueOut{}, err
	}
	out := LookupQueueOut{Matched: total}
	if dryRun {
		return out, nil
	}

	queries := make(map[uuid.UUID]string, len(subjects))
	for _, sub := range subjects {
		if q := lookupQuery(sub.Name, sub.Manufacturer, sub.ModelNumber); q != "" {
			queries[sub.ID] = q
		}
	}
	out.Queued, err = s.repos.EntityLookups.QueueMany(ctx, ctx.GID, queries)
	if err != nil {
		return out, err
	}
	s.wakeWorker()
	return out, nil
}

// RetryFailed puts a collection's failed lookups back in the queue.
func (s *LookupService) RetryFailed(ctx Context) (int, error) {
	if !s.Configured(ctx, ctx.GID) {
		return 0, ErrLookupDisabled
	}
	n, err := s.repos.EntityLookups.RetryFailed(ctx, ctx.GID)
	if err == nil && n > 0 {
		s.wakeWorker()
	}
	return n, err
}

// Clear dismisses a collection's finished lookups: "done" (waiting for review),
// "failed", or "finished" for both. Ones still searching are left alone.
func (s *LookupService) Clear(ctx Context, which string) (int, error) {
	var statuses []entitylookup.Status
	switch which {
	case "done":
		statuses = []entitylookup.Status{entitylookup.StatusDone}
	case "failed":
		statuses = []entitylookup.Status{entitylookup.StatusFailed}
	case "finished":
		statuses = []entitylookup.Status{entitylookup.StatusDone, entitylookup.StatusFailed}
	default:
		return 0, fmt.Errorf("%w: choose done, failed or finished", ErrLookupInvalid)
	}
	return s.repos.EntityLookups.DeleteByStatus(ctx, ctx.GID, statuses...)
}

// LookupActivityOut is the activity list: what the lookups are doing.
type LookupActivityOut struct {
	Items  []repo.EntityLookupListItem `json:"items"`
	Counts repo.EntityLookupCounts     `json:"counts"`
}

// Activity lists a collection's lookups, optionally of one status.
func (s *LookupService) Activity(ctx Context, status string) (LookupActivityOut, error) {
	if status != "" && !slices.Contains([]string{"pending", "done", "failed"}, status) {
		return LookupActivityOut{}, fmt.Errorf("%w: unknown status", ErrLookupInvalid)
	}
	items, counts, err := s.repos.EntityLookups.List(ctx, ctx.GID, status, lookupListLimit)
	return LookupActivityOut{Items: items, Counts: counts}, err
}

// Run works through queued lookups until ctx ends. Lookups live in the database,
// so ones queued before a restart are picked up again. It runs whether or not a
// service is configured, because that can be set from the web UI at any time.
func (s *LookupService) Run(ctx context.Context) error {
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

	t, err := s.target(ctx, job.GroupID)
	if err != nil {
		return true, s.repos.EntityLookups.Fail(ctx, job.ID, "Could not read this collection's lookup settings.")
	}
	if !t.configured() {
		return true, s.repos.EntityLookups.Fail(ctx, job.ID, "Product lookup is not set up. Add the address of the lookup service under Collection, Product lookup.")
	}

	result, err := t.client.Lookup(ctx, job.Query, lookupMaxCandidates)
	switch {
	case errors.Is(err, enrich.ErrUnauthorized):
		return true, s.repos.EntityLookups.Fail(ctx, job.ID, "The lookup service rejected the token.")
	case err != nil:
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
		c.ImageURLs = safeImages(c)
		c.ImageURL = ""
		if len(c.ImageURLs) > 0 {
			c.ImageURL = c.ImageURLs[0]
		}
		candidates = append(candidates, c)
	}
	return true, s.repos.EntityLookups.Complete(ctx, job.ID, candidates)
}

// safeImages keeps the https pictures of a candidate, without repeats.
func safeImages(c types.LookupCandidate) []string {
	out := []string{} // never nil, so it is stored and sent as [] rather than null
	for _, u := range append([]string{c.ImageURL}, c.ImageURLs...) {
		if strings.HasPrefix(u, "https://") && !slices.Contains(out, u) && len(out) < lookupMaxImages {
			out = append(out, u)
		}
	}
	return out
}

// LookupApply says which candidate to use and which of its details to take.
type LookupApply struct {
	Candidate int      `json:"candidate"`
	Fields    []string `json:"fields"`
	// ImageIndex chooses which of the candidate's pictures to add, when "image"
	// is among the fields.
	ImageIndex int `json:"imageIndex"`
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
		pics := candidatePictures(cand)
		if in.ImageIndex < 0 || in.ImageIndex >= len(pics) {
			return repo.EntityOut{}, fmt.Errorf("%w: this match has no such picture", ErrLookupInvalid)
		}
		var ext string
		if image, _, ext, err = s.fetchImage(ctx, pics[in.ImageIndex]); err != nil {
			return repo.EntityOut{}, err
		}
		imageName = "product" + ext
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

// candidatePictures lists a candidate's pictures, including ones stored before
// candidates carried a list.
func candidatePictures(c types.LookupCandidate) []string {
	return safeImages(c)
}

// CandidateImage downloads one picture of a candidate so that it can be shown
// through Homebox: the browser then never contacts the site it came from. Only
// addresses stored with the lookup can be fetched, never one the caller names.
func (s *LookupService) CandidateImage(ctx Context, entityID uuid.UUID, candidate, index int) ([]byte, string, error) {
	lk, err := s.repos.EntityLookups.Get(ctx, ctx.GID, entityID)
	if err != nil {
		return nil, "", err
	}
	if candidate < 0 || candidate >= len(lk.Candidates) {
		return nil, "", fmt.Errorf("%w: no such match", ErrLookupInvalid)
	}
	pics := candidatePictures(lk.Candidates[candidate])
	if index < 0 || index >= len(pics) {
		return nil, "", fmt.Errorf("%w: no such picture", ErrLookupInvalid)
	}
	data, mime, _, err := s.fetchImage(ctx, pics[index])
	return data, mime, err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

// fetchImage downloads a picture, returning it with its type and file extension.
// Every way it can fail is reported as ErrLookupPicture.
func (s *LookupService) fetchImage(ctx context.Context, imageURL string) ([]byte, string, string, error) {
	fail := func(err error) ([]byte, string, string, error) {
		log.Debug().Err(err).Str("url", imageURL).Msg("product lookup picture")
		return nil, "", "", ErrLookupPicture
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return fail(err)
	}
	req.Header.Set("User-Agent", lookupUserAgent)
	req.Header.Set("Accept", lookupAccept)
	resp, err := s.images.Do(req)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fail(fmt.Errorf("status %d", resp.StatusCode))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, lookupMaxImageBytes+1))
	if err != nil {
		return fail(err)
	}
	if len(raw) > lookupMaxImageBytes {
		return fail(errors.New("picture is too large"))
	}
	switch mime := http.DetectContentType(raw); mime {
	case "image/jpeg":
		return raw, mime, ".jpg", nil
	case "image/png":
		return raw, mime, ".png", nil
	case "image/webp":
		return raw, mime, ".webp", nil
	case "image/gif":
		return raw, mime, ".gif", nil
	}
	// Go's sniffer does not know AVIF: an "ftyp" box naming avif or avis.
	if len(raw) > 12 && string(raw[4:8]) == "ftyp" && (string(raw[8:12]) == "avif" || string(raw[8:12]) == "avis") {
		return raw, "image/avif", ".avif", nil
	}
	return fail(errors.New("not a supported picture"))
}
