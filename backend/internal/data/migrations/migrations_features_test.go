package migrations_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entityfield"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entitylookup"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/notificationlog"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/notificationrule"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/templatefield"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/webhookdelivery"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/webhookendpoint"
	"github.com/sysadminsmedia/homebox/backend/internal/data/migrations"
	_ "github.com/sysadminsmedia/homebox/backend/internal/data/migrations/postgres"
	"github.com/sysadminsmedia/homebox/backend/internal/data/types"
)

// TestSqliteFeatureMigrationsMatchEnt applies the real SQL migrations and then
// writes and reads every column and table the regional, typed-field, expiry,
// alert-rule and webhook migrations added, through Ent. A column the SQL forgot
// or misnamed fails here instead of in production.
func TestSqliteFeatureMigrationsMatchEnt(t *testing.T) {
	c, err := ent.Open("sqlite3", "file:migfeat?mode=memory&cache=shared&_fk=1&_time_format=sqlite")
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	fs, err := migrations.Migrations("sqlite3")
	require.NoError(t, err)
	goose.SetBaseFS(fs)
	require.NoError(t, goose.SetDialect("sqlite3"))
	require.NoError(t, goose.Up(c.Sql(), "sqlite3"))

	exerciseFeatureMigrations(t, c)
}

// TestPostgresFeatureMigrationsMatchEnt runs the same checks on PostgreSQL, where
// column types (timestamptz, jsonb) and constraint behaviour differ.
func TestPostgresFeatureMigrationsMatchEnt(t *testing.T) {
	url := os.Getenv("HBOX_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("set HBOX_TEST_POSTGRES_URL to run against a disposable postgres database")
	}
	db, err := sql.Open("pgx", url)
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	c := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))

	fs, err := migrations.Migrations("postgres")
	require.NoError(t, err)
	goose.SetBaseFS(fs)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.Up(db, "postgres"))

	exerciseFeatureMigrations(t, c)
}

func exerciseFeatureMigrations(t *testing.T, c *ent.Client) {
	t.Helper()
	ctx := context.Background()

	// Regional settings.
	g, err := c.Group.Create().SetName("g").SetTimezone("Asia/Karachi").SetNotifyHour(9).SetAlertRulesSeeded(true).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, "Asia/Karachi", g.Timezone)
	require.Equal(t, 9, g.NotifyHour)
	u, err := c.User.Create().SetName("u").SetEmail(uuid.NewString() + "@example.com").SetCountry("PK").SetTimezone("Asia/Karachi").SetLocale("en-PK").Save(ctx)
	require.NoError(t, err)
	require.Equal(t, "PK", u.Country)

	// Expiry on entities, types and templates.
	et, err := c.EntityType.Create().SetGroupID(g.ID).SetName("Medicine").SetExpiryRequired(true).Save(ctx)
	require.NoError(t, err)
	require.True(t, et.ExpiryRequired)
	exp := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	e, err := c.Entity.Create().SetGroupID(g.ID).SetEntityTypeID(et.ID).SetName("Aspirin").SetExpiresAt(exp).Save(ctx)
	require.NoError(t, err)
	require.True(t, exp.Equal(e.ExpiresAt))
	require.Equal(t, "expires", string(e.ExpiryKind))
	tpl, err := c.EntityTemplate.Create().SetGroupID(g.ID).SetName("t").SetIncludeExpiryFields(true).SetDefaultExpiryOffsetDays(30).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, 30, *tpl.DefaultExpiryOffsetDays)

	// Typed custom fields, on both tables.
	f, err := c.EntityField.Create().SetEntityID(e.ID).SetName("price").SetType(entityfield.TypeCurrency).
		SetDecimalValue(4.5).SetCurrencyCode("PKR").SetTimezone("Asia/Karachi").SetChoices([]string{"a", "b"}).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, f.Choices)
	tf, err := c.TemplateField.Create().SetEntityTemplateID(tpl.ID).SetName("grade").SetType(templatefield.TypeSelect).
		SetChoices([]string{"x"}).SetCurrencyCode("").Save(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"x"}, tf.Choices)

	// Alert rules and their send log, including the once-only constraint.
	rule, err := c.NotificationRule.Create().SetGroupID(g.ID).SetName("r").SetTrigger(notificationrule.TriggerExpiresAt).
		SetOffsetsDays([]int{7, 0}).SetNotifierIds([]uuid.UUID{uuid.New()}).SetEntityTypeID(et.ID).Save(ctx)
	require.NoError(t, err)
	nid, sid := uuid.New(), uuid.New()
	mk := func() error {
		_, err := c.NotificationLog.Create().SetRuleID(rule.ID).SetNotifierID(nid).SetSubjectID(sid).
			SetOffsetDays(7).SetAnchorDate(exp).SetDueOn(exp).SetSentAt(time.Now()).Save(ctx)
		return err
	}
	require.NoError(t, mk())
	require.True(t, ent.IsConstraintError(mk()), "the same alert cannot be recorded twice")

	// Webhooks, including the per-endpoint de-duplication key.
	ep, err := c.WebhookEndpoint.Create().SetGroupID(g.ID).SetName("n8n").SetURL("https://example.com/h").SetSecret("s").
		SetEvents([]string{"item.created"}).Save(ctx)
	require.NoError(t, err)
	key := "k1"
	mkd := func(k *string) error {
		_, err := c.WebhookDelivery.Create().SetEndpointID(ep.ID).SetEventType("item.created").SetEventID(uuid.New()).
			SetOccurredAt(time.Now()).SetData(map[string]any{"a": 1}).SetNillableDedupeKey(k).SetNextAttemptAt(time.Now()).Save(ctx)
		return err
	}
	require.NoError(t, mkd(&key))
	require.True(t, ent.IsConstraintError(mkd(&key)), "one delivery per endpoint for a dedupe key")
	require.NoError(t, mkd(nil))
	require.NoError(t, mkd(nil), "deliveries without a key are never merged")

	// Product lookups: one per item, candidates stored as JSON.
	lk, err := c.EntityLookup.Create().SetGroupID(g.ID).SetEntityID(e.ID).SetQuery("aspirin").
		SetCandidates([]types.LookupCandidate{{Name: "Aspirin 100mg", Confidence: 0.8, Sources: []string{"openfoodfacts"}}}).Save(ctx)
	require.NoError(t, err)
	require.Equal(t, entitylookup.StatusPending, lk.Status)
	got, err := c.EntityLookup.Get(ctx, lk.ID)
	require.NoError(t, err)
	require.Equal(t, "Aspirin 100mg", got.Candidates[0].Name)
	_, err = c.EntityLookup.Create().SetGroupID(g.ID).SetEntityID(e.ID).Save(ctx)
	require.True(t, ent.IsConstraintError(err), "an item has one lookup")

	// Deleting the collection cascades through all of it.
	require.NoError(t, c.Group.DeleteOneID(g.ID).Exec(ctx))
	// Counted for this test's own rows: a shared database may hold others.
	n, err := c.WebhookDelivery.Query().Where(webhookdelivery.HasEndpointWith(webhookendpoint.ID(ep.ID))).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
	nl, err := c.EntityLookup.Query().Where(entitylookup.ID(lk.ID)).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, nl, "lookups go with the collection")
	n, err = c.NotificationLog.Query().Where(notificationlog.HasRuleWith(notificationrule.ID(rule.ID))).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, n)
}
