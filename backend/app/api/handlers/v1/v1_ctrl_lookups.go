package v1

import (
	"net/http"
	"strconv"

	"github.com/hay-kot/httpkit/errchain"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
	"github.com/sysadminsmedia/homebox/backend/internal/web/adapters"
)

// LookupQueueRequest asks for lookups of the items that are missing details.
type LookupQueueRequest struct {
	// Missing lists the details to look for gaps in: description, manufacturer,
	// modelNumber, photo. An item missing any of them is queued.
	Missing []string `json:"missing"`
	// DryRun only counts the items, so the UI can say how many there would be.
	DryRun bool `json:"dryRun"`
}

// LookupRetryResult says how many lookups were put back in the queue.
type LookupRetryResult struct {
	Retried int `json:"retried"`
}

// LookupClearResult says how many lookups were dismissed.
type LookupClearResult struct {
	Cleared int `json:"cleared"`
}

// HandleLookupSettingsGet godoc
//
//	@Summary	Get the collection's product lookup settings
//	@Tags		Lookups
//	@Produce	json
//	@Success	200	{object}	services.LookupSettingsOut
//	@Router		/v1/lookups/settings [GET]
//	@Security	Bearer
func (ctrl *V1Controller) HandleLookupSettingsGet() errchain.HandlerFunc {
	fn := func(r *http.Request) (services.LookupSettingsOut, error) {
		return ctrl.svc.Lookups.Settings(services.NewContext(r.Context()))
	}
	return adapters.Command(fn, http.StatusOK)
}

// HandleLookupSettingsSet godoc
//
//	@Summary	Set the collection's product lookup settings
//	@Tags		Lookups
//	@Accept		json
//	@Produce	json
//	@Param		payload	body		services.LookupSettingsIn	true	"Settings"
//	@Success	200		{object}	services.LookupSettingsOut
//	@Router		/v1/lookups/settings [PUT]
//	@Security	Bearer
func (ctrl *V1Controller) HandleLookupSettingsSet() errchain.HandlerFunc {
	fn := func(r *http.Request, body services.LookupSettingsIn) (services.LookupSettingsOut, error) {
		out, err := ctrl.svc.Lookups.SaveSettings(services.NewContext(r.Context()), body)
		return out, lookupError(err)
	}
	return adapters.Action(fn, http.StatusOK)
}

// HandleLookupSettingsTest godoc
//
//	@Summary	Test a connection to a product lookup service
//	@Tags		Lookups
//	@Accept		json
//	@Produce	json
//	@Param		payload	body		services.LookupSettingsIn	true	"Settings to try"
//	@Success	200		{object}	services.LookupTestOut
//	@Router		/v1/lookups/settings/test [POST]
//	@Security	Bearer
func (ctrl *V1Controller) HandleLookupSettingsTest() errchain.HandlerFunc {
	fn := func(r *http.Request, body services.LookupSettingsIn) (services.LookupTestOut, error) {
		return ctrl.svc.Lookups.TestConnection(services.NewContext(r.Context()), body), nil
	}
	return adapters.Action(fn, http.StatusOK)
}

// HandleLookupActivity godoc
//
//	@Summary	List the collection's product lookups
//	@Tags		Lookups
//	@Produce	json
//	@Param		status	query		string	false	"pending, done or failed"
//	@Success	200		{object}	services.LookupActivityOut
//	@Router		/v1/lookups [GET]
//	@Security	Bearer
func (ctrl *V1Controller) HandleLookupActivity() errchain.HandlerFunc {
	type query struct {
		Status string `schema:"status"`
	}
	fn := func(r *http.Request, q query) (services.LookupActivityOut, error) {
		out, err := ctrl.svc.Lookups.Activity(services.NewContext(r.Context()), q.Status)
		return out, lookupError(err)
	}
	return adapters.Query(fn, http.StatusOK)
}

// HandleLookupQueue godoc
//
//	@Summary	Look up every item that is missing details
//	@Tags		Lookups
//	@Accept		json
//	@Produce	json
//	@Param		payload	body		LookupQueueRequest	true	"Which gaps to fill"
//	@Success	200		{object}	services.LookupQueueOut
//	@Router		/v1/lookups/queue [POST]
//	@Security	Bearer
func (ctrl *V1Controller) HandleLookupQueue() errchain.HandlerFunc {
	fn := func(r *http.Request, body LookupQueueRequest) (services.LookupQueueOut, error) {
		out, err := ctrl.svc.Lookups.QueueMissing(services.NewContext(r.Context()), body.Missing, body.DryRun)
		return out, lookupError(err)
	}
	return adapters.Action(fn, http.StatusOK)
}

// HandleLookupRetryFailed godoc
//
//	@Summary	Retry the failed product lookups
//	@Tags		Lookups
//	@Produce	json
//	@Success	200	{object}	LookupRetryResult
//	@Router		/v1/lookups/retry-failed [POST]
//	@Security	Bearer
func (ctrl *V1Controller) HandleLookupRetryFailed() errchain.HandlerFunc {
	fn := func(r *http.Request) (LookupRetryResult, error) {
		n, err := ctrl.svc.Lookups.RetryFailed(services.NewContext(r.Context()))
		return LookupRetryResult{Retried: n}, lookupError(err)
	}
	return adapters.Command(fn, http.StatusOK)
}

// HandleLookupClear godoc
//
//	@Summary	Dismiss finished product lookups
//	@Tags		Lookups
//	@Produce	json
//	@Param		which	query		string	true	"done, failed or finished"
//	@Success	200		{object}	LookupClearResult
//	@Router		/v1/lookups [DELETE]
//	@Security	Bearer
func (ctrl *V1Controller) HandleLookupClear() errchain.HandlerFunc {
	type query struct {
		Which string `schema:"which"`
	}
	fn := func(r *http.Request, q query) (LookupClearResult, error) {
		n, err := ctrl.svc.Lookups.Clear(services.NewContext(r.Context()), q.Which)
		return LookupClearResult{Cleared: n}, lookupError(err)
	}
	return adapters.Query(fn, http.StatusOK)
}

// HandleEntityLookupImage godoc
//
//	@Summary	A picture from a lookup match, fetched through Homebox
//	@Tags		Entities
//	@Produce	image/*
//	@Param		id			path	string	true	"Entity ID"
//	@Param		candidate	query	int		true	"Match number"
//	@Param		index		query	int		false	"Picture number within the match"
//	@Success	200
//	@Router		/v1/entities/{id}/lookup/image [GET]
//	@Security	Bearer
func (ctrl *V1Controller) HandleEntityLookupImage() errchain.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := adapters.RouteUUID(r, "id")
		if err != nil {
			return err
		}
		candidate, err := strconv.Atoi(r.URL.Query().Get("candidate"))
		if err != nil {
			return validate.NewRequestError(err, http.StatusBadRequest)
		}
		index := 0
		if v := r.URL.Query().Get("index"); v != "" {
			if index, err = strconv.Atoi(v); err != nil {
				return validate.NewRequestError(err, http.StatusBadRequest)
			}
		}

		data, mime, err := ctrl.svc.Lookups.CandidateImage(services.NewContext(r.Context()), id, candidate, index)
		if err != nil {
			return lookupError(err)
		}
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, err = w.Write(data)
		return err
	}
}
