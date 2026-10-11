package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
)

func TestImportedField_KeepsExistingType(t *testing.T) {
	date := repo.EntityFieldData{Type: "date"}
	got := importedField("opened", "2026-03-01", date)
	assert.Equal(t, "date", got.Type)
	assert.Equal(t, "2026-03-01", got.TextValue)

	cur := repo.EntityFieldData{Type: "currency", CurrencyCode: "GBP"}
	got = importedField("paid", "19.90", cur)
	assert.Equal(t, "currency", got.Type)
	assert.Equal(t, "GBP", got.CurrencyCode)

	got = importedField("pieces", " 12 ", repo.EntityFieldData{Type: "number"})
	assert.Equal(t, "number", got.Type)
	assert.Equal(t, 12, got.NumberValue)

	got = importedField("sealed", "true", repo.EntityFieldData{Type: "boolean"})
	assert.True(t, got.BooleanValue)
}

func TestImportedField_FallsBackToTextWhenUnparseable(t *testing.T) {
	for name, existing := range map[string]repo.EntityFieldData{
		"date":     {Type: "date"},
		"number":   {Type: "number"},
		"boolean":  {Type: "boolean"},
		"currency": {Type: "currency", CurrencyCode: "USD"},
	} {
		got := importedField("f", "not a value", existing)
		assert.Equal(t, "text", got.Type, name)
		assert.Equal(t, "not a value", got.TextValue, name)
	}
}

func TestImportedField_NewFieldsAreText(t *testing.T) {
	got := importedField("anything", "x", repo.EntityFieldData{})
	assert.Equal(t, "text", got.Type)
}
