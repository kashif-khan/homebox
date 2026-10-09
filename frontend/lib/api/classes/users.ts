import { BaseAPI, route } from "../base";
import type {
  APIKeyCreate,
  APIKeyCreatedOut,
  APIKeyOut,
  ChangePassword,
  OAuthApprove,
  OAuthDecision,
  OAuthGrantOut,
  OauthserverConsentInfo,
  UserOut,
} from "../types/data-contracts";
import type { Result } from "../types/non-generated";

export class UserApi extends BaseAPI {
  public self() {
    return this.http.get<Result<UserOut>>({ url: route("/users/self") });
  }

  public logout() {
    return this.http.post<object, void>({ url: route("/users/logout") });
  }

  /** Revokes every session token for the current user across all devices,
   * including this one. Use to invalidate a session that may have been leaked. */
  public logoutAll() {
    return this.http.post<object, void>({ url: route("/users/logout/all") });
  }

  public delete() {
    return this.http.delete<void>({ url: route("/users/self") });
  }

  public changePassword(current: string, newPassword: string) {
    return this.http.put<ChangePassword, void>({
      url: route("/users/self/change-password"),
      body: {
        current,
        new: newPassword,
      },
    });
  }

  public getSettings() {
    return this.http.get<Result<Record<string, unknown>>>({
      url: route("/users/self/settings"),
    });
  }

  public setSettings(settings: Record<string, unknown>) {
    return this.http.put<Record<string, unknown>, Result<Record<string, unknown>>>({
      url: route("/users/self/settings"),
      body: settings,
    });
  }

  public listApiKeys() {
    return this.http.get<APIKeyOut[]>({ url: route("/users/self/api-keys") });
  }

  public createApiKey(body: APIKeyCreate) {
    return this.http.post<APIKeyCreate, APIKeyCreatedOut>({
      url: route("/users/self/api-keys"),
      body,
    });
  }

  public deleteApiKey(id: string) {
    return this.http.delete<void>({ url: route(`/users/self/api-keys/${id}`) });
  }

  /** AI assistants the user has connected through the OAuth sign-in flow. */
  public listOAuthGrants() {
    return this.http.get<OAuthGrantOut[]>({ url: route("/users/self/oauth-grants") });
  }

  /** Disconnects an assistant: revokes the grant and every token issued under it. */
  public deleteOAuthGrant(id: string) {
    return this.http.delete<void>({ url: route(`/users/self/oauth-grants/${id}`) });
  }

  /** Describes a pending assistant authorization request for the consent screen. */
  public getOAuthRequest(id: string) {
    return this.http.get<OauthserverConsentInfo>({ url: route(`/oauth/requests/${id}`) });
  }

  public approveOAuthRequest(id: string, body: OAuthApprove) {
    return this.http.post<OAuthApprove, OAuthDecision>({ url: route(`/oauth/requests/${id}/approve`), body });
  }

  public denyOAuthRequest(id: string) {
    return this.http.post<object, OAuthDecision>({ url: route(`/oauth/requests/${id}/deny`), body: {} });
  }
}
