package repo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sysadminsmedia/homebox/backend/internal/core/alerts"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entity"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entitytype"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/group"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/maintenanceentry"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/notificationlog"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/notificationrule"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/notifier"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/predicate"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/tag"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
)

// NotificationRuleRepository stores alert rules and remembers what was sent.
type NotificationRuleRepository struct {
	db *ent.Client
}

type (
	// NotificationRuleIn is what a client sends to create or change a rule.
	NotificationRuleIn struct {
		Name string `json:"name"`
		// Trigger is expires_at, warranty_expires or maintenance_due.
		Trigger string `json:"trigger"`
		// OffsetsDays: positive fires that many days before the date, zero on
		// the day, negative that many days after. For example [30, 7, 1, 0, -7].
		OffsetsDays []int `json:"offsetsDays"`
		// EntityTypeID and TagID narrow the rule to matching items.
		EntityTypeID *uuid.UUID `json:"entityTypeId,omitempty" extensions:"x-nullable,x-omitempty"`
		TagID        *uuid.UUID `json:"tagId,omitempty"        extensions:"x-nullable,x-omitempty"`
		// NotifierIDs picks the notifiers; empty means every active one.
		NotifierIDs []uuid.UUID `json:"notifierIds"`
		IsActive    bool        `json:"isActive"`
	}

	// NotificationRuleOut is a stored rule.
	NotificationRuleOut struct {
		ID           uuid.UUID   `json:"id"`
		Name         string      `json:"name"`
		Trigger      string      `json:"trigger"`
		OffsetsDays  []int       `json:"offsetsDays"`
		EntityTypeID *uuid.UUID  `json:"entityTypeId,omitempty" extensions:"x-nullable,x-omitempty"`
		TagID        *uuid.UUID  `json:"tagId,omitempty"        extensions:"x-nullable,x-omitempty"`
		NotifierIDs  []uuid.UUID `json:"notifierIds"`
		IsActive     bool        `json:"isActive"`
		CreatedAt    time.Time   `json:"createdAt"`
		UpdatedAt    time.Time   `json:"updatedAt"`
	}

	// SentKey identifies one delivered alert.
	SentKey struct {
		NotifierID uuid.UUID
		SubjectID  uuid.UUID
		Offset     int
		Anchor     time.Time
	}

	// SentEntry is a delivered alert to record.
	SentEntry struct {
		SentKey
		DueOn  time.Time
		SentAt time.Time
	}
)

func ruleErr(format string, a ...any) error {
	return validate.NewRequestError(fmt.Errorf(format, a...), http.StatusUnprocessableEntity)
}

