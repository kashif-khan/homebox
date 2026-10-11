package v1

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/hay-kot/httpkit/server"
	"github.com/rs/zerolog/log"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/core/webhooks"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
	"github.com/sysadminsmedia/homebox/backend/internal/web/adapters"
)

var errWebhooksDisabled = errors.New("outbound webhooks are not enabled on this server (set HBOX_WEBHOOKS_ENABLED=true)")

// webhooksOn guards every webhook route: they answer 404 while the feature is off.
func (ctrl *V1Controller) webhooksOn() error {
	if !ctrl.config.Webhooks.Enabled {
		return validate.NewRequestError(errWebhooksDisabled, http.StatusNotFound)
	}
	return nil
}

// HandleWebhookEvents godoc
//
//	@Summary	List Webhook Events
//	@Tags		Webhooks
//	@Produce	json
//	@Success	200	{array}	webhooks.Event
//	@Router		/v1/webhooks/events [GET]
//	@Security	Bearer
func (ctrl *V1Controller) HandleWebhookEvents() errchain.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) error {
		if err := ctrl.webhooksOn(); err != nil {
			return err
		}
		return server.JSON(w, http.StatusOK, webhooks.Catalog)
	}
}

// HandleWebhooksGetAll godoc
//
//	@Summary	List Webhooks
//	@Tags		Webhooks
//	@Produce	json
//	@Success	200	{array}	repo.WebhookEndpointOut
//	@Router		/v1/webhooks [GET]
//	@Security	Bearer
func (ctrl *V1Controller) HandleWebhooksGetAll() errchain.HandlerFunc {
	fn := func(r *http.Request, _ struct{}) ([]repo.WebhookEndpointOut, error) {
		if err := ctrl.webhooksOn(); err != nil {
			return nil, err
		}
		auth := services.NewContext(r.Context())
		return ctrl.repo.Webhooks.GetAll(auth, auth.GID)
	}

	return adapters.Query(fn, http.StatusOK)
}

func (ctrl *V1Controller) checkWebhookURL(raw *string) error {
	if raw == nil {
		return nil
	}
	if err := validate.ValidateWebhookURL(*raw, &ctrl.config.Notifier); err != nil {
		return validate.NewRequestError(err, http.StatusUnprocessableEntity)
	}
	return nil
}

// HandleWebhooksCreate godoc
//
//	@Summary		Create Webhook
//	@Description	The response carries the signing secret. It is shown only this once.
//	@Tags			Webhooks
//	@Produce		json
//	@Param			payload	body		repo.WebhookEndpointIn	true	"Webhook"
//	@Success		201		{object}	repo.WebhookEndpointCreated
//	@Router			/v1/webhooks [POST]
//	@Security		Bearer
func (ctrl *V1Controller) HandleWebhooksCreate() errchain.HandlerFunc {
	fn := func(r *http.Request, in repo.WebhookEndpointIn) (repo.WebhookEndpointCreated, error) {
		if err := ctrl.webhooksOn(); err != nil {
			return repo.WebhookEndpointCreated{}, err
		}
		auth := services.NewContext(r.Context())
		if in.URL == nil || *in.URL == "" {
			return repo.WebhookEndpointCreated{}, validate.NewRequestError(errors.New("url is required"), http.StatusUnprocessableEntity)
		}
		if err := ctrl.checkWebhookURL(in.URL); err != nil {
			return repo.WebhookEndpointCreated{}, err
		}
		if max := ctrl.config.Webhooks.MaxEndpoints; max > 0 {
			n, err := ctrl.repo.Webhooks.Count(auth, auth.GID)
			if err != nil {
				return repo.WebhookEndpointCreated{}, err
			}
			if n >= max {
				return repo.WebhookEndpointCreated{}, validate.NewRequestError(
					errors.New("this collection already has the most webhooks allowed ("+strconv.Itoa(max)+")"), http.StatusUnprocessableEntity)
			}
		}
		return ctrl.repo.Webhooks.Create(auth, auth.GID, in, webhooks.NewSecret())
	}

	return adapters.Action(fn, http.StatusCreated)
}

// HandleWebhooksUpdate godoc
//
//	@Summary	Update Webhook
//	@Tags		Webhooks
//	@Produce	json
//	@Param		id		path		string					true	"Webhook ID"
//	@Param		payload	body		repo.WebhookEndpointIn	true	"Webhook"
//	@Success	200		{object}	repo.WebhookEndpointOut
//	@Router		/v1/webhooks/{id} [PUT]
//	@Security	Bearer
func (ctrl *V1Controller) HandleWebhooksUpdate() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID, in repo.WebhookEndpointIn) (repo.WebhookEndpointOut, error) {
		if err := ctrl.webhooksOn(); err != nil {
			return repo.WebhookEndpointOut{}, err
		}
		auth := services.NewContext(r.Context())
		if err := ctrl.checkWebhookURL(in.URL); err != nil {
			return repo.WebhookEndpointOut{}, err
		}
		return ctrl.repo.Webhooks.Update(auth, auth.GID, id, in)
	}

	return adapters.ActionID("id", fn, http.StatusOK)
}

