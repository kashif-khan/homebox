<script setup lang="ts">
  import { useI18n } from "vue-i18n";
  import { Label } from "@/components/ui/label";
  import { Input } from "@/components/ui/input";
  import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
  import type { CurrenciesCurrency } from "~~/lib/api/types/data-contracts";
  import { currencyForCountry, listCountries, listTimezones } from "~/composables/use-regional-defaults";

  const props = withDefaults(
    defineProps<{
      /** Offered currencies; leave empty to hide the currency picker. */
      currencies?: CurrenciesCurrency[];
      /** Changing the country re-suggests a currency. Off where currency is not edited. */
      suggestCurrency?: boolean;
      /** Hide the country picker, for places that only edit the time zone. */
      showCountry?: boolean;
    }>(),
    { currencies: () => [], suggestCurrency: true, showCountry: true }
  );

  const country = defineModel<string>("country", { default: "" });
  const currency = defineModel<string>("currency", { default: "" });
  const timezone = defineModel<string>("timezone", { default: "" });

  const { t, locale } = useI18n();

  const countries = computed(() => listCountries(locale.value));
  const timezones = listTimezones();

  function onCountry(val: unknown) {
    country.value = String(val || "");
    const suggested = currencyForCountry(country.value);
    if (props.suggestCurrency && suggested && props.currencies.some(c => c.code === suggested)) {
      currency.value = suggested;
    }
  }
</script>

<template>
  <div class="flex flex-col gap-3" data-testid="regional-fields">
    <div v-if="showCountry">
      <Label for="regional-country">{{ t("regional.country") }}</Label>
      <Select id="regional-country" :model-value="country" @update:model-value="onCountry">
        <SelectTrigger>
          <SelectValue :placeholder="t('regional.not_set')" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem v-for="c in countries" :key="c.code" :value="c.code">{{ c.name }}</SelectItem>
        </SelectContent>
      </Select>
    </div>

    <div v-if="currencies.length">
      <Label for="regional-currency">{{ t("regional.currency") }}</Label>
      <Select id="regional-currency" :model-value="currency" @update:model-value="v => (currency = String(v || ''))">
        <SelectTrigger>
          <SelectValue :placeholder="t('regional.not_set')" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem v-for="c in currencies" :key="c.code" :value="c.code">{{ c.name }} ({{ c.code }})</SelectItem>
        </SelectContent>
      </Select>
    </div>

    <div>
      <Label for="regional-timezone">{{ t("regional.timezone") }}</Label>
      <Input id="regional-timezone" v-model="timezone" list="regional-timezone-list" autocomplete="off" />
      <datalist id="regional-timezone-list">
        <option v-for="z in timezones" :key="z" :value="z" />
      </datalist>
      <p class="mt-1 text-xs text-muted-foreground">{{ t("regional.timezone_hint") }}</p>
    </div>
  </div>
</template>
