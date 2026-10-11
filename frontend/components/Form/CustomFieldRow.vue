<script setup lang="ts">
  import { useI18n } from "vue-i18n";
  import { Input } from "@/components/ui/input";
  import { Label } from "@/components/ui/label";
  import { Checkbox } from "@/components/ui/checkbox";
  import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
  import type { CurrenciesCurrency } from "~~/lib/api/types/data-contracts";
  import {
    FIELD_TYPES,
    isFieldType,
    parseChoices,
    retypeField,
    wallClock,
    type EditableField,
  } from "~/composables/use-custom-fields";
  import { listTimezones } from "~/composables/use-regional-defaults";

  const props = withDefaults(
    defineProps<{
      /** Offered for currency fields. */
      currencies?: CurrenciesCurrency[];
      /** Used when a field is switched to currency / datetime. */
      defaultCurrency?: string;
      defaultTimezone?: string;
      /** Label for the value box; templates call it "Default value". */
      valueLabel?: string;
    }>(),
    { currencies: () => [], defaultCurrency: "", defaultTimezone: "", valueLabel: "" }
  );

  const field = defineModel<EditableField>({ required: true });
  const { t } = useI18n();

  const timezones = listTimezones();
  const uid = useId();

  // Choices are edited as one comma separated string and parsed on change, so
  // typing a comma does not immediately swallow the separator.
  const choicesText = ref((field.value.choices ?? []).join(", "));
  watch(
    () => field.value.choices,
    c => {
      if (parseChoices(choicesText.value).join("\u0000") !== (c ?? []).join("\u0000")) {
        choicesText.value = (c ?? []).join(", ");
      }
    }
  );
  function onChoices(v: string | number) {
    choicesText.value = String(v);
    field.value.choices = parseChoices(choicesText.value);
    if (field.value.textValue && !field.value.choices.includes(field.value.textValue)) {
      field.value.textValue = "";
    }
  }

  function onType(v: unknown) {
    if (!isFieldType(v) || v === field.value.type) return;
    retypeField(field.value, v, { currency: props.defaultCurrency, timezone: props.defaultTimezone });
    choicesText.value = (field.value.choices ?? []).join(", ");
  }

  const label = computed(() => props.valueLabel || t("global.value"));
</script>

<template>
  <div class="grid flex-1 grid-cols-1 gap-2 md:grid-cols-[1fr_9rem_2fr]" data-testid="custom-field-row">
    <div>
      <Label :for="`${uid}-name`">{{ t("global.name") }}</Label>
      <Input :id="`${uid}-name`" v-model="field.name" :maxlength="255" />
    </div>

    <div>
      <Label :for="`${uid}-type`">{{ t("custom_fields.type") }}</Label>
      <Select :id="`${uid}-type`" :model-value="field.type" @update:model-value="onType">
        <SelectTrigger data-testid="field-type">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem v-for="ty in FIELD_TYPES" :key="ty" :value="ty">{{ t(`custom_fields.types.${ty}`) }}</SelectItem>
        </SelectContent>
      </Select>
    </div>

    <div class="flex flex-col gap-2">
      <Label :for="`${uid}-value`">{{ label }}</Label>

      <Input v-if="field.type === 'text'" :id="`${uid}-value`" v-model="field.textValue" :maxlength="500" />

      <Input
        v-else-if="field.type === 'number'"
        :id="`${uid}-value`"
        v-model.number="field.numberValue"
        type="number"
        step="1"
      />

      <div v-else-if="field.type === 'boolean'" class="flex h-9 items-center gap-2">
        <Checkbox :id="`${uid}-value`" v-model="field.booleanValue" class="size-6" />
      </div>

      <Input
        v-else-if="field.type === 'decimal'"
        :id="`${uid}-value`"
        v-model="field.textValue"
        type="number"
        step="any"
        inputmode="decimal"
      />

      <div v-else-if="field.type === 'currency'" class="flex gap-2">
        <Input
          :id="`${uid}-value`"
          v-model="field.textValue"
          type="number"
          step="any"
          inputmode="decimal"
          class="flex-1"
        />
        <Select :model-value="field.currencyCode" @update:model-value="v => (field.currencyCode = String(v || ''))">
          <SelectTrigger class="w-28" :aria-label="t('regional.currency')">
            <SelectValue :placeholder="t('regional.currency')" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem v-for="c in currencies" :key="c.code" :value="c.code">{{ c.code }}</SelectItem>
          </SelectContent>
        </Select>
      </div>

      <Input v-else-if="field.type === 'date'" :id="`${uid}-value`" v-model="field.textValue" type="date" />

      <Input v-else-if="field.type === 'time'" :id="`${uid}-value`" v-model="field.textValue" type="time" />

      <div v-else-if="field.type === 'datetime'" class="flex flex-col gap-2 sm:flex-row">
        <Input
          :id="`${uid}-value`"
          :model-value="wallClock(field.textValue)"
          type="datetime-local"
          class="flex-1"
          @update:model-value="v => (field.textValue = String(v ?? ''))"
        />
        <div class="sm:w-48">
          <Input
            v-model="field.timezone"
            :list="`${uid}-tz`"
            :placeholder="t('regional.timezone')"
            :aria-label="t('regional.timezone')"
            autocomplete="off"
          />
          <datalist :id="`${uid}-tz`">
            <option v-for="z in timezones" :key="z" :value="z" />
          </datalist>
        </div>
      </div>

      <Input
        v-else-if="field.type === 'url'"
        :id="`${uid}-value`"
        v-model="field.textValue"
        type="url"
        placeholder="https://"
      />

      <template v-else-if="field.type === 'select'">
        <Select :model-value="field.textValue" @update:model-value="v => (field.textValue = String(v || ''))">
          <SelectTrigger :id="`${uid}-value`">
            <SelectValue :placeholder="t('custom_fields.pick_choice')" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem v-for="c in field.choices ?? []" :key="c" :value="c">{{ c }}</SelectItem>
          </SelectContent>
        </Select>
        <Input
          :model-value="choicesText"
          :placeholder="t('custom_fields.choices_placeholder')"
          :aria-label="t('custom_fields.choices')"
          @update:model-value="onChoices"
        />
      </template>
    </div>
  </div>
</template>
