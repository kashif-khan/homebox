package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/sysadminsmedia/homebox/backend/internal/core/fieldtypes"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/entityfield"
	"github.com/sysadminsmedia/homebox/backend/internal/data/ent/templatefield"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
)

// normalizeField validates a custom field a client sent and derives its typed
// values. Failures are returned as 422 request errors.
func normalizeField(f EntityFieldData) (fieldtypes.Value, error) {
	return normalizeFieldInput(fieldtypes.Input{
		Type:         f.Type,
		Name:         f.Name,
		TextValue:    f.TextValue,
		NumberValue:  f.NumberValue,
		BooleanValue: f.BooleanValue,
		CurrencyCode: f.CurrencyCode,
		Timezone:     f.Timezone,
		Choices:      f.Choices,
	})
}

func normalizeFieldInput(in fieldtypes.Input) (fieldtypes.Value, error) {
	v, err := fieldtypes.Normalize(in)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, fieldtypes.ErrInvalid) {
			status = http.StatusUnprocessableEntity
		}
		return fieldtypes.Value{}, validate.NewRequestError(fmt.Errorf("custom field %q: %w", in.Name, err), status)
	}
	return v, nil
}

// storedValue rebuilds a Value from a field that is already persisted.
func storedValue(f EntityFieldData) fieldtypes.Value {
	return fieldtypes.Value{
		Type:         fieldtypes.Type(f.Type),
		TextValue:    f.TextValue,
		NumberValue:  f.NumberValue,
		BooleanValue: f.BooleanValue,
		DecimalValue: f.DecimalValue,
		TimeValue:    f.TimeValue,
		CurrencyCode: f.CurrencyCode,
		Timezone:     f.Timezone,
		Choices:      f.Choices,
	}
}

// timeValueFor hides time_value for types that do not use it: the column is
// non-null and defaults to "now", which would otherwise leak into responses.
func timeValueFor(typ string, t time.Time) *time.Time {
	switch fieldtypes.Type(typ) {
	case fieldtypes.Date, fieldtypes.DateTime:
		return &t
	}
	return nil
}

func applyFieldCreate(c *ent.EntityFieldCreate, v fieldtypes.Value) *ent.EntityFieldCreate {
	c.SetType(entityfield.Type(v.Type)).
		SetTextValue(v.TextValue).
		SetNumberValue(v.NumberValue).
		SetBooleanValue(v.BooleanValue).
		SetNillableDecimalValue(v.DecimalValue).
		SetCurrencyCode(v.CurrencyCode).
		SetTimezone(v.Timezone)
	if v.TimeValue != nil {
		c.SetTimeValue(*v.TimeValue)
	}
	if len(v.Choices) > 0 {
		c.SetChoices(v.Choices)
	}
	return c
}

func applyFieldUpdate(u *ent.EntityFieldUpdate, v fieldtypes.Value) *ent.EntityFieldUpdate {
	u.SetType(entityfield.Type(v.Type)).
		SetTextValue(v.TextValue).
		SetNumberValue(v.NumberValue).
		SetBooleanValue(v.BooleanValue).
		SetCurrencyCode(v.CurrencyCode).
		SetTimezone(v.Timezone)
	if v.DecimalValue != nil {
		u.SetDecimalValue(*v.DecimalValue)
	} else {
		u.ClearDecimalValue()
	}
	if v.TimeValue != nil {
		u.SetTimeValue(*v.TimeValue)
	}
	if len(v.Choices) > 0 {
		u.SetChoices(v.Choices)
	} else {
		u.ClearChoices()
	}
	return u
}

func applyTemplateFieldCreate(c *ent.TemplateFieldCreate, v fieldtypes.Value) *ent.TemplateFieldCreate {
	c.SetType(templatefield.Type(v.Type)).
		SetTextValue(v.TextValue).
		SetNumberValue(v.NumberValue).
		SetBooleanValue(v.BooleanValue).
		SetNillableDecimalValue(v.DecimalValue).
		SetCurrencyCode(v.CurrencyCode).
		SetTimezone(v.Timezone)
	if v.TimeValue != nil {
		c.SetTimeValue(*v.TimeValue)
	}
	if len(v.Choices) > 0 {
		c.SetChoices(v.Choices)
	}
	return c
}

func applyTemplateFieldUpdate(u *ent.TemplateFieldUpdate, v fieldtypes.Value) *ent.TemplateFieldUpdate {
	u.SetType(templatefield.Type(v.Type)).
		SetTextValue(v.TextValue).
		SetNumberValue(v.NumberValue).
		SetBooleanValue(v.BooleanValue).
		SetCurrencyCode(v.CurrencyCode).
		SetTimezone(v.Timezone)
	if v.DecimalValue != nil {
		u.SetDecimalValue(*v.DecimalValue)
	} else {
		u.ClearDecimalValue()
	}
	if v.TimeValue != nil {
		u.SetTimeValue(*v.TimeValue)
	}
	if len(v.Choices) > 0 {
		u.SetChoices(v.Choices)
	} else {
		u.ClearChoices()
	}
	return u
}

// The derived values on a custom field (decimalValue, timeValue) are output
// only: the server recomputes them from the canonical text. Older clients sent
// back whatever they held for them, including "" and null, so the decoders
// below accept and ignore any value rather than failing the whole request.

// UnmarshalJSON implements json.Unmarshaler.
func (f *EntityFieldData) UnmarshalJSON(b []byte) error {
	type plain EntityFieldData
	aux := struct {
		*plain
		DecimalValue json.RawMessage `json:"decimalValue"`
		TimeValue    json.RawMessage `json:"timeValue"`
	}{plain: (*plain)(f)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	f.DecimalValue, f.TimeValue = nil, nil
	return nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (f *TemplateField) UnmarshalJSON(b []byte) error {
	type plain TemplateField
	aux := struct {
		*plain
		DecimalValue json.RawMessage `json:"decimalValue"`
		TimeValue    json.RawMessage `json:"timeValue"`
	}{plain: (*plain)(f)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	f.DecimalValue, f.TimeValue = nil, nil
	return nil
}
