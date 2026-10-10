package mcpserver

import (
	"strings"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sysadminsmedia/homebox/backend/internal/core/scopes"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
)

// Templates are reusable blueprints for items: default name, quantity, tags,
// location, warranty details and custom fields. They use the same permissions
// as items, matching the REST API.

type TemplateSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type ListTemplatesIn struct{}
type ListTemplatesOut struct {
	Templates []TemplateSummary `json:"templates"`
}

type TemplateFieldInfo struct {
	Name      string `json:"name"`
	Type      string `json:"type"                jsonschema:"text, number, boolean or time"`
	TextValue string `json:"textValue,omitempty" jsonschema:"default value, stored as text"`
}

type TemplateDetail struct {
	ID                      string              `json:"id"`
	Name                    string              `json:"name"`
	Description             string              `json:"description,omitempty"`
	Notes                   string              `json:"notes,omitempty"`
	DefaultName             string              `json:"defaultName,omitempty"`
	DefaultDescription      string              `json:"defaultDescription,omitempty"`
	DefaultQuantity         float64             `json:"defaultQuantity"`
	DefaultManufacturer     string              `json:"defaultManufacturer,omitempty"`
	DefaultModelNumber      string              `json:"defaultModelNumber,omitempty"`
	DefaultWarrantyDetails  string              `json:"defaultWarrantyDetails,omitempty"`
	DefaultInsured          bool                `json:"defaultInsured,omitempty"`
	DefaultLifetimeWarranty bool                `json:"defaultLifetimeWarranty,omitempty"`
	DefaultLocation         *Ref                `json:"defaultLocation,omitempty"`
	DefaultTags             []Ref               `json:"defaultTags"`
	CustomFields            []TemplateFieldInfo `json:"customFields"`
	IncludeWarrantyFields   bool                `json:"includeWarrantyFields,omitempty"`
	IncludePurchaseFields   bool                `json:"includePurchaseFields,omitempty"`
	IncludeSoldFields       bool                `json:"includeSoldFields,omitempty"`
}

type GetTemplateIn struct {
	ID string `json:"id"`
}

type TemplateFieldIn struct {
	Name      string `json:"name"                jsonschema:"field label, e.g. Serial number"`
	Type      string `json:"type,omitempty"      jsonschema:"text (default), number, boolean or time"`
	TextValue string `json:"textValue,omitempty" jsonschema:"default value"`
}

type CreateTemplateIn struct {
	Name                    string            `json:"name"                              jsonschema:"template name, 1-255 characters"`
	Description             string            `json:"description,omitempty"             jsonschema:"what the template is for, up to 1000 characters"`
	Notes                   string            `json:"notes,omitempty"`
	DefaultName             string            `json:"defaultName,omitempty"             jsonschema:"name given to items made from it"`
	DefaultDescription      string            `json:"defaultDescription,omitempty"`
	DefaultQuantity         *float64          `json:"defaultQuantity,omitempty"`
	DefaultManufacturer     string            `json:"defaultManufacturer,omitempty"`
	DefaultModelNumber      string            `json:"defaultModelNumber,omitempty"`
	DefaultWarrantyDetails  string            `json:"defaultWarrantyDetails,omitempty"`
	DefaultInsured          bool              `json:"defaultInsured,omitempty"`
	DefaultLifetimeWarranty bool              `json:"defaultLifetimeWarranty,omitempty"`
	DefaultLocationID       string            `json:"defaultLocationId,omitempty"`
	DefaultTagIDs           []string          `json:"defaultTagIds,omitempty"`
	CustomFields            []TemplateFieldIn `json:"customFields,omitempty"`
	IncludeWarrantyFields   bool              `json:"includeWarrantyFields,omitempty"   jsonschema:"show warranty fields on items made from it"`
	IncludePurchaseFields   bool              `json:"includePurchaseFields,omitempty"`
	IncludeSoldFields       bool              `json:"includeSoldFields,omitempty"`
}

