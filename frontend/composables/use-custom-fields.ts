import { useUserApi } from "~/composables/use-api";
import { useCollections } from "~/composables/use-collections";
import { detectRegionalDefaults } from "~/composables/use-regional-defaults";
import type { CurrenciesCurrency } from "~/lib/api/types/data-contracts";

// The pure helpers live in lib/custom-fields so they can be unit tested without Nuxt.
export * from "../lib/custom-fields";

/**
 * Currency list and the collection's currency and time zone, loaded once, for
 * the editors that create custom fields.
 */
export function useFieldDefaults() {
  const api = useUserApi();
  const { selectedCollection } = useCollections();

  const currencies = ref<CurrenciesCurrency[]>([]);
  const defaultCurrency = ref("");
  const defaultTimezone = ref("");

  onMounted(async () => {
    const { data } = await api.group.currencies();
    if (data) currencies.value = data;

    if (selectedCollection.value) {
      const res = await api.group.get(selectedCollection.value.id);
      if (res.data) {
        defaultCurrency.value = res.data.currency;
        defaultTimezone.value = res.data.timezone || "";
      }
    }
    if (!defaultTimezone.value) defaultTimezone.value = detectRegionalDefaults().timezone;
  });

  return { currencies, defaultCurrency, defaultTimezone };
}
