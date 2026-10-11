package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/sysadminsmedia/homebox/backend/internal/core/alerts"
	"github.com/sysadminsmedia/homebox/backend/internal/core/regional"
	"github.com/sysadminsmedia/homebox/backend/internal/core/webhooks"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/config"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
)

// WebhookService queues events for subscribed endpoints and delivers them with
// retries. Events are written to the database as they happen, so a restart or an
// outage at the receiver loses nothing: whatever is still owed is sent when the
// worker next runs, and the body says how late it is.
type WebhookService struct {
	repos    *repo.AllRepos
	cfg      config.WebhooksConf
	notifier *config.NotifierConf

	client *http.Client
	now    func() time.Time
	wake   chan struct{}

	mu   sync.Mutex
	sent map[uuid.UUID][]time.Time // recent attempts per endpoint, for the rate cap
}

// NewWebhookService builds the service. It does nothing until Run is started and
// is inert when webhooks are disabled.
func NewWebhookService(repos *repo.AllRepos, cfg config.WebhooksConf, notifier *config.NotifierConf) *WebhookService {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &WebhookService{
		repos:    repos,
		cfg:      cfg,
		notifier: notifier,
		client:   validate.WebhookHTTPClient(notifier, timeout),
		now:      time.Now,
		wake:     make(chan struct{}, 1),
		sent:     map[uuid.UUID][]time.Time{},
	}
}

// Enabled reports whether webhooks are switched on for this instance.
func (s *WebhookService) Enabled() bool { return s != nil && s.cfg.Enabled }

// Config returns the settings, for the UI to show limits.
func (s *WebhookService) Config() config.WebhooksConf { return s.cfg }

func (s *WebhookService) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// matches reports whether an endpoint wants an item event, by its optional type
// and tag filters. Events with no item pass.
func matches(ep repo.WebhookEndpointFull, typeID uuid.UUID, tagIDs []uuid.UUID) bool {
	if ep.EntityTypeID != nil && typeID != *ep.EntityTypeID {
		return false
	}
	if ep.TagID != nil {
		for _, t := range tagIDs {
			if t == *ep.TagID {
				return true
			}
		}
		return false
	}
	return true
}

// Emit implements repo.EventSink: it queues the event for every subscribed
// endpoint of the collection.
func (s *WebhookService) Emit(ctx context.Context, ev repo.DomainEvent) {
	if !s.Enabled() {
		return
	}
	subs, err := s.repos.Webhooks.Subscribers(ctx, ev.GID, ev.Type)
	if err != nil {
		log.Err(err).Str("event", ev.Type).Msg("webhooks: could not look up subscribers")
		return
	}
	eventID, now, queued := uuid.New(), s.now(), 0
	if ev.OccurredAt.IsZero() {
		ev.OccurredAt = now
	}
	for _, ep := range subs {
		// Item filters apply to events about an item, not to backups.
		if ev.EntityTypeID != uuid.Nil || len(ev.TagIDs) > 0 {
			if !matches(ep, ev.EntityTypeID, ev.TagIDs) {
				continue
			}
		}
		_, ok, err := s.repos.Webhooks.Queue(ctx, ep.ID, repo.WebhookNewDelivery{
			EventID: eventID, EventType: ev.Type, OccurredAt: ev.OccurredAt, Data: ev.Data,
		}, now, now)
		if err != nil {
			log.Err(err).Str("endpoint_id", ep.ID.String()).Msg("webhooks: could not queue delivery")
			continue
		}
		if ok {
			queued++
		}
	}
	if queued > 0 {
		s.poke()
	}
}

// Alerts implements AlertSink: date alerts become expiry, warranty and
// maintenance events. A tick can offer the same alert more than once, so each
// is keyed and queued once per endpoint.
func (s *WebhookService) Alerts(ctx context.Context, g repo.Group, now time.Time, due []alerts.Alert) {
	if !s.Enabled() || len(due) == 0 {
		return
	}
	loc := regional.Location(g.Timezone)
	subsByEvent := map[string][]repo.WebhookEndpointFull{}
	queued := 0
	for _, a := range due {
		name := webhooks.EventForAlert(a)
		if name == "" {
			continue
		}
		subs, ok := subsByEvent[name]
		if !ok {
			var err error
			if subs, err = s.repos.Webhooks.Subscribers(ctx, g.ID, name); err != nil {
				log.Err(err).Str("event", name).Msg("webhooks: could not look up subscribers")
				continue
			}
			subsByEvent[name] = subs
		}
		if len(subs) == 0 {
			continue
		}
		when := a.ScheduledFor
		eventID, data, key := uuid.New(), webhooks.AlertData(a, loc), webhooks.AlertKey(a)
		for _, ep := range subs {
			_, created, err := s.repos.Webhooks.Queue(ctx, ep.ID, repo.WebhookNewDelivery{
				EventID: eventID, EventType: name, OccurredAt: when, Scheduled: &when, Data: data, DedupeKey: key,
			}, s.now(), s.now())
			if err != nil {
				log.Err(err).Str("endpoint_id", ep.ID.String()).Msg("webhooks: could not queue alert delivery")
				continue
			}
			if created {
				queued++
			}
		}
	}
	if queued > 0 {
		s.poke()
	}
}

