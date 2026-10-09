package v1

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/hay-kot/httpkit/errchain"
	"github.com/sysadminsmedia/homebox/backend/internal/core/services"
	"github.com/sysadminsmedia/homebox/backend/internal/data/repo"
	"github.com/sysadminsmedia/homebox/backend/internal/oauthserver"
	"github.com/sysadminsmedia/homebox/backend/internal/sys/validate"
	"github.com/sysadminsmedia/homebox/backend/internal/web/adapters"
)

var errOAuthDisabled = validate.NewRequestError(errors.New("OAuth sign-in for AI assistants is not enabled on this server"), http.StatusNotFound)

// OAuthDecision is where to send the user's browser after they decide.
type OAuthDecision struct {
	RedirectURL string `json:"redirectUrl"`
}

// OAuthApprove is the user's consent: the collection to connect and the
// permissions to grant, which may be fewer than the application asked for.
type OAuthApprove struct {
	GroupID uuid.UUID `json:"groupId"`
	Scopes  []string  `json:"scopes"`
}

// HandleOAuthRequestGet godoc
//
//	@Summary	Describe a pending AI assistant authorization request
//	@Tags		OAuth
//	@Produce	json
//	@Param		id	path		string	true	"Request ID"
//	@Success	200	{object}	oauthserver.ConsentInfo
//	@Router		/v1/oauth/requests/{id} [GET]
//	@Security	Bearer
func (ctrl *V1Controller) HandleOAuthRequestGet() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (oauthserver.ConsentInfo, error) {
		if ctrl.oauth == nil {
			return oauthserver.ConsentInfo{}, errOAuthDisabled
		}
		return ctrl.oauth.RequestInfo(r.Context(), services.UseUserCtx(r.Context()), id)
	}
	return adapters.CommandID("id", fn, http.StatusOK)
}

// HandleOAuthRequestApprove godoc
//
//	@Summary	Approve a pending AI assistant authorization request
//	@Tags		OAuth
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string			true	"Request ID"
//	@Param		payload	body		v1.OAuthApprove	true	"Collection and permissions to grant"
//	@Success	200		{object}	v1.OAuthDecision
//	@Router		/v1/oauth/requests/{id}/approve [POST]
//	@Security	Bearer
func (ctrl *V1Controller) HandleOAuthRequestApprove() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID, in OAuthApprove) (OAuthDecision, error) {
		if ctrl.oauth == nil {
			return OAuthDecision{}, errOAuthDisabled
		}
		issuer := SecureBaseURL(r, &ctrl.config.Options)
		u, err := ctrl.oauth.Approve(r.Context(), services.UseUserCtx(r.Context()), id, in.GroupID, in.Scopes, issuer)
		return OAuthDecision{RedirectURL: u}, err
	}
	return adapters.ActionID("id", fn, http.StatusOK)
}

// HandleOAuthRequestDeny godoc
//
//	@Summary	Deny a pending AI assistant authorization request
//	@Tags		OAuth
//	@Produce	json
//	@Param		id	path		string	true	"Request ID"
//	@Success	200	{object}	v1.OAuthDecision
//	@Router		/v1/oauth/requests/{id}/deny [POST]
//	@Security	Bearer
func (ctrl *V1Controller) HandleOAuthRequestDeny() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (OAuthDecision, error) {
		if ctrl.oauth == nil {
			return OAuthDecision{}, errOAuthDisabled
		}
		u, err := ctrl.oauth.Deny(r.Context(), id, SecureBaseURL(r, &ctrl.config.Options))
		return OAuthDecision{RedirectURL: u}, err
	}
	return adapters.CommandID("id", fn, http.StatusOK)
}

// HandleOAuthGrantsList godoc
//
//	@Summary	List connected AI assistants
//	@Tags		OAuth
//	@Produce	json
//	@Success	200	{object}	[]repo.OAuthGrantOut
//	@Router		/v1/users/self/oauth-grants [GET]
//	@Security	Bearer
func (ctrl *V1Controller) HandleOAuthGrantsList() errchain.HandlerFunc {
	fn := func(r *http.Request, _ struct{}) ([]repo.OAuthGrantOut, error) {
		actor := services.UseUserCtx(r.Context())
		out, err := ctrl.repo.OAuth.ListGrantsByUser(r.Context(), actor.ID)
		if out == nil {
			out = []repo.OAuthGrantOut{}
		}
		return out, err
	}
	return adapters.Query(fn, http.StatusOK)
}

// HandleOAuthGrantDelete godoc
//
//	@Summary		Disconnect an AI assistant
//	@Description	Revokes the grant and every token issued under it.
//	@Tags			OAuth
//	@Param			id	path	string	true	"Grant ID"
//	@Success		204
//	@Router			/v1/users/self/oauth-grants/{id} [DELETE]
//	@Security		Bearer
func (ctrl *V1Controller) HandleOAuthGrantDelete() errchain.HandlerFunc {
	fn := func(r *http.Request, id uuid.UUID) (any, error) {
		actor := services.UseUserCtx(r.Context())
		return nil, ctrl.repo.OAuth.DeleteGrantForUser(r.Context(), actor.ID, id)
	}
	return adapters.CommandID("id", fn, http.StatusNoContent)
}
