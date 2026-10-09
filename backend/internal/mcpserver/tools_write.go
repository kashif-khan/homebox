package mcpserver

import (
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sysadminsmedia/homebox/backend/internal/core/scopes"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/data/types"
)

// ---- items ----

type CreateItemIn struct {
	Name         string   `json:"name"                   jsonschema:"1-255 characters"`
	ParentID     string   `json:"parentId,omitempty"     jsonschema:"location (or container item) it is stored in; find ids with list_locations or search_items"`
	Description  string   `json:"description,omitempty"  jsonschema:"up to 1000 characters"`
	Quantity     float64  `json:"quantity,omitempty"     jsonschema:"defaults to 1"`
	TagIDs       []string `json:"tagIds,omitempty"`
	Manufacturer string   `json:"manufacturer,omitempty"`
	ModelNumber  string   `json:"modelNumber,omitempty"`
	EntityTypeID string   `json:"entityTypeId,omitempty" jsonschema:"defaults to the collection's default item type"`
}

type CreateLocationIn struct {
	Name        string `json:"name"                  jsonschema:"1-255 characters"`
	ParentID    string `json:"parentId,omitempty"    jsonschema:"enclosing location; omit for a top-level location"`
	Description string `json:"description,omitempty"`
}

type UpdateItemIn struct {
	ID              string    `json:"id"`
	Name            *string   `json:"name,omitempty"`
	Description     *string   `json:"description,omitempty"`
	Notes           *string   `json:"notes,omitempty"`
	Quantity        *float64  `json:"quantity,omitempty"`
	SerialNumber    *string   `json:"serialNumber,omitempty"`
	ModelNumber     *string   `json:"modelNumber,omitempty"`
	Manufacturer    *string   `json:"manufacturer,omitempty"`
	PurchaseFrom    *string   `json:"purchaseFrom,omitempty"`
	PurchasePrice   *float64  `json:"purchasePrice,omitempty"`
	PurchaseDate    *string   `json:"purchaseDate,omitempty"     jsonschema:"YYYY-MM-DD, or empty to clear"`
	WarrantyExpires *string   `json:"warrantyExpires,omitempty"  jsonschema:"YYYY-MM-DD, or empty to clear"`
	WarrantyDetails *string   `json:"warrantyDetails,omitempty"`
	Lifetime        *bool     `json:"lifetimeWarranty,omitempty"`
	Insured         *bool     `json:"insured,omitempty"`
	Archived        *bool     `json:"archived,omitempty"`
	TagIDs          *[]string `json:"tagIds,omitempty"           jsonschema:"replaces the full tag list; use update_item_tags to add or remove individual tags"`
}

type MoveItemIn struct {
	ID         string `json:"id"`
	ParentID   string `json:"parentId"             jsonschema:"the location or container to move it into"`
	LocationID string `json:"locationId,omitempty" jsonschema:"only when parentId is another item that lives in a different location"`
}

type UpdateItemTagsIn struct {
	ID     string   `json:"id"`
	Add    []string `json:"add,omitempty"    jsonschema:"tag ids to add"`
	Remove []string `json:"remove,omitempty" jsonschema:"tag ids to remove"`
}

type DeleteIn struct {
	ID      string `json:"id"`
	Confirm bool   `json:"confirm" jsonschema:"must be true; only set after the user explicitly agreed to this deletion"`
}

type DeleteOut struct {
	Deleted string `json:"deleted"`
}

// ---- tags ----

type CreateTagIn struct {
	Name        string `json:"name"`
	ParentID    string `json:"parentId,omitempty"`
	Description string `json:"description,omitempty"`
	Color       string `json:"color,omitempty"`
}

type UpdateTagIn struct {
	ID          string  `json:"id"`
	Name        *string `json:"name,omitempty"`
	ParentID    *string `json:"parentId,omitempty"    jsonschema:"empty string makes it a top-level tag"`
	Description *string `json:"description,omitempty"`
	Color       *string `json:"color,omitempty"`
}

// ---- maintenance ----

