package repo

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
)

func typedFieldSet() []EntityFieldData {
	return []EntityFieldData{
		{Name: "plain", Type: "text", TextValue: "hello"},
		{Name: "count", Type: "number", NumberValue: 4},
		{Name: "flag", Type: "boolean", BooleanValue: true},
		{Name: "weight", Type: "decimal", TextValue: "12.50"},
		{Name: "paid", Type: "currency", TextValue: "19.9", CurrencyCode: "gbp"},
		{Name: "opened", Type: "date", TextValue: "2026-03-01"},
		{Name: "reminder", Type: "datetime", TextValue: "2026-10-10T08:00", Timezone: "Asia/Karachi"},
		{Name: "alarm", Type: "time", TextValue: "07:30"},
		{Name: "manual", Type: "url", TextValue: "https://example.com/m.pdf"},
		{Name: "grade", Type: "select", TextValue: "b", Choices: []string{"a", "b"}},
	}
}

func updateWithFields(t *testing.T, e EntityOut, fields []EntityFieldData) (EntityOut, error) {
	t.Helper()
	upd := EntityUpdate{ID: e.ID, Name: e.Name, Fields: fields}
	if e.EntityType != nil {
		upd.EntityTypeID = e.EntityType.ID
	}
	return tRepos.Entities.UpdateByGroup(context.Background(), tGroup.ID, upd)
}

func byName(fields []EntityFieldData) map[string]EntityFieldData {
	m := make(map[string]EntityFieldData, len(fields))
	for _, f := range fields {
		m[f.Name] = f
	}
	return m
}

func TestEntityFields_TypedRoundTrip(t *testing.T) {
	e := useEntities(t, 1)[0]

	out, err := updateWithFields(t, e, typedFieldSet())
	require.NoError(t, err)

	got := byName(out.Fields)
	require.Len(t, got, 10)

	assert.Equal(t, "hello", got["plain"].TextValue)
	assert.Equal(t, 4, got["count"].NumberValue)
	assert.True(t, got["flag"].BooleanValue)

	require.NotNil(t, got["weight"].DecimalValue)
	assert.InDelta(t, 12.5, *got["weight"].DecimalValue, 1e-9)
	assert.Equal(t, "12.5", got["weight"].TextValue)

	assert.Equal(t, 1990, got["paid"].NumberValue, "stored as minor units")
	assert.Equal(t, "GBP", got["paid"].CurrencyCode)
	assert.Equal(t, "19.90", got["paid"].TextValue)

	require.NotNil(t, got["opened"].TimeValue)
	assert.Equal(t, "2026-03-01", got["opened"].TextValue)
	assert.Equal(t, time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), got["opened"].TimeValue.UTC())

	require.NotNil(t, got["reminder"].TimeValue)
	assert.Equal(t, time.Date(2026, 10, 10, 3, 0, 0, 0, time.UTC), got["reminder"].TimeValue.UTC(), "08:00 in Karachi is 03:00 UTC")
	assert.Equal(t, "Asia/Karachi", got["reminder"].Timezone)
	assert.Equal(t, "2026-10-10T08:00:00+05:00", got["reminder"].TextValue)

	assert.Equal(t, "07:30", got["alarm"].TextValue)
	assert.Nil(t, got["alarm"].TimeValue, "types that do not use time_value must not leak the default")
	assert.Equal(t, "https://example.com/m.pdf", got["manual"].TextValue)
	assert.Equal(t, []string{"a", "b"}, got["grade"].Choices)

	// Re-reading from the database gives the same thing.
	again, err := tRepos.Entities.GetOneByGroup(context.Background(), tGroup.ID, e.ID)
	require.NoError(t, err)
	assert.Equal(t, got["paid"].NumberValue, byName(again.Fields)["paid"].NumberValue)
}

func TestEntityFields_InvalidValueIsRejectedAndNothingChanges(t *testing.T) {
	e := useEntities(t, 1)[0]

	_, err := updateWithFields(t, e, []EntityFieldData{{Name: "keep", Type: "text", TextValue: "v1"}})
	require.NoError(t, err)

	for name, f := range map[string]EntityFieldData{
		"bad date":     {Name: "d", Type: "date", TextValue: "31/12/2026"},
		"bad currency": {Name: "c", Type: "currency", TextValue: "1.234", CurrencyCode: "USD"},
		"bad choice":   {Name: "s", Type: "select", TextValue: "z", Choices: []string{"a"}},
		"bad url":      {Name: "u", Type: "url", TextValue: "javascript:alert(1)"},
		"unknown type": {Name: "g", Type: "geo", TextValue: "1,2"},
	} {
		_, err := updateWithFields(t, e, []EntityFieldData{f})
		require.Error(t, err, name)
		var re *validate.RequestError
		require.ErrorAs(t, err, &re, name)
		assert.Equal(t, http.StatusUnprocessableEntity, re.Status, name)
	}

	still, err := tRepos.Entities.GetOneByGroup(context.Background(), tGroup.ID, e.ID)
	require.NoError(t, err)
	require.Len(t, still.Fields, 1)
	assert.Equal(t, "v1", still.Fields[0].TextValue)
}

