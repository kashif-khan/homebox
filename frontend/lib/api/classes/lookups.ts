import { BaseAPI, route } from "../base";
import type {
  EntityLookupOut,
  EntityOut,
  LookupActivityOut,
  LookupApply,
  LookupQueueOut,
  LookupQueueRequest,
  LookupSettingsIn,
  LookupSettingsOut,
  LookupTestOut,
} from "../types/data-contracts";

/** Product details found on the web for items, held for the user to review. */
export class LookupsAPI extends BaseAPI {
  /** 404 when the item has no lookup. */
  get(itemId: string) {
    return this.http.get<EntityLookupOut>({ url: route(`/entities/${itemId}/lookup`) });
  }

  /** Starts a lookup, replacing an earlier one. The result arrives in the background. */
  request(itemId: string) {
    return this.http.post<object, EntityLookupOut>({ url: route(`/entities/${itemId}/lookup`), body: {} });
  }

  apply(itemId: string, body: LookupApply) {
    return this.http.post<LookupApply, EntityOut>({ url: route(`/entities/${itemId}/lookup/apply`), body });
  }

  dismiss(itemId: string) {
    return this.http.delete<void>({ url: route(`/entities/${itemId}/lookup`) });
  }

  /** A picture of a match, fetched through Homebox so the browser never contacts the site it came from. */
  imageURL(itemId: string, candidate: number, index: number) {
    return this.authURL(`/entities/${itemId}/lookup/image?candidate=${candidate}&index=${index}`);
  }

  settings() {
    return this.http.get<LookupSettingsOut>({ url: route("/lookups/settings") });
  }

  saveSettings(body: LookupSettingsIn) {
    return this.http.put<LookupSettingsIn, LookupSettingsOut>({ url: route("/lookups/settings"), body });
  }

  testSettings(body: LookupSettingsIn) {
    return this.http.post<LookupSettingsIn, LookupTestOut>({ url: route("/lookups/settings/test"), body });
  }

  activity(status = "") {
    return this.http.get<LookupActivityOut>({ url: route("/lookups", status ? { status } : {}) });
  }

  /** With dryRun, only counts the items that would be looked up. */
  queue(body: LookupQueueRequest) {
    return this.http.post<LookupQueueRequest, LookupQueueOut>({ url: route("/lookups/queue"), body });
  }

  retryFailed() {
    return this.http.post<object, { retried: number }>({ url: route("/lookups/retry-failed"), body: {} });
  }

  /** which: done, failed or finished. Ones still searching are left alone. */
  clear(which: "done" | "failed" | "finished") {
    return this.http.delete<{ cleared: number }>({ url: route("/lookups", { which }) });
  }
}