type AddMaintenanceIn struct {
	ItemID        string  `json:"itemId"`
	Name          string  `json:"name"`
	Description   string  `json:"description,omitempty"`
	ScheduledDate string  `json:"scheduledDate,omitempty" jsonschema:"YYYY-MM-DD; give this and/or completedDate"`
	CompletedDate string  `json:"completedDate,omitempty" jsonschema:"YYYY-MM-DD"`
	Cost          float64 `json:"cost,omitempty"`
}

type UpdateMaintenanceIn struct {
	ItemID        string   `json:"itemId"                  jsonschema:"the item the entry belongs to"`
	ID            string   `json:"id"`
	Name          *string  `json:"name,omitempty"`
	Description   *string  `json:"description,omitempty"`
	ScheduledDate *string  `json:"scheduledDate,omitempty" jsonschema:"YYYY-MM-DD, or empty to clear"`
	CompletedDate *string  `json:"completedDate,omitempty" jsonschema:"YYYY-MM-DD; set this to mark the work done"`
	Cost          *float64 `json:"cost,omitempty"`
}

func registerWriteTools(s *Server, srv *mcp.Server, p *Principal) {
	addTool(s, srv, p, toolDef[CreateItemIn, ItemDetail]{
		name: "create_item", title: "Create item",
		description: "Create an item. Search first to avoid duplicates. Put it somewhere with parentId.",
		scope:       scopes.ItemsWrite, kind: kindCreate,
		handler: toolCreateItem,
	})

	addTool(s, srv, p, toolDef[CreateLocationIn, ItemDetail]{
		name: "create_location", title: "Create location",
		description: "Create a location such as a room, shelf or box. Nest it with parentId.",
		scope:       scopes.ItemsWrite, kind: kindCreate,
		handler: toolCreateLocation,
	})

	addTool(s, srv, p, toolDef[UpdateItemIn, ItemDetail]{
		name: "update_item", title: "Update item",
		description: "Change fields of an item or location. Only the fields you pass are changed; everything else is kept.",
		scope:       scopes.ItemsWrite, kind: kindUpdate,
		handler: toolUpdateItem,
	})

	addTool(s, srv, p, toolDef[MoveItemIn, ItemDetail]{
		name: "move_item", title: "Move item",
		description: "Move an item or location into another location or container.",
		scope:       scopes.ItemsWrite, kind: kindUpdate,
		handler: toolMoveItem,
	})

	addTool(s, srv, p, toolDef[UpdateItemTagsIn, ItemDetail]{
		name: "update_item_tags", title: "Add or remove tags",
		description: "Add and/or remove tags on an item without disturbing its other tags.",
		scope:       scopes.ItemsWrite, kind: kindUpdate,
		handler: toolUpdateItemTags,
	})

	addTool(s, srv, p, toolDef[DeleteIn, DeleteOut]{
		name: "delete_item", title: "Delete item",
		description: "Permanently delete an item or location, and what it contains. Irreversible. Requires confirm=true, which may only be set after the user has explicitly agreed to deleting this specific record.",
		scope:       scopes.ItemsDelete, kind: kindDestructive,
		handler: toolDeleteItem,
	})

	addTool(s, srv, p, toolDef[CreateTagIn, TagInfo]{
		name: "create_tag", title: "Create tag",
		description: "Create a tag, optionally nested under a parent tag.",
		scope:       scopes.ItemsWrite, kind: kindCreate,
		handler: toolCreateTag,
	})

	addTool(s, srv, p, toolDef[UpdateTagIn, TagInfo]{
		name: "update_tag", title: "Update tag",
		description: "Rename, recolour or re-parent a tag. Only the fields you pass are changed.",
		scope:       scopes.ItemsWrite, kind: kindUpdate,
		handler: toolUpdateTag,
	})

	addTool(s, srv, p, toolDef[DeleteIn, DeleteOut]{
		name: "delete_tag", title: "Delete tag",
		description: "Permanently delete a tag and remove it from every item. Requires confirm=true, which may only be set after the user has explicitly agreed.",
		scope:       scopes.ItemsDelete, kind: kindDestructive,
		handler: toolDeleteTag,
	})

	addTool(s, srv, p, toolDef[AddMaintenanceIn, MaintenanceInfo]{
		name: "add_maintenance_entry", title: "Add maintenance entry",
		description: "Record maintenance on an item: schedule future work (scheduledDate), log completed work (completedDate), or both.",
		scope:       scopes.MaintenanceWrite, kind: kindCreate,
		handler: toolAddMaintenanceEntry,
	})

	addTool(s, srv, p, toolDef[UpdateMaintenanceIn, MaintenanceInfo]{
		name: "update_maintenance_entry", title: "Update maintenance entry",
		description: "Change a maintenance entry, for example to mark it done by setting completedDate. Only the fields you pass are changed.",
		scope:       scopes.MaintenanceWrite, kind: kindUpdate,
		handler: toolUpdateMaintenanceEntry,
	})

	addTool(s, srv, p, toolDef[DeleteIn, DeleteOut]{
		name: "delete_maintenance_entry", title: "Delete maintenance entry",
		description: "Permanently delete a maintenance entry. Requires confirm=true, which may only be set after the user has explicitly agreed.",
		// Gated by items:delete rather than maintenance:write, so every deleting tool
		// sits behind one permission that collection owners and operators can switch off.
		scope: scopes.ItemsDelete, kind: kindDestructive,
		handler: toolDeleteMaintenanceEntry,
	})
}

