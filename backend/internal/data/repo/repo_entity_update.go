package repo

import "github.com/google/uuid"

// AsUpdate turns a stored record into the full update payload the repository
// expects, so partial updates can overlay onto it without losing anything the
// caller did not mention.
func (e EntityOut) AsUpdate() EntityUpdate {
	u := EntityUpdate{
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
		ExpiresAt:                e.ExpiresAt,
		ExpiryKind:               e.ExpiryKind,
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
