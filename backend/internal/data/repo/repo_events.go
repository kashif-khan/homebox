package repo

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/sysadminsmedia/homebox/backend/internal/core/webhooks"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/attachment"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entity"
)

// DomainEvent is something that happened to a collection, for outbound webhooks.
// OccurredAt may be left zero, in which case the sink stamps it with its clock.
// It is built only when a sink is registered, so a deployment without webhooks
// pays nothing for it.
type DomainEvent struct {
	GID          uuid.UUID
	Type         string
	OccurredAt   time.Time
	EntityTypeID uuid.UUID // zero when the event has no item
	TagIDs       []uuid.UUID
	Data         map[string]any
}

// EventSink receives domain events. Implementations must not block for long and
// must not fail the caller: the change has already been made.
type EventSink interface {
	Emit(ctx context.Context, ev DomainEvent)
}

// SetEventSink registers the sink that is told about changes.
func (a *AllRepos) SetEventSink(s EventSink) {
	a.Entities.events = s
	a.Entities.attachments.events = s
}

func (r *EntityRepository) emit(ctx context.Context, ev DomainEvent) {
	if r.events == nil {
		return
	}
	// The mutation is done; a cancelled request must not drop its event.
	r.events.Emit(context.WithoutCancel(ctx), ev)
}

// snap is the slice of an item the webhooks care about.
type snap struct {
	gid    uuid.UUID
	typeID uuid.UUID
	tagIDs []uuid.UUID
	parent uuid.UUID
	qty    float64
	data   map[string]any
	// vals holds the comparable fields, for working out what changed.
	vals map[string]string
}

func str[T any](v T) string { return fmt.Sprint(v) }

func (r *EntityRepository) snapshot(ctx context.Context, id uuid.UUID) *snap {
	if r.events == nil || id == uuid.Nil {
		return nil
	}
	e, err := r.db.Entity.Query().Where(entity.ID(id)).
		WithGroup().WithParent().WithEntityType().WithTag().WithFields().Only(ctx)
	if err != nil {
		if !ent.IsNotFound(err) {
			log.Debug().Err(err).Msg("webhooks: could not snapshot item")
		}
		return nil
	}
	s := &snap{qty: e.Quantity, vals: map[string]string{}}
	if e.Edges.Group != nil {
		s.gid = e.Edges.Group.ID
	}
	item := map[string]any{
		"id": e.ID, "name": e.Name, "description": e.Description, "quantity": e.Quantity,
		"asset_id": AssetID(e.AssetID).String(), "archived": e.Archived, "path": "/item/" + e.ID.String(),
	}
	if e.Edges.EntityType != nil {
		s.typeID = e.Edges.EntityType.ID
		item["entity_type"] = map[string]any{"id": e.Edges.EntityType.ID, "name": e.Edges.EntityType.Name, "is_location": e.Edges.EntityType.IsLocation}
		if e.Edges.EntityType.IsLocation {
			item["path"] = "/location/" + e.ID.String()
		}
	}
	if e.Edges.Parent != nil {
		s.parent = e.Edges.Parent.ID
		item["parent"] = map[string]any{"id": e.Edges.Parent.ID, "name": e.Edges.Parent.Name}
	}
	tags := make([]map[string]any, 0, len(e.Edges.Tag))
	for _, t := range e.Edges.Tag {
		s.tagIDs = append(s.tagIDs, t.ID)
		tags = append(tags, map[string]any{"id": t.ID, "name": t.Name})
	}
	item["tags"] = tags
	if !e.ExpiresAt.IsZero() {
		item["expires_at"] = e.ExpiresAt.UTC().Format(time.DateOnly)
		item["expiry_kind"] = string(e.ExpiryKind)
	}
	if !e.WarrantyExpires.IsZero() {
		item["warranty_expires"] = e.WarrantyExpires.UTC().Format(time.DateOnly)
	}
	s.data = item

	date := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.UTC().Format(time.DateOnly)
	}
	s.vals = map[string]string{
		"name": e.Name, "description": e.Description, "notes": e.Notes, "quantity": str(e.Quantity),
		"archived": str(e.Archived), "insured": str(e.Insured), "asset_id": str(e.AssetID),
		"manufacturer": e.Manufacturer, "model_number": e.ModelNumber, "serial_number": e.SerialNumber,
		"purchase_price": str(e.PurchasePrice), "purchase_from": e.PurchaseFrom, "purchase_date": date(e.PurchaseDate),
		"expires_at": date(e.ExpiresAt), "expiry_kind": string(e.ExpiryKind), "warranty_expires": date(e.WarrantyExpires),
		"lifetime_warranty": str(e.LifetimeWarranty),
		"parent_id":         s.parent.String(), "entity_type_id": s.typeID.String(),
	}
	ids := make([]string, 0, len(s.tagIDs))
	for _, t := range s.tagIDs {
		ids = append(ids, t.String())
	}
	sort.Strings(ids)
	s.vals["tags"] = strings.Join(ids, ",")
	for _, f := range e.Edges.Fields {
		s.vals["field:"+f.Name] = f.TextValue + "|" + str(f.NumberValue) + "|" + str(f.BooleanValue)
	}
	return s
}