// HandleWebhooksDelete godoc
//
//	@Summary	Delete Webhook
//	@Tags		Webhooks
//	@Param		id	path	string	true	"Webhook ID"
//	@Success	204
//	@Router		/v1/webhooks/{id} [DELETE]
//	@Security	Bearer
func (ctrl *V1Controller) HandleWebhooksDelete() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (any, error) {
		if err := ctrl.webhooksOn(); err != nil {
			return nil, err
		}
		auth := services.NewContext(r.Context())
		return nil, ctrl.repo.Webhooks.Delete(auth, auth.GID, id)
	}

	return adapters.CommandID("id", fn, http.StatusNoContent)
}

// HandleWebhooksRotateSecret godoc
//
//	@Summary		Rotate Webhook Secret
//	@Description	Issues a new signing secret, shown only in this response. The old one stops working at once.
//	@Tags			Webhooks
//	@Produce		json
//	@Param			id	path		string	true	"Webhook ID"
//	@Success		200	{object}	repo.WebhookEndpointCreated
//	@Router			/v1/webhooks/{id}/rotate-secret [POST]
//	@Security		Bearer
func (ctrl *V1Controller) HandleWebhooksRotateSecret() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (repo.WebhookEndpointCreated, error) {
		if err := ctrl.webhooksOn(); err != nil {
			return repo.WebhookEndpointCreated{}, err
		}
		auth := services.NewContext(r.Context())
		return ctrl.repo.Webhooks.RotateSecret(auth, auth.GID, id, webhooks.NewSecret())
	}

	return adapters.CommandID("id", fn, http.StatusOK)
}

// HandleWebhooksTest godoc
//
//	@Summary	Send Test Event
//	@Tags		Webhooks
//	@Produce	json
//	@Param		id	path		string	true	"Webhook ID"
//	@Success	200	{object}	services.WebhookResult
//	@Router		/v1/webhooks/{id}/test [POST]
//	@Security	Bearer
func (ctrl *V1Controller) HandleWebhooksTest() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (services.WebhookResult, error) {
		if err := ctrl.webhooksOn(); err != nil {
			return services.WebhookResult{}, err
		}
		auth := services.NewContext(r.Context())
		res, err := ctrl.svc.Webhooks.SendTest(auth, auth.GID, id)
		if err != nil {
			log.Err(err).Msg("webhook test failed to run")
		}
		return res, err
	}

	return adapters.CommandID("id", fn, http.StatusOK)
}

// WebhookDeliveriesPage is a page of an endpoint's delivery log.
type WebhookDeliveriesPage struct {
	Items []repo.WebhookDeliveryOut `json:"items"`
	Total int                       `json:"total"`
}

// HandleWebhooksDeliveries godoc
//
//	@Summary	Webhook Delivery Log
//	@Tags		Webhooks
//	@Produce	json
//	@Param		id			path		string	true	"Webhook ID"
//	@Param		page		query		int		false	"page number"
//	@Param		pageSize	query		int		false	"deliveries per page (max 100)"
//	@Success	200			{object}	WebhookDeliveriesPage
//	@Router		/v1/webhooks/{id}/deliveries [GET]
//	@Security	Bearer
func (ctrl *V1Controller) HandleWebhooksDeliveries() errchain.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := ctrl.webhooksOn(); err != nil {
			return err
		}
		id, err := adapters.RouteUUID(r, "id")
		if err != nil {
			return validate.NewRequestError(err, http.StatusBadRequest)
		}
		auth := services.NewContext(r.Context())
		q := r.URL.Query()
		size := min(max(queryIntOr(q.Get("pageSize"), 25), 1), 100)
		page := max(queryIntOr(q.Get("page"), 1), 1)
		items, total, err := ctrl.repo.Webhooks.Deliveries(auth, auth.GID, id, size, (page-1)*size)
		if err != nil {
			return err
		}
		return server.JSON(w, http.StatusOK, WebhookDeliveriesPage{Items: items, Total: total})
	}
}

// HandleWebhooksRedeliver godoc
//
//	@Summary		Redeliver
//	@Description	Queues a fresh copy of a past delivery. It keeps the event id.
//	@Tags			Webhooks
//	@Produce		json
//	@Param			id			path	string	true	"Webhook ID"
//	@Param			deliveryId	path	string	true	"Delivery ID"
//	@Success		202
//	@Router			/v1/webhooks/{id}/deliveries/{deliveryId}/redeliver [POST]
//	@Security		Bearer
func (ctrl *V1Controller) HandleWebhooksRedeliver() errchain.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := ctrl.webhooksOn(); err != nil {
			return err
		}
		id, err := adapters.RouteUUID(r, "id")
		if err != nil {
			return validate.NewRequestError(err, http.StatusBadRequest)
		}
		did, err := adapters.RouteUUID(r, "deliveryId")
		if err != nil {
			return validate.NewRequestError(err, http.StatusBadRequest)
		}
		auth := services.NewContext(r.Context())
		if _, err := ctrl.svc.Webhooks.Redeliver(auth, auth.GID, id, did); err != nil {
			return err
		}
		w.WriteHeader(http.StatusAccepted)
		return nil
	}
}