// EmitBackup tells subscribers a backup finished or failed.
func (s *WebhookService) EmitBackup(ctx context.Context, gid uuid.UUID, ok bool, data map[string]any) {
	typ := webhooks.BackupFailed
	if ok {
		typ = webhooks.BackupCompleted
	}
	s.Emit(ctx, repo.DomainEvent{GID: gid, Type: typ, OccurredAt: s.now(), Data: data})
}

// Run delivers queued events until ctx ends.
func (s *WebhookService) Run(ctx context.Context) error {
	if !s.Enabled() {
		<-ctx.Done()
		return nil
	}
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	purge := time.NewTicker(time.Hour)
	defer purge.Stop()
	s.processDue(ctx) // whatever was owed when the server was last stopped
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			s.processDue(ctx)
		case <-s.wake:
			s.processDue(ctx)
		case <-purge.C:
			keep := s.cfg.KeepDeliveries
			if keep <= 0 {
				keep = 7 * 24 * time.Hour
			}
			if n, err := s.repos.Webhooks.PurgeDelivered(ctx, s.now().Add(-keep)); err != nil {
				log.Err(err).Msg("webhooks: purge failed")
			} else if n > 0 {
				log.Debug().Int("removed", n).Msg("webhooks: purged old deliveries")
			}
		}
	}
}

const (
	batchSize = 50
	workers   = 8
)

// ProcessDue sends everything that is due now. Exposed for tests.
func (s *WebhookService) ProcessDue(ctx context.Context) { s.processDue(ctx) }

func (s *WebhookService) processDue(ctx context.Context) {
	for {
		due, err := s.repos.Webhooks.Due(ctx, s.now(), batchSize)
		if err != nil {
			log.Err(err).Msg("webhooks: could not load due deliveries")
			return
		}
		if len(due) == 0 {
			return
		}
		var wg sync.WaitGroup
		sem := make(chan struct{}, workers)
		for _, d := range due {
			// Hold the delivery so a slow receiver is not picked up twice.
			lease := s.now().Add(s.cfg.Timeout + 30*time.Second)
			if err := s.repos.Webhooks.Lease(ctx, d.ID, lease); err != nil {
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				s.attempt(ctx, d, false)
			}()
		}
		wg.Wait()
		if len(due) < batchSize {
			return
		}
	}
}

// rateLimited reports whether an endpoint has had its per-minute budget; if not
// it counts this attempt.
func (s *WebhookService) rateLimited(endpoint uuid.UUID) bool {
	if s.cfg.RatePerMinute <= 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := s.now().Add(-time.Minute)
	recent := s.sent[endpoint][:0]
	for _, t := range s.sent[endpoint] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= s.cfg.RatePerMinute {
		s.sent[endpoint] = recent
		return true
	}
	s.sent[endpoint] = append(recent, s.now())
	return false
}

// Result is the outcome of one attempt, for the "send test" button.
type WebhookResult struct {
	OK         bool   `json:"ok"`
	StatusCode int    `json:"statusCode"`
	Error      string `json:"error,omitempty"`
	DurationMs int    `json:"durationMs"`
}

