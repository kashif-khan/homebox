import { BaseAPI, route } from "../base";
import type { EntityLookupOut, EntityOut, LookupApply } from "../types/data-contracts";

/** Product details found on the web for an item, held for the user to review. */
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
}