type UpdateTemplateIn struct {
	ID                      string             `json:"id"`
	Name                    *string            `json:"name,omitempty"`
	Description             *string            `json:"description,omitempty"`
	Notes                   *string            `json:"notes,omitempty"`
	DefaultName             *string            `json:"defaultName,omitempty"`
	DefaultDescription      *string            `json:"defaultDescription,omitempty"`
	DefaultQuantity         *float64           `json:"defaultQuantity,omitempty"`
	DefaultManufacturer     *string            `json:"defaultManufacturer,omitempty"`
	DefaultModelNumber      *string            `json:"defaultModelNumber,omitempty"`
	DefaultWarrantyDetails  *string            `json:"defaultWarrantyDetails,omitempty"`
	DefaultInsured          *bool              `json:"defaultInsured,omitempty"`
	DefaultLifetimeWarranty *bool              `json:"defaultLifetimeWarranty,omitempty"`
	DefaultLocationID       *string            `json:"defaultLocationId,omitempty"       jsonschema:"empty string clears it"`
	DefaultTagIDs           *[]string          `json:"defaultTagIds,omitempty"           jsonschema:"replaces the default tags"`
	CustomFields            *[]TemplateFieldIn `json:"customFields,omitempty"            jsonschema:"replaces all custom fields"`
	IncludeWarrantyFields   *bool              `json:"includeWarrantyFields,omitempty"`
	IncludePurchaseFields   *bool              `json:"includePurchaseFields,omitempty"`
	IncludeSoldFields       *bool              `json:"includeSoldFields,omitempty"`
}

type CreateFromTemplateIn struct {
	TemplateID   string   `json:"templateId"`
	Name         string   `json:"name"                   jsonschema:"name of the new item"`
	ParentID     string   `json:"parentId"               jsonschema:"location or container to put it in"`
	Description  string   `json:"description,omitempty"`
	LocationID   string   `json:"locationId,omitempty"   jsonschema:"only when parentId is another item that lives elsewhere"`
	EntityTypeID string   `json:"entityTypeId,omitempty"`
	TagIDs       []string `json:"tagIds,omitempty"       jsonschema:"tags for the new item"`
	Quantity     *float64 `json:"quantity,omitempty"     jsonschema:"overrides the template's default"`
}

func registerTemplateTools(s *Server, srv *mcp.Server, p *Principal) {
	addTool(s, srv, p, toolDef[ListTemplatesIn, ListTemplatesOut]{
		name: "list_templates", title: "List item templates",
		description: "List the reusable item templates in the collection." + untrusted,
		scope:       scopes.ItemsRead, kind: kindRead,
		handler: toolListTemplates,
	})
	addTool(s, srv, p, toolDef[GetTemplateIn, TemplateDetail]{
		name: "get_template", title: "Get item template",
		description: "Get a template's defaults, default tags and location, and custom fields." + untrusted,
		scope:       scopes.ItemsRead, kind: kindRead,
		handler: toolGetTemplate,
	})
	addTool(s, srv, p, toolDef[CreateTemplateIn, TemplateDetail]{
		name: "create_template", title: "Create item template",
		description: "Create a reusable item template: default name, quantity, manufacturer, tags, location, warranty details and custom fields. Items made from it start with those defaults.",
		scope:       scopes.ItemsWrite, kind: kindCreate,
		handler: toolCreateTemplate,
	})
	addTool(s, srv, p, toolDef[UpdateTemplateIn, TemplateDetail]{
		name: "update_template", title: "Update item template",
		description: "Change a template. Only the fields you pass are changed; everything else is kept.",
		scope:       scopes.ItemsWrite, kind: kindUpdate,
		handler: toolUpdateTemplate,
	})
	addTool(s, srv, p, toolDef[CreateFromTemplateIn, ItemDetail]{
		name: "create_item_from_template", title: "Create item from template",
		description: "Create an item pre-filled from a template's defaults (quantity, manufacturer, warranty details, insurance and custom fields). You must say where it goes with parentId.",
		scope:       scopes.ItemsWrite, kind: kindCreate,
		handler: toolCreateItemFromTemplate,
	})
	addTool(s, srv, p, toolDef[DeleteIn, DeleteOut]{
		name: "delete_template", title: "Delete item template",
		description: "Permanently delete a template. Items already made from it are not affected. Requires confirm=true, which may only be set after the user has explicitly agreed.",
		scope:       scopes.ItemsDelete, kind: kindDestructive,
		handler: toolDeleteTemplate,
	})
}

