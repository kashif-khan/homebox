<script setup lang="ts">
  import { useI18n } from "vue-i18n";
  import { Badge } from "@/components/ui/badge";
  import { expiryLabel, expiryStatus, hasExpiry, todayString } from "~/lib/expiry";

  const props = defineProps<{
    expiresAt: string | null | undefined;
    kind?: string;
  }>();

  const { t } = useI18n();

  const today = todayString();
  const status = computed(() => expiryStatus(props.expiresAt, today));
  const label = computed(() => expiryLabel(props.expiresAt, today, t));
  const kindLabel = computed(() =>
    props.kind && props.kind !== "expires" ? t(`items.expiry_kinds.${props.kind}`) : ""
  );

  const variant = computed(() =>
    status.value === "expired" ? "destructive" : status.value === "soon" ? "default" : "secondary"
  );
</script>

<template>
  <Badge v-if="hasExpiry(expiresAt)" :variant="variant" class="whitespace-nowrap" data-testid="expiry-badge">
    <span v-if="kindLabel" class="mr-1 opacity-80">{{ kindLabel }}:</span>{{ label }}
  </Badge>
</template>
