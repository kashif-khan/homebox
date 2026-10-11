package repo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sysadminsmedia/homebox/backend/internal/core/webhooks"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/webhookdelivery"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/webhookendpoint"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
)

// WebhookRepository stores webhook endpoints and the deliveries owed to them.
type WebhookRepository struct {
	db *ent.Client
}

type (
	// WebhookEndpointIn is what a client sends to create or change an endpoint.
	WebhookEndpointIn struct {
		Name string `json:"name"`
		// URL receives the events. On update, omit it (or send null) to keep the
		// stored one; it is never returned by the API.
		URL *string `json:"url,omitempty" extensions:"x-nullable,x-omitempty"`
		// Events lists the event names to subscribe to.
		Events []string `json:"events"`
		// EntityTypeID and TagID narrow item events to matching items.
		EntityTypeID *uuid.UUID `json:"entityTypeId,omitempty" extensions:"x-nullable,x-omitempty"`
		TagID        *uuid.UUID `json:"tagId,omitempty"        extensions:"x-nullable,x-omitempty"`
		IsActive     bool       `json:"isActive"`
	}

	// WebhookEndpointOut is a stored endpoint. The URL is shown only as its
	// scheme and host, and the secret not at all: both are write-only.
	WebhookEndpointOut struct {
		ID           uuid.UUID  `json:"id"`
		Name         string     `json:"name"`
		URLHost      string     `json:"urlHost"`
		Events       []string   `json:"events"`
		EntityTypeID *uuid.UUID `json:"entityTypeId,omitempty" extensions:"x-nullable,x-omitempty"`
		TagID        *uuid.UUID `json:"tagId,omitempty"        extensions:"x-nullable,x-omitempty"`
		IsActive     bool       `json:"isActive"`
		CreatedAt    time.Time  `json:"createdAt"`
		UpdatedAt    time.Time  `json:"updatedAt"`
	}

	// WebhookEndpointCreated is returned once on creation (and on rotating the
	// secret): the only time the signing secret is shown.
	WebhookEndpointCreated struct {
		WebhookEndpointOut
		Secret string `json:"secret"`
	}

	// WebhookEndpointFull carries the sensitive parts, for the sender only.
	WebhookEndpointFull struct {
		WebhookEndpointOut
		GroupID uuid.UUID `json:"-"`
		URL     string    `json:"-"`
		Secret  string    `json:"-"`
	}

	// WebhookDeliveryOut is one row of an endpoint's delivery log.
	WebhookDeliveryOut struct {
		ID             uuid.UUID  `json:"id"`
		EventID        uuid.UUID  `json:"eventId"`
		EventType      string     `json:"eventType"`
		Status         string     `json:"status"`
		Attempts       int        `json:"attempts"`
		OccurredAt     time.Time  `json:"occurredAt"`
		ScheduledFor   *time.Time `json:"scheduledFor,omitempty"   extensions:"x-nullable,x-omitempty"`
		CreatedAt      time.Time  `json:"createdAt"`
		NextAttemptAt  *time.Time `json:"nextAttemptAt,omitempty"  extensions:"x-nullable,x-omitempty"`
		DeliveredAt    *time.Time `json:"deliveredAt,omitempty"    extensions:"x-nullable,x-omitempty"`
		LastStatusCode int        `json:"lastStatusCode"`
		LastError      string     `json:"lastError"`
		LastDurationMs int        `json:"lastDurationMs"`
		Response       string     `json:"response"`
	}

	// WebhookDeliveryFull is a delivery with the data needed to send it.
	WebhookDeliveryFull struct {
		WebhookDeliveryOut
		EndpointID uuid.UUID
		Data       map[string]any
	}

	// WebhookNewDelivery asks for a delivery to be queued.
	WebhookNewDelivery struct {
		EventID    uuid.UUID
		EventType  string
		OccurredAt time.Time
		Scheduled  *time.Time
		Data       map[string]any
		DedupeKey  string
	}
)