func (c *call) templateDetail(t repo.EntityTemplateOut) TemplateDetail {
	d := TemplateDetail{
		ID: t.ID.String(), Name: t.Name, Description: c.text(t.Description), Notes: c.text(t.Notes),
		DefaultName: t.DefaultName, DefaultDescription: c.text(t.DefaultDescription), DefaultQuantity: t.DefaultQuantity,
		DefaultManufacturer: t.DefaultManufacturer, DefaultModelNumber: t.DefaultModelNumber,
		DefaultWarrantyDetails: c.text(t.DefaultWarrantyDetails), DefaultInsured: t.DefaultInsured,
		DefaultLifetimeWarranty: t.DefaultLifetimeWarranty,
		DefaultTags:             make([]Ref, 0, len(t.DefaultTags)), CustomFields: make([]TemplateFieldInfo, 0, len(t.Fields)),
		IncludeWarrantyFields: t.IncludeWarrantyFields, IncludePurchaseFields: t.IncludePurchaseFields,
		IncludeSoldFields: t.IncludeSoldFields,
	}
	if t.DefaultLocation != nil {
		d.DefaultLocation = &Ref{ID: t.DefaultLocation.ID.String(), Name: t.DefaultLocation.Name}
	}
	for _, tg := range t.DefaultTags {
		d.DefaultTags = append(d.DefaultTags, Ref{ID: tg.ID.String(), Name: tg.Name})
	}
	for _, f := range t.Fields {
		d.CustomFields = append(d.CustomFields, TemplateFieldInfo{Name: f.Name, Type: f.Type, TextValue: c.text(f.TextValue)})
	}
	return d
}

func toolListTemplates(c *call, _ ListTemplatesIn) (ListTemplatesOut, error) {
	rows, err := c.repos().EntityTemplates.GetAll(c, c.gid())
	if err != nil {
		return ListTemplatesOut{}, err
	}
	out := ListTemplatesOut{Templates: make([]TemplateSummary, 0, len(rows))}
	for _, r := range rows {
		out.Templates = append(out.Templates, TemplateSummary{ID: r.ID.String(), Name: r.Name, Description: c.text(r.Description)})
	}
	return out, nil
}

func toolGetTemplate(c *call, in GetTemplateIn) (TemplateDetail, error) {
	id, err := parseID("id", in.ID)
	if err != nil {
		return TemplateDetail{}, err
	}
	t, err := c.repos().EntityTemplates.GetOne(c, c.gid(), id)
	if err != nil {
		return TemplateDetail{}, err
	}
	return c.templateDetail(t), nil
}

// checkTags rejects tag ids from other collections. The template repository
// validates a default location but not default tags, and would later display
// the names of whatever tags it was given, so the check lives here.
func (c *call) checkTags(ids []uuid.UUID) error {
	for _, id := range ids {
		if _, err := c.repos().Tags.GetOneByGroup(c, c.gid(), id); err != nil {
			return invalid("tag %s not found", id)
		}
	}
	return nil
}

func templateFields(in []TemplateFieldIn) ([]repo.TemplateField, error) {
	out := make([]repo.TemplateField, 0, len(in))
	for _, f := range in {
		name := strings.TrimSpace(f.Name)
		if name == "" || len(name) > 255 {
			return nil, invalid("custom field names must be 1-255 characters")
		}
		typ := f.Type
		switch typ {
		case "":
			typ = "text"
		case "text", "number", "boolean", "time":
		default:
			return nil, invalid("custom field type must be text, number, boolean or time")
		}
		out = append(out, repo.TemplateField{Name: name, Type: typ, TextValue: f.TextValue})
	}
	return out, nil
}

func validateTemplateText(name, description, notes string) error {
	if n := strings.TrimSpace(name); n == "" || len(n) > 255 {
		return invalid("name must be 1-255 characters")
	}
	if len(description) > 1000 || len(notes) > 1000 {
		return invalid("description and notes must be at most 1000 characters")
	}
	return nil
}