func (c *call) maintenanceInfo(itemID uuid.UUID, e repo.MaintenanceEntry) MaintenanceInfo {
	return MaintenanceInfo{
		ID: e.ID.String(), ItemID: itemID.String(), Name: e.Name, Description: c.text(e.Description),
		ScheduledDate: e.ScheduledDate.String(), CompletedDate: e.CompletedDate.String(), Cost: e.Cost,
	}
}

// updateFromOut turns a stored record into the full update payload the
// repository expects, so partial updates can overlay onto it without losing
// anything the caller did not mention.
func updateFromOut(e repo.EntityOut) repo.EntityUpdate {
	u := repo.EntityUpdate{
		ID:                       e.ID,
		Name:                     e.Name,
		Description:              e.Description,
		Quantity:                 e.Quantity,
		AssetID:                  e.AssetID,
		Insured:                  e.Insured,
		Archived:                 e.Archived,
		SyncChildEntityLocations: e.SyncChildEntityLocations,
		SerialNumber:             e.SerialNumber,
		ModelNumber:              e.ModelNumber,
		Manufacturer:             e.Manufacturer,
		LifetimeWarranty:         e.LifetimeWarranty,
		WarrantyExpires:          e.WarrantyExpires,
		WarrantyDetails:          e.WarrantyDetails,
		PurchaseDate:             e.PurchaseDate,
		PurchaseFrom:             e.PurchaseFrom,
		PurchasePrice:            e.PurchasePrice,
		SoldDate:                 e.SoldDate,
		SoldTo:                   e.SoldTo,
		SoldPrice:                e.SoldPrice,
		SoldNotes:                e.SoldNotes,
		Notes:                    e.Notes,
		Fields:                   e.Fields,
	}
	if e.Parent != nil {
		u.ParentID = e.Parent.ID
	}
	if e.LocationID != nil {
		u.LocationID = *e.LocationID
	}
	if e.EntityType != nil {
		u.EntityTypeID = e.EntityType.ID
	}
	u.TagIDs = make([]uuid.UUID, 0, len(e.Tags))
	for _, t := range e.Tags {
		u.TagIDs = append(u.TagIDs, t.ID)
	}
	return u
}

func toolCreateItem(c *call, in CreateItemIn) (ItemDetail, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 255 {
		return ItemDetail{}, invalid("name must be 1-255 characters")
	}
	if len(in.Description) > 1000 {
		return ItemDetail{}, invalid("description must be at most 1000 characters")
	}
	parent, err := parseOptionalID("parentId", in.ParentID)
	if err != nil {
		return ItemDetail{}, err
	}
	tags, err := parseIDs("tagIds", in.TagIDs)
	if err != nil {
		return ItemDetail{}, err
	}
	et, err := parseOptionalID("entityTypeId", in.EntityTypeID)
	if err != nil {
		return ItemDetail{}, err
	}
	qty := in.Quantity
	if qty <= 0 {
		qty = 1
	}
	out, err := c.s.deps.Services.Entities.Create(c.auth, repo.EntityCreate{
		Name: name, ParentID: parent, Description: in.Description, Quantity: qty,
		TagIDs: tags, Manufacturer: in.Manufacturer, ModelNumber: in.ModelNumber, EntityTypeID: et,
	})
	if err != nil {
		return ItemDetail{}, err
	}
	return c.detail(out), nil
}