func (s *WebhookService) attempt(ctx context.Context, d repo.WebhookDeliveryFull, once bool) WebhookResult {
	ep, err := s.repos.Webhooks.GetFull(ctx, d.EndpointID)
	if err != nil {
		return WebhookResult{Error: "endpoint no longer exists"}
	}
	now := s.now()
	fail := func(a repo.Attempt, retry bool) WebhookResult {
		var next *time.Time
		if retry && !once {
			if t, ok := webhooks.NextAttempt(d.Attempts+1, d.CreatedAt, now, s.cfg.MaxAge); ok {
				next = &t
			}
		}
		if err := s.repos.Webhooks.RecordFailure(ctx, d.ID, a, next); err != nil {
			log.Err(err).Str("delivery_id", d.ID.String()).Msg("webhooks: could not record failure")
		}
		return WebhookResult{StatusCode: a.StatusCode, Error: a.Err, DurationMs: int(a.Duration.Milliseconds())}
	}

	if !ep.IsActive && !once {
		return fail(repo.Attempt{Err: "endpoint is switched off"}, false)
	}
	if s.rateLimited(ep.ID) {
		// Not a failed attempt: just not yet.
		later := now.Add(30 * time.Second)
		if err := s.repos.Webhooks.Lease(ctx, d.ID, later); err != nil {
			log.Err(err).Msg("webhooks: could not defer rate-limited delivery")
		}
		return WebhookResult{Error: "rate limited"}
	}
	if err := validate.ValidateWebhookURL(ep.URL, s.notifier); err != nil {
		// The destination is refused by policy; trying again will not change that.
		return fail(repo.Attempt{Err: err.Error()}, false)
	}

	g, gerr := s.repos.Groups.GroupByID(ctx, ep.GroupID)
	zone := "UTC"
	if gerr == nil && g.Timezone != "" {
		zone = g.Timezone
	}
	attemptNo := d.Attempts + 1
	body, err := webhooks.Body(webhooks.Delivery{
		ID: d.ID, EventID: d.EventID, Type: d.EventType, GroupID: ep.GroupID,
		OccurredAt: d.OccurredAt, ScheduledAt: d.ScheduledFor, CreatedAt: d.CreatedAt, Data: d.Data,
	}, attemptNo, now, regional.Location(zone), zone)
	if err != nil {
		return fail(repo.Attempt{Err: "could not build the body: " + err.Error()}, false)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, bytes.NewReader(body))
	if err != nil {
		return fail(repo.Attempt{Err: "bad URL"}, false)
	}
	delay := max(now.Sub(delayRef(d)), 0)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Homebox-Webhooks/1")
	req.Header.Set("X-Homebox-Event", d.EventType)
	req.Header.Set("X-Homebox-Delivery", d.ID.String())
	req.Header.Set("X-Homebox-Attempt", strconv.Itoa(attemptNo))
	req.Header.Set("X-Homebox-Delay-Seconds", strconv.FormatInt(int64(delay.Seconds()), 10))
	req.Header.Set("X-Homebox-Timestamp", strconv.FormatInt(now.Unix(), 10))
	req.Header.Set("X-Homebox-Signature", webhooks.Sign(ep.Secret, now, body))

	start := time.Now()
	resp, err := s.client.Do(req)
	took := time.Since(start)
	if err != nil {
		// Never log or store the URL: it can carry a token.
		return fail(repo.Attempt{Err: transportError(err), Duration: took}, true)
	}
	defer func() { _ = resp.Body.Close() }()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))

	a := repo.Attempt{StatusCode: resp.StatusCode, Duration: took, Response: string(snippet)}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if err := s.repos.Webhooks.RecordSuccess(ctx, d.ID, a, s.now()); err != nil {
			log.Err(err).Str("delivery_id", d.ID.String()).Msg("webhooks: could not record success")
		}
		return WebhookResult{OK: true, StatusCode: resp.StatusCode, DurationMs: int(took.Milliseconds())}
	}
	a.Err = "receiver answered " + strconv.Itoa(resp.StatusCode)
	// 410 Gone says the receiver has retired the URL for good.
	return fail(a, resp.StatusCode != http.StatusGone)
}

func delayRef(d repo.WebhookDeliveryFull) time.Time {
	if d.ScheduledFor != nil {
		return *d.ScheduledFor
	}
	return d.OccurredAt
}

// transportError words a network failure without echoing the request URL.
func transportError(err error) string {
	var ue interface{ Unwrap() error }
	if errors.As(err, &ue) && ue.Unwrap() != nil {
		err = ue.Unwrap()
	}
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

// SendTest delivers a sample event to an endpoint right away and reports what
// the receiver said. It is logged like any delivery but never retried.
func (s *WebhookService) SendTest(ctx context.Context, gid, endpointID uuid.UUID) (WebhookResult, error) {
	if !s.Enabled() {
		return WebhookResult{}, errors.New("webhooks are not enabled")
	}
	if _, err := s.repos.Webhooks.GetOne(ctx, gid, endpointID); err != nil {
		return WebhookResult{}, err
	}
	now := s.now()
	id, ok, err := s.repos.Webhooks.Queue(ctx, endpointID, repo.WebhookNewDelivery{
		EventID: uuid.New(), EventType: webhooks.Test, OccurredAt: now,
		Data: map[string]any{"message": "This is a test event from Homebox."},
	}, now, now.Add(24*time.Hour)) // due far ahead, so the worker leaves it alone
	if err != nil || !ok {
		return WebhookResult{}, fmt.Errorf("could not queue the test event: %w", err)
	}
	d, err := s.repos.Webhooks.DeliveryByID(ctx, id)
	if err != nil {
		return WebhookResult{}, err
	}
	return s.attempt(ctx, d, true), nil
}

// Redeliver queues a fresh copy of a past delivery. The copy keeps the event id,
// so a receiver that de-duplicates on it can still recognise the event.
func (s *WebhookService) Redeliver(ctx context.Context, gid, endpointID, deliveryID uuid.UUID) (uuid.UUID, error) {
	if !s.Enabled() {
		return uuid.Nil, errors.New("webhooks are not enabled")
	}
	d, err := s.repos.Webhooks.Delivery(ctx, gid, endpointID, deliveryID)
	if err != nil {
		return uuid.Nil, err
	}
	id, _, err := s.repos.Webhooks.Queue(ctx, endpointID, repo.WebhookNewDelivery{
		EventID: d.EventID, EventType: d.EventType, OccurredAt: d.OccurredAt, Scheduled: d.ScheduledFor, Data: d.Data,
	}, s.now(), s.now())
	if err != nil {
		return uuid.Nil, err
	}
	s.poke()
	return id, nil
}