func TestEntityFields_ChangingTypeClearsStaleValues(t *testing.T) {
	e := useEntities(t, 1)[0]
	out, err := updateWithFields(t, e, []EntityFieldData{{Name: "x", Type: "decimal", TextValue: "3.5"}})
	require.NoError(t, err)
	require.NotNil(t, out.Fields[0].DecimalValue)

	out, err = updateWithFields(t, e, []EntityFieldData{{ID: out.Fields[0].ID, Name: "x", Type: "text", TextValue: "three"}})
	require.NoError(t, err)
	require.Len(t, out.Fields, 1)
	assert.Nil(t, out.Fields[0].DecimalValue)
	assert.Equal(t, "text", out.Fields[0].Type)
}

func TestTemplateFields_TypedAndAppliedToItems(t *testing.T) {
	data := templateFactory()
	data.Fields = []TemplateField{
		{Name: "expiry", Type: "date", TextValue: "2027-01-31"},
		{Name: "dose", Type: "decimal", TextValue: "2.5"},
		{Name: "tier", Type: "select", TextValue: "gold", Choices: []string{"gold", "silver"}},
		{Name: "pieces", Type: "number", NumberValue: 12},
		{Name: "sealed", Type: "boolean", BooleanValue: true},
	}
	tpl, err := tRepos.EntityTemplates.Create(context.Background(), tGroup.ID, data)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tRepos.EntityTemplates.Delete(context.Background(), tGroup.ID, tpl.ID) })

	got := map[string]TemplateField{}
	for _, f := range tpl.Fields {
		got[f.Name] = f
	}
	require.Len(t, got, 5)
	assert.Equal(t, "2027-01-31", got["expiry"].TextValue)
	require.NotNil(t, got["expiry"].TimeValue)
	assert.Equal(t, 12, got["pieces"].NumberValue, "number defaults used to be dropped")
	assert.True(t, got["sealed"].BooleanValue)
	assert.Equal(t, []string{"gold", "silver"}, got["tier"].Choices)

	// An invalid default is refused and no template is left behind.
	bad := templateFactory()
	bad.Fields = []TemplateField{{Name: "expiry", Type: "date", TextValue: "soon"}}
	_, err = tRepos.EntityTemplates.Create(context.Background(), tGroup.ID, bad)
	require.Error(t, err)

	// Creating an item from the template carries every typed value across.
	fields := make([]EntityFieldData, 0, len(tpl.Fields))
	for _, f := range tpl.Fields {
		fields = append(fields, f.ToEntityField())
	}
	item, err := tRepos.Entities.CreateFromTemplate(context.Background(), tGroup.ID, EntityCreateFromTemplate{
		Name: fk.Str(10), Quantity: 1, Fields: fields,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tRepos.Entities.Delete(context.Background(), item.ID) })

	full, err := tRepos.Entities.GetOneByGroup(context.Background(), tGroup.ID, item.ID)
	require.NoError(t, err)
	itemFields := byName(full.Fields)
	require.Len(t, itemFields, 5)
	assert.Equal(t, "2027-01-31", itemFields["expiry"].TextValue)
	assert.Equal(t, 12, itemFields["pieces"].NumberValue)
	require.NotNil(t, itemFields["dose"].DecimalValue)
	assert.Equal(t, []string{"gold", "silver"}, itemFields["tier"].Choices)

	// And so does duplicating the item.
	dup, err := tRepos.Entities.Duplicate(context.Background(), tGroup.ID, item.ID, DuplicateOptions{CopyCustomFields: true, CopyPrefix: "Copy of "})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tRepos.Entities.Delete(context.Background(), dup.ID) })
	dupFull, err := tRepos.Entities.GetOneByGroup(context.Background(), tGroup.ID, dup.ID)
	require.NoError(t, err)
	dupFields := byName(dupFull.Fields)
	require.NotNil(t, dupFields["expiry"].TimeValue)
	assert.Equal(t, itemFields["expiry"].TimeValue.UTC(), dupFields["expiry"].TimeValue.UTC())
	assert.Equal(t, []string{"gold", "silver"}, dupFields["tier"].Choices)
}

func TestFieldJSON_IgnoresDerivedValuesFromOlderClients(t *testing.T) {
	for _, in := range []string{
		`{"name":"a","type":"text","textValue":"x","timeValue":""}`,
		`{"name":"a","type":"text","textValue":"x","timeValue":null,"decimalValue":null}`,
		`{"name":"a","type":"text","textValue":"x","timeValue":"2026-01-01T00:00:00Z","decimalValue":1.5}`,
		`{"name":"a","type":"text","textValue":"x","timeValue":"garbage","decimalValue":"nope"}`,
	} {
		var f EntityFieldData
		require.NoError(t, json.Unmarshal([]byte(in), &f), in)
		assert.Equal(t, "x", f.TextValue, in)
		assert.Nil(t, f.TimeValue, in)
		assert.Nil(t, f.DecimalValue, in)

		var tf TemplateField
		require.NoError(t, json.Unmarshal([]byte(in), &tf), in)
		assert.Equal(t, "x", tf.TextValue, in)
		assert.Nil(t, tf.TimeValue, in)
	}

	// Output still carries them.
	now := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	d := 2.5
	out, err := json.Marshal(EntityFieldData{Name: "a", Type: "date", TimeValue: &now, DecimalValue: &d})
	require.NoError(t, err)
	assert.Contains(t, string(out), `"timeValue":"2026-03-01T00:00:00Z"`)
	assert.Contains(t, string(out), `"decimalValue":2.5`)
}