func webhookErr(format string, a ...any) error {
	return validate.NewRequestError(fmt.Errorf(format, a...), http.StatusUnprocessableEntity)
}

// MaskURL reduces a webhook URL to scheme and host, so a token in the path or
// query never reaches the client.
func MaskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	out := u.Scheme + "://" + u.Host
	if strings.Trim(u.Path, "/") != "" || u.RawQuery != "" {
		out += "/…"
	}
	return out
}

func mapEndpoint(e *ent.WebhookEndpoint) WebhookEndpointOut {
	evs := slices.Clone(e.Events)
	if evs == nil {
		evs = []string{}
	}
	return WebhookEndpointOut{
		ID: e.ID, Name: e.Name, URLHost: MaskURL(e.URL), Events: evs,
		EntityTypeID: e.EntityTypeID, TagID: e.TagID, IsActive: e.IsActive,
		CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
	}
}

func mapDelivery(d *ent.WebhookDelivery) WebhookDeliveryOut {
	out := WebhookDeliveryOut{
		ID: d.ID, EventID: d.EventID, EventType: d.EventType, Status: string(d.Status), Attempts: d.Attempts,
		OccurredAt: d.OccurredAt, ScheduledFor: d.ScheduledFor, CreatedAt: d.CreatedAt, DeliveredAt: d.DeliveredAt,
		LastStatusCode: d.LastStatusCode, LastError: d.LastError, LastDurationMs: d.LastDurationMs, Response: d.ResponseSnippet,
	}
	if d.Status == webhookdelivery.StatusPending {
		n := d.NextAttemptAt
		out.NextAttemptAt = &n
	}
	return out
}

// cleanEndpoint validates the parts of an endpoint that do not need the
// database, returning the de-duplicated, catalogue-ordered event list.
func cleanEndpoint(in WebhookEndpointIn) (WebhookEndpointIn, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 255 {
		return in, webhookErr("name must be 1-255 characters")
	}
	if len(in.Events) == 0 {
		return in, webhookErr("choose at least one event")
	}
	want := map[string]bool{}
	for _, e := range in.Events {
		if !webhooks.ValidEvent(e) {
			return in, webhookErr("unknown event %q", e)
		}
		want[e] = true
	}
	in.Events = in.Events[:0:0]
	for _, ev := range webhooks.Catalog {
		if want[ev.Name] {
			in.Events = append(in.Events, ev.Name)
		}
	}
	return in, nil
}

func (r *WebhookRepository) checkFilters(ctx context.Context, gid uuid.UUID, in WebhookEndpointIn) error {
	if in.EntityTypeID != nil {
		if err := assertEntityTypeInGroup(ctx, r.db.EntityType, gid, *in.EntityTypeID); err != nil {
			return webhookErr("entityTypeId is not one of this collection's types")
		}
	}
	if in.TagID != nil {
		if err := assertTagsInGroup(ctx, r.db.Tag, gid, []uuid.UUID{*in.TagID}); err != nil {
			return webhookErr("tagId is not one of this collection's tags")
		}
	}
	return nil
}

// Count returns how many endpoints a collection has.
func (r *WebhookRepository) Count(ctx context.Context, gid uuid.UUID) (int, error) {
	return r.db.WebhookEndpoint.Query().Where(webhookendpoint.GroupID(gid)).Count(ctx)
}

// GetAll lists a collection's endpoints.
func (r *WebhookRepository) GetAll(ctx context.Context, gid uuid.UUID) ([]WebhookEndpointOut, error) {
	rows, err := r.db.WebhookEndpoint.Query().
		Where(webhookendpoint.GroupID(gid)).
		Order(ent.Asc(webhookendpoint.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]WebhookEndpointOut, len(rows))
	for i, e := range rows {
		out[i] = mapEndpoint(e)
	}
	return out, nil
}

// GetOne returns one endpoint of the collection.
func (r *WebhookRepository) GetOne(ctx context.Context, gid, id uuid.UUID) (WebhookEndpointOut, error) {
	e, err := r.db.WebhookEndpoint.Query().Where(webhookendpoint.ID(id), webhookendpoint.GroupID(gid)).Only(ctx)
	if err != nil {
		return WebhookEndpointOut{}, err
	}
	return mapEndpoint(e), nil
}