func toolCreateLocation(c *call, in CreateLocationIn) (ItemDetail, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 255 {
		return ItemDetail{}, invalid("name must be 1-255 characters")
	}
	parent, err := parseOptionalID("parentId", in.ParentID)
	if err != nil {
		return ItemDetail{}, err
	}
	et, err := c.repos().EntityTypes.GetDefault(c, c.gid(), true)
	if err != nil {
		return ItemDetail{}, err
	}
	out, err := c.s.deps.Services.Entities.Create(c.auth, repo.EntityCreate{
		Name: name, ParentID: parent, Description: in.Description, Quantity: 1, EntityTypeID: et.ID,
	})
	if err != nil {
		return ItemDetail{}, err
	}
	return c.detail(out), nil
}

func toolUpdateItem(c *call, in UpdateItemIn) (ItemDetail, error) {
	id, err := parseID("id", in.ID)
	if err != nil {
		return ItemDetail{}, err
	}
	cur, err := c.repos().Entities.GetOneByGroup(c, c.gid(), id)
	if err != nil {
		return ItemDetail{}, err
	}
	upd := updateFromOut(cur)

	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	set(&upd.Name, in.Name)
	set(&upd.Description, in.Description)
	set(&upd.Notes, in.Notes)
	set(&upd.SerialNumber, in.SerialNumber)
	set(&upd.ModelNumber, in.ModelNumber)
	set(&upd.Manufacturer, in.Manufacturer)
	set(&upd.PurchaseFrom, in.PurchaseFrom)
	set(&upd.WarrantyDetails, in.WarrantyDetails)
	if in.Quantity != nil {
		upd.Quantity = *in.Quantity
	}
	if in.PurchasePrice != nil {
		upd.PurchasePrice = *in.PurchasePrice
	}
	if in.Lifetime != nil {
		upd.LifetimeWarranty = *in.Lifetime
	}
	if in.Insured != nil {
		upd.Insured = *in.Insured
	}
	if in.Archived != nil {
		upd.Archived = *in.Archived
	}
	for _, d := range []struct {
		field string
		src   *string
		dst   *types.Date
	}{
		{"purchaseDate", in.PurchaseDate, &upd.PurchaseDate},
		{"warrantyExpires", in.WarrantyExpires, &upd.WarrantyExpires},
	} {
		if d.src == nil {
			continue
		}
		v, err := parseDate(d.field, *d.src)
		if err != nil {
			return ItemDetail{}, err
		}
		*d.dst = v
	}
	if in.TagIDs != nil {
		if upd.TagIDs, err = parseIDs("tagIds", *in.TagIDs); err != nil {
			return ItemDetail{}, err
		}
	}
	if strings.TrimSpace(upd.Name) == "" || len(upd.Name) > 255 {
		return ItemDetail{}, invalid("name must be 1-255 characters")
	}
	if len(upd.Description) > 1000 {
		return ItemDetail{}, invalid("description must be at most 1000 characters")
	}

	out, err := c.repos().Entities.UpdateByGroup(c, c.gid(), upd)
	if err != nil {
		return ItemDetail{}, err
	}
	return c.detail(out), nil
}

func toolMoveItem(c *call, in MoveItemIn) (ItemDetail, error) {
	id, err := parseID("id", in.ID)
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
	if parent == id {
		return ItemDetail{}, invalid("an item cannot be moved into itself")
	}
	if err := c.repos().Entities.Patch(c, c.gid(), id, repo.EntityPatch{ID: id, ParentID: parent, LocationID: loc}); err != nil {
		return ItemDetail{}, err
	}
	out, err := c.repos().Entities.GetOneByGroup(c, c.gid(), id)
	if err != nil {
		return ItemDetail{}, err
	}
	return c.detail(out), nil
}

