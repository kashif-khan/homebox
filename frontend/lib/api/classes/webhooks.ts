import { BaseAPI, route } from "../base";
import type {
  WebhookDeliveriesPage,
  WebhookEndpointCreated,
  WebhookEndpointIn,
  WebhookEndpointOut,
  WebhookResult,
  WebhooksEvent,
} from "../types/data-contracts";

export class WebhooksAPI extends BaseAPI {
  events() {
    return this.http.get<WebhooksEvent[]>({ url: route("/webhooks/events") });
  }

  getAll() {
    return this.http.get<WebhookEndpointOut[]>({ url: route("/webhooks") });
  }

  /** The response carries the signing secret; it is shown only once. */
  create(body: WebhookEndpointIn) {
    return this.http.post<WebhookEndpointIn, WebhookEndpointCreated>({ url: route("/webhooks"), body });
  }

  update(id: string, body: WebhookEndpointIn) {
    return this.http.put<WebhookEndpointIn, WebhookEndpointOut>({ url: route(`/webhooks/${id}`), body });
  }

  delete(id: string) {
    return this.http.delete<void>({ url: route(`/webhooks/${id}`) });
  }

  rotateSecret(id: string) {
    return this.http.post<object, WebhookEndpointCreated>({ url: route(`/webhooks/${id}/rotate-secret`), body: {} });
  }

  test(id: string) {
    return this.http.post<object, WebhookResult>({ url: route(`/webhooks/${id}/test`), body: {} });
  }

  deliveries(id: string, page = 1, pageSize = 25) {
    return this.http.get<WebhookDeliveriesPage>({ url: route(`/webhooks/${id}/deliveries`, { page, pageSize }) });
  }

  redeliver(id: string, deliveryId: string) {
    return this.http.post<object, void>({ url: route(`/webhooks/${id}/deliveries/${deliveryId}/redeliver`), body: {} });
  }
}