// GetFull returns an endpoint with its URL and secret, for sending.
func (r *WebhookRepository) GetFull(ctx context.Context, id uuid.UUID) (WebhookEndpointFull, error) {
	e, err := r.db.WebhookEndpoint.Get(ctx, id)
	if err != nil {
		return WebhookEndpointFull{}, err
	}
	return WebhookEndpointFull{WebhookEndpointOut: mapEndpoint(e), GroupID: e.GroupID, URL: e.URL, Secret: e.Secret}, nil
}

// Create stores an endpoint and returns its signing secret, once. The URL must
// already have passed the network policy check.
func (r *WebhookRepository) Create(ctx context.Context, gid uuid.UUID, in WebhookEndpointIn, secret string) (WebhookEndpointCreated, error) {
	in, err := cleanEndpoint(in)
	if err != nil {
		return WebhookEndpointCreated{}, err
	}
	if in.URL == nil || strings.TrimSpace(*in.URL) == "" {
		return WebhookEndpointCreated{}, webhookErr("url is required")
	}
	if err := r.checkFilters(ctx, gid, in); err != nil {
		return WebhookEndpointCreated{}, err
	}
	e, err := r.db.WebhookEndpoint.Create().
		SetGroupID(gid).SetName(in.Name).SetURL(strings.TrimSpace(*in.URL)).SetSecret(secret).
		SetEvents(in.Events).SetNillableEntityTypeID(in.EntityTypeID).SetNillableTagID(in.TagID).
		SetIsActive(in.IsActive).Save(ctx)
	if err != nil {
		return WebhookEndpointCreated{}, err
	}
	return WebhookEndpointCreated{WebhookEndpointOut: mapEndpoint(e), Secret: secret}, nil
}

// Update changes an endpoint. A nil URL keeps the stored one.
func (r *WebhookRepository) Update(ctx context.Context, gid, id uuid.UUID, in WebhookEndpointIn) (WebhookEndpointOut, error) {
	in, err := cleanEndpoint(in)
	if err != nil {
		return WebhookEndpointOut{}, err
	}
	if err := r.checkFilters(ctx, gid, in); err != nil {
		return WebhookEndpointOut{}, err
	}
	upd := r.db.WebhookEndpoint.Update().
		Where(webhookendpoint.ID(id), webhookendpoint.GroupID(gid)).
		SetName(in.Name).SetEvents(in.Events).SetIsActive(in.IsActive)
	if in.URL != nil && strings.TrimSpace(*in.URL) != "" {
		upd.SetURL(strings.TrimSpace(*in.URL))
	}
	if in.EntityTypeID != nil {
		upd.SetEntityTypeID(*in.EntityTypeID)
	} else {
		upd.ClearEntityTypeID()
	}
	if in.TagID != nil {
		upd.SetTagID(*in.TagID)
	} else {
		upd.ClearTagID()
	}
	n, err := upd.Save(ctx)
	if err != nil {
		return WebhookEndpointOut{}, err
	}
	if n == 0 {
		return WebhookEndpointOut{}, &ent.NotFoundError{}
	}
	return r.GetOne(ctx, gid, id)
}

// RotateSecret replaces the signing secret and returns the new one, once.
func (r *WebhookRepository) RotateSecret(ctx context.Context, gid, id uuid.UUID, secret string) (WebhookEndpointCreated, error) {
	n, err := r.db.WebhookEndpoint.Update().
		Where(webhookendpoint.ID(id), webhookendpoint.GroupID(gid)).SetSecret(secret).Save(ctx)
	if err != nil {
		return WebhookEndpointCreated{}, err
	}
	if n == 0 {
		return WebhookEndpointCreated{}, &ent.NotFoundError{}
	}
	out, err := r.GetOne(ctx, gid, id)
	return WebhookEndpointCreated{WebhookEndpointOut: out, Secret: secret}, err
}

