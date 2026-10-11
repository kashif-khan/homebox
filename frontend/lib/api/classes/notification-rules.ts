import { BaseAPI, route } from "../base";
import type { NotificationRuleIn, NotificationRuleOut } from "../types/data-contracts";

export class NotificationRulesAPI extends BaseAPI {
  getAll() {
    return this.http.get<NotificationRuleOut[]>({ url: route("/notification-rules") });
  }

  create(body: NotificationRuleIn) {
    return this.http.post<NotificationRuleIn, NotificationRuleOut>({ url: route("/notification-rules"), body });
  }

  update(id: string, body: NotificationRuleIn) {
    return this.http.put<NotificationRuleIn, NotificationRuleOut>({ url: route(`/notification-rules/${id}`), body });
  }

  delete(id: string) {
    return this.http.delete<void>({ url: route(`/notification-rules/${id}`) });
  }
}
