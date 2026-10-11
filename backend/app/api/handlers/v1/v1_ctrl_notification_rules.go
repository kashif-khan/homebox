package v1

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/web/adapters"
)

// HandleNotificationRulesGetAll godoc
//
//	@Summary	List Alert Rules
//	@Tags		Notification Rules
//	@Produce	json
//	@Success	200	{array}	repo.NotificationRuleOut
//	@Router		/v1/notification-rules [GET]
//	@Security	Bearer
func (ctrl *V1Controller) HandleNotificationRulesGetAll() errchain.HandlerFunc {
	fn := func(r *http.Request, _ struct{}) ([]repo.NotificationRuleOut, error) {
		auth := services.NewContext(r.Context())
		return ctrl.repo.NotificationRules.GetAll(auth, auth.GID)
	}

	return adapters.Query(fn, http.StatusOK)
}

// HandleNotificationRulesCreate godoc
//
//	@Summary	Create Alert Rule
//	@Tags		Notification Rules
//	@Produce	json
//	@Param		payload	body		repo.NotificationRuleIn	true	"Rule"
//	@Success	201		{object}	repo.NotificationRuleOut
//	@Router		/v1/notification-rules [POST]
//	@Security	Bearer
func (ctrl *V1Controller) HandleNotificationRulesCreate() errchain.HandlerFunc {
	fn := func(r *http.Request, in repo.NotificationRuleIn) (repo.NotificationRuleOut, error) {
		auth := services.NewContext(r.Context())
		return ctrl.repo.NotificationRules.Create(auth, auth.GID, in)
	}

	return adapters.Action(fn, http.StatusCreated)
}

// HandleNotificationRulesUpdate godoc
//
//	@Summary	Update Alert Rule
//	@Tags		Notification Rules
//	@Produce	json
//	@Param		id		path		string					true	"Rule ID"
//	@Param		payload	body		repo.NotificationRuleIn	true	"Rule"
//	@Success	200		{object}	repo.NotificationRuleOut
//	@Router		/v1/notification-rules/{id} [PUT]
//	@Security	Bearer
func (ctrl *V1Controller) HandleNotificationRulesUpdate() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID, in repo.NotificationRuleIn) (repo.NotificationRuleOut, error) {
		auth := services.NewContext(r.Context())
		return ctrl.repo.NotificationRules.Update(auth, auth.GID, id, in)
	}

	return adapters.ActionID("id", fn, http.StatusOK)
}

// HandleNotificationRulesDelete godoc
//
//	@Summary	Delete Alert Rule
//	@Tags		Notification Rules
//	@Param		id	path	string	true	"Rule ID"
//	@Success	204
//	@Router		/v1/notification-rules/{id} [DELETE]
//	@Security	Bearer
func (ctrl *V1Controller) HandleNotificationRulesDelete() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (any, error) {
		auth := services.NewContext(r.Context())
		return nil, ctrl.repo.NotificationRules.Delete(auth, auth.GID, id)
	}

	return adapters.CommandID("id", fn, http.StatusNoContent)
}