// Delete removes an endpoint and its delivery log.
func (r *WebhookRepository) Delete(ctx context.Context, gid, id uuid.UUID) error {
	n, err := r.db.WebhookEndpoint.Delete().Where(webhookendpoint.ID(id), webhookendpoint.GroupID(gid)).Exec(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return &ent.NotFoundError{}
	}
	return nil
}

// Subscribers returns the active endpoints of a collection subscribed to an
// event, in full (they are about to be sent to).
func (r *WebhookRepository) Subscribers(ctx context.Context, gid uuid.UUID, event string) ([]WebhookEndpointFull, error) {
	rows, err := r.db.WebhookEndpoint.Query().
		Where(webhookendpoint.GroupID(gid), webhookendpoint.IsActive(true)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	var out []WebhookEndpointFull
	for _, e := range rows {
		if slices.Contains(e.Events, event) {
			out = append(out, WebhookEndpointFull{WebhookEndpointOut: mapEndpoint(e), GroupID: e.GroupID, URL: e.URL, Secret: e.Secret})
		}
	}
	return out, nil
}

// Queue records a delivery owed to an endpoint. created is when it was queued
// (the retry limit counts from it) and due when it should first be tried. A
// non-empty DedupeKey makes a repeat of the same event a no-op; the second
// return value says whether a delivery was actually created.
func (r *WebhookRepository) Queue(ctx context.Context, endpointID uuid.UUID, nd WebhookNewDelivery, created, due time.Time) (uuid.UUID, bool, error) {
	c := r.db.WebhookDelivery.Create().
		SetEndpointID(endpointID).SetEventID(nd.EventID).SetEventType(nd.EventType).
		SetOccurredAt(nd.OccurredAt).SetNillableScheduledFor(nd.Scheduled).
		SetData(nd.Data).SetNextAttemptAt(due).SetCreatedAt(created).SetUpdatedAt(created)
	if nd.DedupeKey != "" {
		c.SetDedupeKey(nd.DedupeKey)
	}
	d, err := c.Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) && nd.DedupeKey != "" {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, err
	}
	return d.ID, true, nil
}

// Due returns up to limit pending deliveries whose time has come, oldest first.
func (r *WebhookRepository) Due(ctx context.Context, now time.Time, limit int) ([]WebhookDeliveryFull, error) {
	rows, err := r.db.WebhookDelivery.Query().
		Where(webhookdelivery.StatusEQ(webhookdelivery.StatusPending), webhookdelivery.NextAttemptAtLTE(now)).
		WithEndpoint().
		Order(ent.Asc(webhookdelivery.FieldNextAttemptAt)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]WebhookDeliveryFull, 0, len(rows))
	for _, d := range rows {
		if d.Edges.Endpoint == nil {
			continue
		}
		out = append(out, WebhookDeliveryFull{WebhookDeliveryOut: mapDelivery(d), EndpointID: d.Edges.Endpoint.ID, Data: d.Data})
	}
	return out, nil
}

// Lease pushes a delivery's next attempt out so that another pass does not pick
// it up while it is being sent.
func (r *WebhookRepository) Lease(ctx context.Context, id uuid.UUID, until time.Time) error {
	return r.db.WebhookDelivery.UpdateOneID(id).SetNextAttemptAt(until).Exec(ctx)
}

