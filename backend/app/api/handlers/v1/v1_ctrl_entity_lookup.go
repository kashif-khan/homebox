package v1

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
	"github.com/sysadminsmedia/homebox/backend/internal/web/adapters"
)

// lookupError gives the lookup service's errors the status a client should see.
func lookupError(err error) error {
	switch {
	case errors.Is(err, services.ErrLookupDisabled):
		return validate.NewRequestError(err, http.StatusNotImplemented)
	case errors.Is(err, services.ErrLookupNotReady):
		return validate.NewRequestError(err, http.StatusConflict)
	case errors.Is(err, services.ErrLookupNotForLocations), errors.Is(err, services.ErrLookupInvalid):
		return validate.NewRequestError(err, http.StatusBadRequest)
	}
	return err
}

// HandleEntityLookupGet godoc
//
//	@Summary	Get an item's product lookup
//	@Tags		Entities
//	@Produce	json
//	@Param		id	path		string	true	"Entity ID"
//	@Success	200	{object}	repo.EntityLookupOut
//	@Router		/v1/entities/{id}/lookup [GET]
//	@Security	Bearer
func (ctrl *V1Controller) HandleEntityLookupGet() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (repo.EntityLookupOut, error) {
		auth := services.NewContext(r.Context())
		return ctrl.repo.EntityLookups.Get(auth, auth.GID, id)
	}
	return adapters.CommandID("id", fn, http.StatusOK)
}

// HandleEntityLookupRequest godoc
//
//	@Summary	Look an item up on the web
//	@Tags		Entities
//	@Produce	json
//	@Param		id	path		string	true	"Entity ID"
//	@Success	202	{object}	repo.EntityLookupOut
//	@Router		/v1/entities/{id}/lookup [POST]
//	@Security	Bearer
func (ctrl *V1Controller) HandleEntityLookupRequest() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (repo.EntityLookupOut, error) {
		out, err := ctrl.svc.Lookups.Request(services.NewContext(r.Context()), id)
		return out, lookupError(err)
	}
	return adapters.CommandID("id", fn, http.StatusAccepted)
}

// HandleEntityLookupApply godoc
//
//	@Summary	Apply chosen details of a lookup match to the item
//	@Tags		Entities
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string					true	"Entity ID"
//	@Param		payload	body		services.LookupApply	true	"Match and details to apply"
//	@Success	200		{object}	repo.EntityOut
//	@Router		/v1/entities/{id}/lookup/apply [POST]
//	@Security	Bearer
func (ctrl *V1Controller) HandleEntityLookupApply() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID, body services.LookupApply) (repo.EntityOut, error) {
		out, err := ctrl.svc.Lookups.Apply(services.NewContext(r.Context()), id, body)
		return out, lookupError(err)
	}
	return adapters.ActionID("id", fn, http.StatusOK)
}

// HandleEntityLookupDismiss godoc
//
//	@Summary	Dismiss an item's product lookup
//	@Tags		Entities
//	@Param		id	path	string	true	"Entity ID"
//	@Success	204
//	@Router		/v1/entities/{id}/lookup [DELETE]
//	@Security	Bearer
func (ctrl *V1Controller) HandleEntityLookupDismiss() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (any, error) {
		auth := services.NewContext(r.Context())
		return nil, ctrl.repo.EntityLookups.Delete(auth, auth.GID, id)
	}
	return adapters.CommandID("id", fn, http.StatusNoContent)
}