func toolUpdateItemTags(c *call, in UpdateItemTagsIn) (ItemDetail, error) {
	id, err := parseID("id", in.ID)
	if err != nil {
		return ItemDetail{}, err
	}
	add, err := parseIDs("add", in.Add)
	if err != nil {
		return ItemDetail{}, err
	}
	remove, err := parseIDs("remove", in.Remove)
	if err != nil {
		return ItemDetail{}, err
	}
	cur, err := c.repos().Entities.GetOneByGroup(c, c.gid(), id)
	if err != nil {
		return ItemDetail{}, err
	}
	next := make([]uuid.UUID, 0, len(cur.Tags)+len(add))
	for _, t := range cur.Tags {
		if !slices.Contains(remove, t.ID) {
			next = append(next, t.ID)
		}
	}
	for _, t := range add {
		if !slices.Contains(next, t) && !slices.Contains(remove, t) {
			next = append(next, t)
		}
	}
	if err := c.repos().Entities.Patch(c, c.gid(), id, repo.EntityPatch{ID: id, TagIDs: next}); err != nil {
		return ItemDetail{}, err
	}
	out, err := c.repos().Entities.GetOneByGroup(c, c.gid(), id)
	if err != nil {
		return ItemDetail{}, err
	}
	return c.detail(out), nil
}

func toolDeleteItem(c *call, in DeleteIn) (DeleteOut, error) {
	id, err := parseID("id", in.ID)
	if err != nil {
		return DeleteOut{}, err
	}
	if err := requireConfirm(in.Confirm, "this record"); err != nil {
		return DeleteOut{}, err
	}
	if err := c.repos().Entities.DeleteByGroup(c, c.gid(), id); err != nil {
		return DeleteOut{}, err
	}
	return DeleteOut{Deleted: id.String()}, nil
}

func toolCreateTag(c *call, in CreateTagIn) (TagInfo, error) {
	parent, err := parseOptionalID("parentId", in.ParentID)
	if err != nil {
		return TagInfo{}, err
	}
	if n := strings.TrimSpace(in.Name); n == "" || len(n) > 255 {
		return TagInfo{}, invalid("name must be 1-255 characters")
	}
	out, err := c.repos().Tags.Create(c, c.gid(), repo.TagCreate{
		Name: strings.TrimSpace(in.Name), ParentID: parent, Description: in.Description, Color: in.Color,
	})
	if err != nil {
		return TagInfo{}, err
	}
	return c.tagInfo(out.TagSummary), nil
}

func toolUpdateTag(c *call, in UpdateTagIn) (TagInfo, error) {
	id, err := parseID("id", in.ID)
	if err != nil {
		return TagInfo{}, err
	}
	cur, err := c.repos().Tags.GetOneByGroup(c, c.gid(), id)
	if err != nil {
		return TagInfo{}, err
	}
	upd := repo.TagUpdate{ID: id, ParentID: cur.ParentID, Name: cur.Name, Description: cur.Description, Color: cur.Color, Icon: cur.Icon}
	if in.Name != nil {
		upd.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		upd.Description = *in.Description
	}
	if in.Color != nil {
		upd.Color = *in.Color
	}
	if in.ParentID != nil {
		if upd.ParentID, err = parseOptionalID("parentId", *in.ParentID); err != nil {
			return TagInfo{}, err
		}
	}
	if upd.Name == "" || len(upd.Name) > 255 {
		return TagInfo{}, invalid("name must be 1-255 characters")
	}
	out, err := c.repos().Tags.UpdateByGroup(c, c.gid(), upd)
	if err != nil {
		return TagInfo{}, err
	}
	return c.tagInfo(out.TagSummary), nil
}

func toolDeleteTag(c *call, in DeleteIn) (DeleteOut, error) {
	id, err := parseID("id", in.ID)
	if err != nil {
		return DeleteOut{}, err
	}
	if err := requireConfirm(in.Confirm, "this tag"); err != nil {
		return DeleteOut{}, err
	}
	// The repository's group-scoped delete silently does nothing for a tag
	// in another collection; look it up first so the caller is told "not
	// found" instead of being misled into thinking it was deleted.
	if _, err := c.repos().Tags.GetOneByGroup(c, c.gid(), id); err != nil {
		return DeleteOut{}, err
	}
	if err := c.repos().Tags.DeleteByGroup(c, c.gid(), id); err != nil {
		return DeleteOut{}, err
	}
	return DeleteOut{Deleted: id.String()}, nil
}