// Attempt is the outcome of one try.
type Attempt struct {
	StatusCode int
	Err        string
	Duration   time.Duration
	Response   string
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// RecordSuccess marks a delivery delivered.
func (r *WebhookRepository) RecordSuccess(ctx context.Context, id uuid.UUID, a Attempt, now time.Time) error {
	return r.db.WebhookDelivery.UpdateOneID(id).
		SetStatus(webhookdelivery.StatusDelivered).AddAttempts(1).SetDeliveredAt(now).
		SetLastStatusCode(a.StatusCode).SetLastError("").SetLastDurationMs(int(a.Duration.Milliseconds())).
		SetResponseSnippet(clip(a.Response, 500)).Exec(ctx)
}

// RecordFailure notes a failed try. A nil next marks the delivery failed for
// good; otherwise it will be tried again then.
func (r *WebhookRepository) RecordFailure(ctx context.Context, id uuid.UUID, a Attempt, next *time.Time) error {
	u := r.db.WebhookDelivery.UpdateOneID(id).AddAttempts(1).
		SetLastStatusCode(a.StatusCode).SetLastError(clip(a.Err, 500)).
		SetLastDurationMs(int(a.Duration.Milliseconds())).SetResponseSnippet(clip(a.Response, 500))
	if next == nil {
		u.SetStatus(webhookdelivery.StatusFailed)
	} else {
		u.SetNextAttemptAt(*next)
	}
	return u.Exec(ctx)
}

// Deliveries lists an endpoint's log, newest first.
func (r *WebhookRepository) Deliveries(ctx context.Context, gid, endpointID uuid.UUID, limit, offset int) ([]WebhookDeliveryOut, int, error) {
	if _, err := r.GetOne(ctx, gid, endpointID); err != nil {
		return nil, 0, err
	}
	q := r.db.WebhookDelivery.Query().Where(webhookdelivery.HasEndpointWith(webhookendpoint.ID(endpointID)))
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.Order(ent.Desc(webhookdelivery.FieldCreatedAt)).Limit(limit).Offset(offset).All(ctx)
	if err != nil {
		return nil, 0, err
	}
	out := make([]WebhookDeliveryOut, len(rows))
	for i, d := range rows {
		out[i] = mapDelivery(d)
	}
	return out, total, nil
}

// Delivery returns one delivery of an endpoint, with its data.
func (r *WebhookRepository) Delivery(ctx context.Context, gid, endpointID, id uuid.UUID) (WebhookDeliveryFull, error) {
	if _, err := r.GetOne(ctx, gid, endpointID); err != nil {
		return WebhookDeliveryFull{}, err
	}
	d, err := r.db.WebhookDelivery.Query().
		Where(webhookdelivery.ID(id), webhookdelivery.HasEndpointWith(webhookendpoint.ID(endpointID))).Only(ctx)
	if err != nil {
		return WebhookDeliveryFull{}, err
	}
	return WebhookDeliveryFull{WebhookDeliveryOut: mapDelivery(d), EndpointID: endpointID, Data: d.Data}, nil
}

// Delivery fetches by id alone, for the worker.
func (r *WebhookRepository) DeliveryByID(ctx context.Context, id uuid.UUID) (WebhookDeliveryFull, error) {
	d, err := r.db.WebhookDelivery.Query().Where(webhookdelivery.ID(id)).WithEndpoint().Only(ctx)
	if err != nil {
		return WebhookDeliveryFull{}, err
	}
	if d.Edges.Endpoint == nil {
		return WebhookDeliveryFull{}, errors.New("delivery has no endpoint")
	}
	return WebhookDeliveryFull{WebhookDeliveryOut: mapDelivery(d), EndpointID: d.Edges.Endpoint.ID, Data: d.Data}, nil
}

// PurgeDelivered removes finished deliveries older than before. Pending ones are
// never removed here: they are still owed.
func (r *WebhookRepository) PurgeDelivered(ctx context.Context, before time.Time) (int, error) {
	return r.db.WebhookDelivery.Delete().
		Where(webhookdelivery.StatusNEQ(webhookdelivery.StatusPending), webhookdelivery.CreatedAtLT(before)).
		Exec(ctx)
}

// RecentCount counts deliveries attempted for an endpoint since a time, for
// rate limiting across restarts.
func (r *WebhookRepository) RecentCount(ctx context.Context, endpointID uuid.UUID, since time.Time) (int, error) {
	return r.db.WebhookDelivery.Query().
		Where(webhookdelivery.HasEndpointWith(webhookendpoint.ID(endpointID)), webhookdelivery.UpdatedAtGTE(since),
			webhookdelivery.AttemptsGT(0)).
		Count(ctx)
}