func (s *snap) event(typ string, data map[string]any) DomainEvent {
	return DomainEvent{GID: s.gid, Type: typ, EntityTypeID: s.typeID, TagIDs: s.tagIDs, Data: data}
}

// changes lists what differs between two snapshots, one entry per field.
func changes(before, after *snap) []map[string]any {
	keys := map[string]struct{}{}
	for k := range before.vals {
		keys[k] = struct{}{}
	}
	for k := range after.vals {
		keys[k] = struct{}{}
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []map[string]any
	for _, k := range names {
		b, a := before.vals[k], after.vals[k]
		if b == a {
			continue
		}
		out = append(out, map[string]any{"field": strings.TrimPrefix(k, "field:"), "custom": strings.HasPrefix(k, "field:"), "before": b, "after": a})
	}
	return out
}

func (r *EntityRepository) emitCreated(ctx context.Context, id uuid.UUID) {
	if s := r.snapshot(ctx, id); s != nil {
		r.emit(ctx, s.event(webhooks.ItemCreated, map[string]any{"item": s.data}))
	}
}

func (r *EntityRepository) emitDeleted(ctx context.Context, s *snap) {
	if s != nil {
		r.emit(ctx, s.event(webhooks.ItemDeleted, map[string]any{"item": s.data}))
	}
}

// emitChanged works out what an update did and emits the matching events: an
// update listing the changed fields, a move when the parent changed, and a
// quantity change when the quantity did.
func (r *EntityRepository) emitChanged(ctx context.Context, before *snap, id uuid.UUID) {
	if before == nil {
		return
	}
	after := r.snapshot(ctx, id)
	if after == nil {
		return
	}
	diff := changes(before, after)
	if len(diff) == 0 {
		return
	}
	r.emit(ctx, after.event(webhooks.ItemUpdated, map[string]any{"item": after.data, "changes": diff}))

	if before.parent != after.parent {
		r.emit(ctx, after.event(webhooks.ItemMoved, map[string]any{
			"item": after.data,
			"from": before.data["parent"],
			"to":   after.data["parent"],
		}))
	}
	if before.qty != after.qty {
		r.emit(ctx, after.event(webhooks.ItemQuantityChanged, map[string]any{
			"item": after.data, "before": before.qty, "after": after.qty,
		}))
	}
}

// ---- wrappers: each mutation, then its events ----

func (r *EntityRepository) Create(ctx context.Context, gid uuid.UUID, data EntityCreate) (EntityOut, error) {
	out, err := r.create(ctx, gid, data)
	if err == nil {
		r.emitCreated(ctx, out.ID)
	}
	return out, err
}

func (r *EntityRepository) CreateContainer(ctx context.Context, gid uuid.UUID, data EntityCreate) (EntityOut, error) {
	out, err := r.createContainer(ctx, gid, data)
	if err == nil {
		r.emitCreated(ctx, out.ID)
	}
	return out, err
}

func (r *EntityRepository) CreateFromTemplate(ctx context.Context, gid uuid.UUID, data EntityCreateFromTemplate) (EntityOut, error) {
	out, err := r.createFromTemplate(ctx, gid, data)
	if err == nil {
		r.emitCreated(ctx, out.ID)
	}
	return out, err
}

func (r *EntityRepository) Duplicate(ctx context.Context, gid, id uuid.UUID, options DuplicateOptions) (EntityOut, error) {
	out, err := r.duplicate(ctx, gid, id, options)
	if err == nil {
		r.emitCreated(ctx, out.ID)
	}
	return out, err
}

func (r *EntityRepository) UpdateByGroup(ctx context.Context, gid uuid.UUID, data EntityUpdate) (EntityOut, error) {
	before := r.snapshot(ctx, data.ID)
	out, err := r.updateByGroup(ctx, gid, data)
	if err == nil {
		r.emitChanged(ctx, before, data.ID)
	}
	return out, err
}

func (r *EntityRepository) Patch(ctx context.Context, gid, id uuid.UUID, data EntityPatch) error {
	before := r.snapshot(ctx, id)
	err := r.patch(ctx, gid, id, data)
	if err == nil {
		r.emitChanged(ctx, before, id)
	}
	return err
}

func (r *EntityRepository) Delete(ctx context.Context, id uuid.UUID) error {
	before := r.snapshot(ctx, id)
	err := r.deleteByID(ctx, id)
	if err == nil {
		r.emitDeleted(ctx, before)
	}
	return err
}

func (r *EntityRepository) DeleteByGroup(ctx context.Context, gid, id uuid.UUID) error {
	before := r.snapshot(ctx, id)
	err := r.deleteByGroup(ctx, gid, id)
	if err == nil {
		r.emitDeleted(ctx, before)
	}
	return err
}

func (r *EntityRepository) DeleteContainerByGroup(ctx context.Context, gid, id uuid.UUID) error {
	before := r.snapshot(ctx, id)
	err := r.deleteContainerByGroup(ctx, gid, id)
	if err == nil {
		r.emitDeleted(ctx, before)
	}
	return err
}

// ---- attachments ----

func (r *AttachmentRepo) emitAdded(ctx context.Context, entityID uuid.UUID, att *ent.Attachment) {
	if r.events == nil || att == nil {
		return
	}
	e, err := r.db.Entity.Query().Where(entity.ID(entityID)).WithGroup().WithEntityType().WithTag().Only(ctx)
	if err != nil || e.Edges.Group == nil {
		return
	}
	ev := DomainEvent{
		GID: e.Edges.Group.ID, Type: webhooks.AttachmentAdded,
		Data: map[string]any{
			"item":       map[string]any{"id": e.ID, "name": e.Name, "path": "/item/" + e.ID.String()},
			"attachment": map[string]any{"id": att.ID, "type": string(att.Type), "title": att.Title, "primary": att.Primary},
		},
	}
	if e.Edges.EntityType != nil {
		ev.EntityTypeID = e.Edges.EntityType.ID
	}
	for _, t := range e.Edges.Tag {
		ev.TagIDs = append(ev.TagIDs, t.ID)
	}
	r.events.Emit(context.WithoutCancel(ctx), ev)
}

func (r *AttachmentRepo) Create(ctx context.Context, itemID uuid.UUID, doc ItemCreateAttachment, typ attachment.Type, primary bool) (*ent.Attachment, error) {
	att, err := r.create(ctx, itemID, doc, typ, primary)
	if err == nil {
		r.emitAdded(ctx, itemID, att)
	}
	return att, err
}

func (r *AttachmentRepo) CreateExternalLink(ctx context.Context, entityID uuid.UUID, externalID string, title string, mimeType string, attType attachment.Type) (*ent.Attachment, error) {
	att, err := r.createExternalLink(ctx, entityID, externalID, title, mimeType, attType)
	if err == nil {
		r.emitAdded(ctx, entityID, att)
	}
	return att, err
}