func toolAddMaintenanceEntry(c *call, in AddMaintenanceIn) (MaintenanceInfo, error) {
	itemID, err := parseID("itemId", in.ItemID)
	if err != nil {
		return MaintenanceInfo{}, err
	}
	sched, err := parseDate("scheduledDate", in.ScheduledDate)
	if err != nil {
		return MaintenanceInfo{}, err
	}
	done, err := parseDate("completedDate", in.CompletedDate)
	if err != nil {
		return MaintenanceInfo{}, err
	}
	body := repo.MaintenanceEntryCreate{
		Name: strings.TrimSpace(in.Name), Description: in.Description,
		ScheduledDate: sched, CompletedDate: done, Cost: in.Cost,
	}
	if body.Name == "" {
		return MaintenanceInfo{}, invalid("name is required")
	}
	if err := body.Validate(); err != nil {
		return MaintenanceInfo{}, invalid("%s", err.Error())
	}
	out, err := c.repos().MaintEntry.Create(c, c.gid(), itemID, body)
	if err != nil {
		return MaintenanceInfo{}, err
	}
	return c.maintenanceInfo(itemID, out), nil
}

func toolUpdateMaintenanceEntry(c *call, in UpdateMaintenanceIn) (MaintenanceInfo, error) {
	itemID, err := parseID("itemId", in.ItemID)
	if err != nil {
		return MaintenanceInfo{}, err
	}
	id, err := parseID("id", in.ID)
	if err != nil {
		return MaintenanceInfo{}, err
	}
	entries, err := c.repos().MaintEntry.GetMaintenanceByItemID(c, c.gid(), itemID, repo.MaintenanceFilters{Status: repo.MaintenanceFilterStatusBoth})
	if err != nil {
		return MaintenanceInfo{}, err
	}
	idx := slices.IndexFunc(entries, func(e repo.MaintenanceEntryWithDetails) bool { return e.ID == id })
	if idx < 0 {
		return MaintenanceInfo{}, invalid("not found")
	}
	cur := entries[idx]
	upd := repo.MaintenanceEntryUpdate{
		Name: cur.Name, Description: cur.Description,
		ScheduledDate: cur.ScheduledDate, CompletedDate: cur.CompletedDate, Cost: cur.Cost,
	}
	if in.Name != nil {
		upd.Name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		upd.Description = *in.Description
	}
	if in.Cost != nil {
		upd.Cost = *in.Cost
	}
	if in.ScheduledDate != nil {
		if upd.ScheduledDate, err = parseDate("scheduledDate", *in.ScheduledDate); err != nil {
			return MaintenanceInfo{}, err
		}
	}
	if in.CompletedDate != nil {
		if upd.CompletedDate, err = parseDate("completedDate", *in.CompletedDate); err != nil {
			return MaintenanceInfo{}, err
		}
	}
	if upd.Name == "" {
		return MaintenanceInfo{}, invalid("name is required")
	}
	if err := upd.Validate(); err != nil {
		return MaintenanceInfo{}, invalid("%s", err.Error())
	}
	out, err := c.repos().MaintEntry.Update(c, c.gid(), id, upd)
	if err != nil {
		return MaintenanceInfo{}, err
	}
	return c.maintenanceInfo(itemID, out), nil
}

func toolDeleteMaintenanceEntry(c *call, in DeleteIn) (DeleteOut, error) {
	id, err := parseID("id", in.ID)
	if err != nil {
		return DeleteOut{}, err
	}
	if err := requireConfirm(in.Confirm, "this maintenance entry"); err != nil {
		return DeleteOut{}, err
	}
	if err := c.repos().MaintEntry.Delete(c, c.gid(), id); err != nil {
		return DeleteOut{}, err
	}
	return DeleteOut{Deleted: id.String()}, nil
}
