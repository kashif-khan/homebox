package repo

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entity"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/group"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/predicate"
	"github.com/sysadminsmedia/homebox/backend/internal/data/types"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Expiry kinds. "expires" is the plain case; the other two mirror the labels on
// food and medicine packaging.
const (
	ExpiryKindExpires    = "expires"
	ExpiryKindUseBy      = "use_by"
	ExpiryKindBestBefore = "best_before"
)

// ValidExpiryKind reports whether k is a known expiry kind. The empty string is
// accepted and means "expires".
func ValidExpiryKind(k string) bool {
	switch k {
	case "", ExpiryKindExpires, ExpiryKindUseBy, ExpiryKindBestBefore:
		return true
	}
	return false
}

func expiryKindOrDefault(k string) string {
	if k == "" {
		return ExpiryKindExpires
	}
	return k
}

// orderByNullsLast sorts a nullable column with NULLs at the end whichever way
// it is sorted, so items with no date never crowd the top of a list.
func orderByNullsLast(col string, desc bool) entity.OrderOption {
	opts := []sql.OrderTermOption{sql.OrderNullsLast()}
	if desc {
		opts = append(opts, sql.OrderDesc())
	}
	return entity.OrderOption(sql.OrderByField(col, opts...).ToFunc())
}

func expiryError(format string, a ...any) error {
	return validate.NewRequestError(fmt.Errorf(format, a...), http.StatusUnprocessableEntity)
}

// validateExpiry checks the kind and, when the entity's type requires an
// expiry date, that one was given. typeID may be uuid.Nil, meaning "keep the
// entity's current type"; entityID is used to look that up on updates.
func (r *EntityRepository) validateExpiry(ctx context.Context, gid, entityID, typeID uuid.UUID, kind string, expiresAt types.Date) error {
	if !ValidExpiryKind(kind) {
		return expiryError("expiryKind must be one of expires, use_by, best_before")
	}
	if !expiresAt.Time().IsZero() {
		return nil
	}

	if typeID == uuid.Nil && entityID != uuid.Nil {
		e, err := r.db.Entity.Query().
			Where(entity.ID(entityID), entity.HasGroupWith(group.ID(gid))).
			WithEntityType().
			Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				return nil // the caller reports the missing entity
			}
			return err
		}
		if e.Edges.EntityType != nil {
			typeID = e.Edges.EntityType.ID
		}
	}
	if typeID == uuid.Nil {
		return nil
	}

	et, err := r.db.EntityType.Get(ctx, typeID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil
		}
		return err
	}
	if et.ExpiryRequired {
		return expiryError("items of type %q need an expiry date", et.Name)
	}
	return nil
}

// expiryPredicates turns the query's expiry bounds into predicates.
func expiryPredicates(q EntityQuery) []predicate.Entity {
	var out []predicate.Entity
	if q.ExpiresBefore != nil {
		out = append(out, entity.ExpiresAtNotNil(), entity.ExpiresAtLTE(*q.ExpiresBefore))
	}
	if q.ExpiresAfter != nil {
		out = append(out, entity.ExpiresAtNotNil(), entity.ExpiresAtGTE(*q.ExpiresAfter))
	}
	return out
}

// ExpiryEntry is an entity together with its expiry date.
type ExpiryEntry struct {
	EntitySummary
	// DaysLeft is negative once the date has passed.
	DaysLeft int `json:"daysLeft"`
}

// ExpiringResult is the answer to "what is expiring": the expired and the
// soon-to-expire items, each capped, with the real totals.
type ExpiringResult struct {
	Expired      []ExpiryEntry `json:"expired"`
	ExpiredTotal int           `json:"expiredTotal"`
	Soon         []ExpiryEntry `json:"soon"`
	SoonTotal    int           `json:"soonTotal"`
}

// Expiring returns non-archived entities that have already expired and those
// that expire within days, measured from today in loc. Soonest first for items
// still to expire, most recently expired first for the others.
func (r *EntityRepository) Expiring(ctx context.Context, gid uuid.UUID, today time.Time, days, limit int) (ExpiringResult, error) {
	ctx, span := entityTracer().Start(ctx, "repo.EntityRepository.Expiring",
		trace.WithAttributes(
			attribute.String("group.id", gid.String()),
			attribute.Int("days", days),
			attribute.Int("limit", limit),
		))
	defer span.End()

	if days < 0 {
		return ExpiringResult{}, errors.New("days must not be negative")
	}
	day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)

	base := []predicate.Entity{
		entity.HasGroupWith(group.ID(gid)),
		entity.Archived(false),
		entity.ExpiresAtNotNil(),
	}

	load := func(extra predicate.Entity, order entity.OrderOption) ([]ExpiryEntry, int, error) {
		where := append(append([]predicate.Entity{}, base...), extra)
		total, err := r.db.Entity.Query().Where(where...).Count(ctx)
		if err != nil {
			return nil, 0, err
		}
		rows, err := r.db.Entity.Query().Where(where...).
			WithParent().WithEntityType().WithTag().
			Order(order, ent.Asc(entity.FieldName)).
			Limit(limit).All(ctx)
		if err != nil {
			return nil, 0, err
		}
		out := make([]ExpiryEntry, len(rows))
		for i, e := range rows {
			out[i] = ExpiryEntry{
				EntitySummary: mapEntitySummary(e),
				DaysLeft:      int(e.ExpiresAt.UTC().Sub(day).Hours() / 24),
			}
		}
		return out, total, nil
	}

	var res ExpiringResult
	var err error
	res.Expired, res.ExpiredTotal, err = load(entity.ExpiresAtLT(day), ent.Desc(entity.FieldExpiresAt))
	if err != nil {
		recordSpanError(span, err)
		return ExpiringResult{}, err
	}
	res.Soon, res.SoonTotal, err = load(
		entity.And(entity.ExpiresAtGTE(day), entity.ExpiresAtLTE(day.AddDate(0, 0, days))),
		ent.Asc(entity.FieldExpiresAt),
	)
	if err != nil {
		recordSpanError(span, err)
		return ExpiringResult{}, err
	}
	return res, nil
}

// MaxExpiryOffsetDays bounds a template's default expiry offset (100 years).
const MaxExpiryOffsetDays = 36500

// validateTemplateExpiry checks a template's expiry defaults.
func validateTemplateExpiry(kind string, offsetDays *int) error {
	if !ValidExpiryKind(kind) {
		return expiryError("defaultExpiryKind must be one of expires, use_by, best_before")
	}
	if offsetDays != nil && (*offsetDays < 0 || *offsetDays > MaxExpiryOffsetDays) {
		return expiryError("defaultExpiryOffsetDays must be between 0 and %d", MaxExpiryOffsetDays)
	}
	return nil
}

// TemplateExpiry works out the expiry date and kind for an item created from a
// template: the template's offset counted from today's date in loc (the
// collection's time zone). It returns the zero Date when the template sets no
// expiry.
func TemplateExpiry(t EntityTemplateOut, now time.Time, loc *time.Location) (types.Date, string) {
	if !t.IncludeExpiryFields || t.DefaultExpiryOffsetDays == nil {
		return types.Date{}, t.DefaultExpiryKind
	}
	local := now.In(loc)
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	return types.DateFromTime(day.AddDate(0, 0, *t.DefaultExpiryOffsetDays)), t.DefaultExpiryKind
}
