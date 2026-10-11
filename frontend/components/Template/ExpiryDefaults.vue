<script setup lang="ts">
  import { useI18n } from "vue-i18n";
  import { Checkbox } from "@/components/ui/checkbox";
  import { Input } from "@/components/ui/input";
  import { Label } from "@/components/ui/label";
  import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

  const include = defineModel<boolean>("include", { default: false });
  /** Days after an item is added that it expires; null leaves the date empty. */
  const offsetDays = defineModel<number | null>("offsetDays", { default: null });
  const kind = defineModel<string>("kind", { default: "expires" });

  const { t } = useI18n();
  const uid = useId();
  const kinds = ["expires", "use_by", "best_before"] as const;

  // The number box yields "" when cleared; the API wants null.
  function onOffset(v: string | number) {
    const n = typeof v === "number" ? v : v === "" ? NaN : Number(v);
    offsetDays.value = Number.isFinite(n) ? Math.max(0, Math.round(n)) : null;
  }
</script>

<template>
  <div class="flex flex-col gap-2" data-testid="template-expiry">
    <div class="flex items-center gap-2">
      <Checkbox :id="`${uid}-inc`" v-model="include" class="size-5" />
      <Label :for="`${uid}-inc`" class="cursor-pointer">{{ t("components.template.form.expiry_include") }}</Label>
    </div>
    <div v-if="include" class="grid grid-cols-1 gap-2 sm:grid-cols-2">
      <div>
        <Label :for="`${uid}-days`">{{ t("components.template.form.expiry_offset_days") }}</Label>
        <Input
          :id="`${uid}-days`"
          :model-value="offsetDays ?? ''"
          type="number"
          min="0"
          max="36500"
          step="1"
          @update:model-value="onOffset"
        />
        <p class="mt-1 text-xs text-muted-foreground">{{ t("components.template.form.expiry_offset_hint") }}</p>
      </div>
      <div>
        <Label :for="`${uid}-kind`">{{ t("items.expiry_kind") }}</Label>
        <Select :id="`${uid}-kind`" v-model="kind">
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem v-for="k in kinds" :key="k" :value="k">{{ t(`items.expiry_kinds.${k}`) }}</SelectItem>
          </SelectContent>
        </Select>
      </div>
    </div>
  </div>
</template>