func toolCreateTemplate(c *call, in CreateTemplateIn) (TemplateDetail, error) {
	if err := validateTemplateText(in.Name, in.Description, in.Notes); err != nil {
		return TemplateDetail{}, err
	}
	loc, err := parseOptionalID("defaultLocationId", in.DefaultLocationID)
	if err != nil {
		return TemplateDetail{}, err
	}
	tagIDs, err := parseIDs("defaultTagIds", in.DefaultTagIDs)
	if err != nil {
		return TemplateDetail{}, err
	}
	if err := c.checkTags(tagIDs); err != nil {
		return TemplateDetail{}, err
	}
	fields, err := templateFields(in.CustomFields)
	if err != nil {
		return TemplateDetail{}, err
	}

	optStr := func(s string) *string {
		if s == "" {
			return nil
		}
		return &s
	}
	create := repo.EntityTemplateCreate{
		Name: strings.TrimSpace(in.Name), Description: in.Description, Notes: in.Notes,
		DefaultName: optStr(in.DefaultName), DefaultDescription: optStr(in.DefaultDescription),
		DefaultQuantity: in.DefaultQuantity, DefaultManufacturer: optStr(in.DefaultManufacturer),
		DefaultModelNumber: optStr(in.DefaultModelNumber), DefaultWarrantyDetails: optStr(in.DefaultWarrantyDetails),
		DefaultInsured: in.DefaultInsured, DefaultLifetimeWarranty: in.DefaultLifetimeWarranty,
		DefaultLocationID: loc, Fields: fields,
		IncludeWarrantyFields: in.IncludeWarrantyFields, IncludePurchaseFields: in.IncludePurchaseFields,
		IncludeSoldFields: in.IncludeSoldFields,
	}
	if len(tagIDs) > 0 {
		create.DefaultTagIDs = &tagIDs
	}
	out, err := c.repos().EntityTemplates.Create(c, c.gid(), create)
	if err != nil {
		return TemplateDetail{}, err
	}
	return c.templateDetail(out), nil
}

func toolUpdateTemplate(c *call, in UpdateTemplateIn) (TemplateDetail, error) {
	id, err := parseID("id", in.ID)
	if err != nil {
		return TemplateDetail{}, err
	}
	cur, err := c.repos().EntityTemplates.GetOne(c, c.gid(), id)
	if err != nil {
		return TemplateDetail{}, err
	}

	// Start from what is stored, so unmentioned fields are preserved.
	str := func(s string) *string { return &s }
	curTags := make([]uuid.UUID, 0, len(cur.DefaultTags))
	for _, t := range cur.DefaultTags {
		curTags = append(curTags, t.ID)
	}
	qty := cur.DefaultQuantity
	upd := repo.EntityTemplateUpdate{
		ID: id, Name: cur.Name, Description: cur.Description, Notes: cur.Notes,
		DefaultName: str(cur.DefaultName), DefaultDescription: str(cur.DefaultDescription), DefaultQuantity: &qty,
		DefaultManufacturer: str(cur.DefaultManufacturer), DefaultModelNumber: str(cur.DefaultModelNumber),
		DefaultWarrantyDetails: str(cur.DefaultWarrantyDetails), DefaultTagIDs: &curTags,
		DefaultInsured: cur.DefaultInsured, DefaultLifetimeWarranty: cur.DefaultLifetimeWarranty,
		Fields:                cur.Fields,
		IncludeWarrantyFields: cur.IncludeWarrantyFields, IncludePurchaseFields: cur.IncludePurchaseFields,
		IncludeSoldFields: cur.IncludeSoldFields,
	}
	if cur.DefaultLocation != nil {
		upd.DefaultLocationID = cur.DefaultLocation.ID
	}

	if in.Name != nil {
		upd.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		upd.Description = *in.Description
	}
	if in.Notes != nil {
		upd.Notes = *in.Notes
	}
	if in.DefaultName != nil {
		upd.DefaultName = in.DefaultName
	}
	if in.DefaultDescription != nil {
		upd.DefaultDescription = in.DefaultDescription
	}
	if in.DefaultQuantity != nil {
		upd.DefaultQuantity = in.DefaultQuantity
	}
	if in.DefaultManufacturer != nil {
		upd.DefaultManufacturer = in.DefaultManufacturer
	}
	if in.DefaultModelNumber != nil {
		upd.DefaultModelNumber = in.DefaultModelNumber
	}
	if in.DefaultWarrantyDetails != nil {
		upd.DefaultWarrantyDetails = in.DefaultWarrantyDetails
	}
	if in.DefaultInsured != nil {
		upd.DefaultInsured = *in.DefaultInsured
	}
	if in.DefaultLifetimeWarranty != nil {
		upd.DefaultLifetimeWarranty = *in.DefaultLifetimeWarranty
	}
	if in.IncludeWarrantyFields != nil {
		upd.IncludeWarrantyFields = *in.IncludeWarrantyFields
	}
	if in.IncludePurchaseFields != nil {
		upd.IncludePurchaseFields = *in.IncludePurchaseFields
	}
	if in.IncludeSoldFields != nil {
		upd.IncludeSoldFields = *in.IncludeSoldFields
	}
	if in.DefaultLocationID != nil {
		if upd.DefaultLocationID, err = parseOptionalID("defaultLocationId", *in.DefaultLocationID); err != nil {
			return TemplateDetail{}, err
		}
	}
	if in.DefaultTagIDs != nil {
		tags, err := parseIDs("defaultTagIds", *in.DefaultTagIDs)
		if err != nil {
			return TemplateDetail{}, err
		}
		if err := c.checkTags(tags); err != nil {
			return TemplateDetail{}, err
		}
		upd.DefaultTagIDs = &tags
	}
	if in.CustomFields != nil {
		if upd.Fields, err = templateFields(*in.CustomFields); err != nil {
			return TemplateDetail{}, err
		}
	}
	if err := validateTemplateText(upd.Name, upd.Description, upd.Notes); err != nil {
		return TemplateDetail{}, err
	}

	out, err := c.repos().EntityTemplates.Update(c, c.gid(), upd)
	if err != nil {
		return TemplateDetail{}, err
	}
	return c.templateDetail(out), nil
}