func mapRule(r *ent.NotificationRule) NotificationRuleOut {
	ids := r.NotifierIds
	if ids == nil {
		ids = []uuid.UUID{}
	}
	offs := slices.Clone(r.OffsetsDays)
	if offs == nil {
		offs = []int{}
	}
	return NotificationRuleOut{
		ID: r.ID, Name: r.Name, Trigger: string(r.Trigger), OffsetsDays: offs,
		EntityTypeID: r.EntityTypeID, TagID: r.TagID, NotifierIDs: ids, IsActive: r.IsActive,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// validate checks a rule and returns a cleaned copy: offsets de-duplicated and
// ordered from furthest before to furthest after.
func (r *NotificationRuleRepository) validate(ctx context.Context, gid uuid.UUID, in NotificationRuleIn) (NotificationRuleIn, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 255 {
		return in, ruleErr("name must be 1-255 characters")
	}
	if !alerts.ValidTrigger(in.Trigger) {
		return in, ruleErr("trigger must be one of expires_at, warranty_expires, maintenance_due")
	}
	if len(in.OffsetsDays) == 0 || len(in.OffsetsDays) > alerts.MaxOffsets {
		return in, ruleErr("offsetsDays needs between 1 and %d values", alerts.MaxOffsets)
	}
	seen := map[int]bool{}
	var offs []int
	for _, o := range in.OffsetsDays {
		if o < -alerts.MaxOffsetDays || o > alerts.MaxOffsetDays {
			return in, ruleErr("each offset must be within %d days of the date", alerts.MaxOffsetDays)
		}
		if !seen[o] {
			seen[o] = true
			offs = append(offs, o)
		}
	}
	slices.SortFunc(offs, func(a, b int) int { return b - a })
	in.OffsetsDays = offs

	if in.EntityTypeID != nil {
		if err := assertEntityTypeInGroup(ctx, r.db.EntityType, gid, *in.EntityTypeID); err != nil {
			return in, ruleErr("entityTypeId is not one of this collection's types")
		}
	}
	if in.TagID != nil {
		if err := assertTagsInGroup(ctx, r.db.Tag, gid, []uuid.UUID{*in.TagID}); err != nil {
			return in, ruleErr("tagId is not one of this collection's tags")
		}
	}
	in.NotifierIDs = dedupNonNil(in.NotifierIDs)
	if len(in.NotifierIDs) > 0 {
		n, err := r.db.Notifier.Query().
			Where(notifier.IDIn(in.NotifierIDs...), notifier.GroupID(gid)).
			Count(ctx)
		if err != nil {
			return in, err
		}
		if n != len(in.NotifierIDs) {
			return in, ruleErr("notifierIds includes a notifier that is not in this collection")
		}
	}
	return in, nil
}

// GetAll lists a collection's rules, newest first.
func (r *NotificationRuleRepository) GetAll(ctx context.Context, gid uuid.UUID) ([]NotificationRuleOut, error) {
	rows, err := r.db.NotificationRule.Query().
		Where(notificationrule.GroupID(gid)).
		Order(ent.Asc(notificationrule.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]NotificationRuleOut, len(rows))
	for i, row := range rows {
		out[i] = mapRule(row)
	}
	return out, nil
}

// GetOne returns one rule of the collection.
func (r *NotificationRuleRepository) GetOne(ctx context.Context, gid, id uuid.UUID) (NotificationRuleOut, error) {
	row, err := r.db.NotificationRule.Query().
		Where(notificationrule.ID(id), notificationrule.GroupID(gid)).
		Only(ctx)
	if err != nil {
		return NotificationRuleOut{}, err
	}
	return mapRule(row), nil
}

// Create stores a new rule.
func (r *NotificationRuleRepository) Create(ctx context.Context, gid uuid.UUID, in NotificationRuleIn) (NotificationRuleOut, error) {
	in, err := r.validate(ctx, gid, in)
	if err != nil {
		return NotificationRuleOut{}, err
	}
	row, err := r.db.NotificationRule.Create().
		SetGroupID(gid).
		SetName(in.Name).
		SetTrigger(notificationrule.Trigger(in.Trigger)).
		SetOffsetsDays(in.OffsetsDays).
		SetNillableEntityTypeID(in.EntityTypeID).
		SetNillableTagID(in.TagID).
		SetNotifierIds(in.NotifierIDs).
		SetIsActive(in.IsActive).
		Save(ctx)
	if err != nil {
		return NotificationRuleOut{}, err
	}
	return mapRule(row), nil
}

// Update replaces a rule's settings. Alerts already sent stay sent.
func (r *NotificationRuleRepository) Update(ctx context.Context, gid, id uuid.UUID, in NotificationRuleIn) (NotificationRuleOut, error) {
	in, err := r.validate(ctx, gid, in)
	if err != nil {
		return NotificationRuleOut{}, err
	}
	upd := r.db.NotificationRule.Update().
		Where(notificationrule.ID(id), notificationrule.GroupID(gid)).
		SetName(in.Name).
		SetTrigger(notificationrule.Trigger(in.Trigger)).
		SetOffsetsDays(in.OffsetsDays).
		SetNotifierIds(in.NotifierIDs).
		SetIsActive(in.IsActive)
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
		return NotificationRuleOut{}, err
	}
	if n == 0 {
		return NotificationRuleOut{}, &ent.NotFoundError{}
	}
	return r.GetOne(ctx, gid, id)
}

// Delete removes a rule and its send history.
func (r *NotificationRuleRepository) Delete(ctx context.Context, gid, id uuid.UUID) error {
	n, err := r.db.NotificationRule.Delete().
		Where(notificationrule.ID(id), notificationrule.GroupID(gid)).
		Exec(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return &ent.NotFoundError{}
	}
	return nil
}

// Active returns the collection's rules that are switched on.
func (r *NotificationRuleRepository) Active(ctx context.Context, gid uuid.UUID) ([]NotificationRuleOut, error) {
	rows, err := r.db.NotificationRule.Query().
		Where(notificationrule.GroupID(gid), notificationrule.IsActive(true)).
		Order(ent.Asc(notificationrule.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]NotificationRuleOut, len(rows))
	for i, row := range rows {
		out[i] = mapRule(row)
	}
	return out, nil
}

// SeedDefaults gives a collection that already had notifiers the maintenance
// rule they used to get implicitly: a digest on the day maintenance is due. It
// runs once per collection (alert_rules_seeded), so deleting the rule sticks.
func (r *NotificationRuleRepository) SeedDefaults(ctx context.Context, gid uuid.UUID) error {
	g, err := r.db.Group.Get(ctx, gid)
	if err != nil {
		return err
	}
	if g.AlertRulesSeeded {
		return nil
	}
	n, err := r.db.Notifier.Query().Where(notifier.GroupID(gid)).Count(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return nil // nothing to send to yet; try again once there is
	}
	_, err = r.db.NotificationRule.Create().
		SetGroupID(gid).
		SetName("Maintenance due").
		SetTrigger(notificationrule.TriggerMaintenanceDue).
		SetOffsetsDays([]int{0}).
		SetNotifierIds([]uuid.UUID{}).
		Save(ctx)
	if err != nil {
		return err
	}
	return r.db.Group.UpdateOneID(gid).SetAlertRulesSeeded(true).Exec(ctx)
}

// Subjects loads the dated things a rule watches whose date lies in
// [from, to], with the rule's type and tag filters applied. Archived items and,
// for warranties, lifetime warranties are left out; maintenance entries that
// are already done are too.
func (r *NotificationRuleRepository) Subjects(ctx context.Context, gid uuid.UUID, rule NotificationRuleOut, from, to time.Time) ([]alerts.Subject, error) {
	itemFilters := func() []predicate.Entity {
		p := []predicate.Entity{entity.HasGroupWith(group.ID(gid)), entity.Archived(false)}
		if rule.EntityTypeID != nil {
			p = append(p, entity.HasEntityTypeWith(entitytype.ID(*rule.EntityTypeID)))
		}
		if rule.TagID != nil {
			p = append(p, entity.HasTagWith(tag.ID(*rule.TagID)))
		}
		return p
	}

	switch rule.Trigger {
	case alerts.TriggerExpiresAt, alerts.TriggerWarrantyExpires:
		where := itemFilters()
		if rule.Trigger == alerts.TriggerExpiresAt {
			where = append(where, entity.ExpiresAtGTE(from), entity.ExpiresAtLTE(to))
		} else {
			where = append(where, entity.LifetimeWarranty(false), entity.WarrantyExpiresGTE(from), entity.WarrantyExpiresLTE(to))
		}
		rows, err := r.db.Entity.Query().Where(where...).WithParent().All(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]alerts.Subject, 0, len(rows))
		for _, e := range rows {
			s := alerts.Subject{ID: e.ID, EntityID: e.ID, Name: e.Name}
			if rule.Trigger == alerts.TriggerExpiresAt {
				s.Anchor, s.Kind = alerts.Day(e.ExpiresAt.UTC()), string(e.ExpiryKind)
			} else {
				s.Anchor = alerts.Day(e.WarrantyExpires.UTC())
			}
			if e.Edges.Parent != nil {
				s.Location = e.Edges.Parent.Name
			}
			out = append(out, s)
		}
		return out, nil

	case alerts.TriggerMaintenanceDue:
		rows, err := r.db.MaintenanceEntry.Query().
			Where(
				maintenanceentry.HasEntityWith(itemFilters()...),
				maintenanceentry.ScheduledDateGTE(from),
				maintenanceentry.ScheduledDateLTE(to),
				maintenanceentry.Or(maintenanceentry.DateIsNil(), maintenanceentry.DateEQ(time.Time{})),
			).
			WithEntity(func(q *ent.EntityQuery) { q.WithParent() }).
			All(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]alerts.Subject, 0, len(rows))
		for _, m := range rows {
			if m.Edges.Entity == nil {
				continue
			}
			s := alerts.Subject{
				ID: m.ID, EntityID: m.Edges.Entity.ID, Name: m.Edges.Entity.Name,
				Detail: m.Name, Anchor: alerts.Day(m.ScheduledDate.UTC()),
			}
			if m.Edges.Entity.Edges.Parent != nil {
				s.Location = m.Edges.Entity.Edges.Parent.Name
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, errors.New("unknown trigger " + rule.Trigger)
}

// SentSet returns which of the given subjects have already been told about, per
// notifier, offset and date, so they are not told twice.
func (r *NotificationRuleRepository) SentSet(ctx context.Context, ruleID uuid.UUID, subjectIDs []uuid.UUID) (map[SentKey]struct{}, error) {
	out := map[SentKey]struct{}{}
	for chunk := range slices.Chunk(subjectIDs, 500) {
		rows, err := r.db.NotificationLog.Query().
			Where(notificationlog.HasRuleWith(notificationrule.ID(ruleID)), notificationlog.SubjectIDIn(chunk...)).
			All(ctx)
		if err != nil {
			return nil, err
		}
		for _, l := range rows {
			out[SentKey{NotifierID: l.NotifierID, SubjectID: l.SubjectID, Offset: l.OffsetDays, Anchor: alerts.Day(l.AnchorDate.UTC())}] = struct{}{}
		}
	}
	return out, nil
}

// RecordSent remembers delivered alerts. Recording one twice is harmless.
func (r *NotificationRuleRepository) RecordSent(ctx context.Context, ruleID uuid.UUID, entries []SentEntry) error {
	for _, e := range entries {
		_, err := r.db.NotificationLog.Create().
			SetRuleID(ruleID).
			SetNotifierID(e.NotifierID).
			SetSubjectID(e.SubjectID).
			SetOffsetDays(e.Offset).
			SetAnchorDate(e.Anchor).
			SetDueOn(e.DueOn).
			SetSentAt(e.SentAt).
			Save(ctx)
		if err != nil && !ent.IsConstraintError(err) {
			return err
		}
	}
	return nil
}

// PurgeLogs forgets sends older than before. Entries only matter while their
// alert could still come due, which is a few days past its date.
func (r *NotificationRuleRepository) PurgeLogs(ctx context.Context, before time.Time) (int, error) {
	return r.db.NotificationLog.Delete().Where(notificationlog.SentAtLT(before)).Exec(ctx)
}