func toolCreateItemFromTemplate(c *call, in CreateFromTemplateIn) (ItemDetail, error) {
	tid, err := parseID("templateId", in.TemplateID)
	if err != nil {
		return ItemDetail{}, err
	}
	parent, err := parseID("parentId", in.ParentID)
	if err != nil {
		return ItemDetail{}, err
	}
	loc, err := parseOptionalID("locationId", in.LocationID)
	if err != nil {
		return ItemDetail{}, err
	}
	et, err := parseOptionalID("entityTypeId", in.EntityTypeID)
	if err != nil {
		return ItemDetail{}, err
	}
	tags, err := parseIDs("tagIds", in.TagIDs)
	if err != nil {
		return ItemDetail{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 255 {
		return ItemDetail{}, invalid("name must be 1-255 characters")
	}
	if len(in.Description) > 1000 {
		return ItemDetail{}, invalid("description must be at most 1000 characters")
	}

	t, err := c.repos().EntityTemplates.GetOne(c, c.gid(), tid)
	if err != nil {
		return ItemDetail{}, err
	}
	qty := t.DefaultQuantity
	if in.Quantity != nil {
		qty = *in.Quantity
	}
	fields := make([]repo.EntityFieldData, 0, len(t.Fields))
	for _, f := range t.Fields {
		fields = append(fields, repo.EntityFieldData{
			Type: f.Type, Name: f.Name, TextValue: f.TextValue, NumberValue: f.NumberValue, BooleanValue: f.BooleanValue,
		})
	}
	out, err := c.repos().Entities.CreateFromTemplate(c, c.gid(), repo.EntityCreateFromTemplate{
		Name: name, Description: in.Description, Quantity: qty, ParentID: parent, LocationID: loc,
		EntityTypeID: et, TagIDs: tags, Insured: t.DefaultInsured, Manufacturer: t.DefaultManufacturer,
		ModelNumber: t.DefaultModelNumber, LifetimeWarranty: t.DefaultLifetimeWarranty,
		WarrantyDetails: t.DefaultWarrantyDetails, Fields: fields,
	})
	if err != nil {
		return ItemDetail{}, err
	}
	return c.detail(out), nil
}

func toolDeleteTemplate(c *call, in DeleteIn) (DeleteOut, error) {
	id, err := parseID("id", in.ID)
	if err != nil {
		return DeleteOut{}, err
	}
	if err := requireConfirm(in.Confirm, "this template"); err != nil {
		return DeleteOut{}, err
	}
	// Look it up first so a template in another collection reports "not found"
	// instead of a silent no-op success.
	if _, err := c.repos().EntityTemplates.GetOne(c, c.gid(), id); err != nil {
		return DeleteOut{}, err
	}
	if err := c.repos().EntityTemplates.Delete(c, c.gid(), id); err != nil {
		return DeleteOut{}, err
	}
	return DeleteOut{Deleted: id.String()}, nil
}
