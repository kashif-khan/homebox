<script setup lang="ts">
  import { useI18n } from "vue-i18n";
  import BaseCard from "@/components/Base/Card.vue";
  import Subtitle from "~/components/global/Subtitle.vue";
  import ExpiryBadge from "~/components/Item/ExpiryBadge.vue";
  import type { ExpiryEntry } from "~/lib/api/types/data-contracts";

  const { t } = useI18n();
  const api = useUserApi();

  const { data, refresh } = useAsyncData(
    "expiring",
    async () => {
      const { data } = await api.items.expiring(30, 10);
      return data;
    },
    { deep: true }
  );

  onServerEvent(ServerEvent.EntityMutation, () => {
    refresh();
  });

  const lists = computed(() => [
    {
      key: "expired",
      title: t("home.expired"),
      items: data.value?.expired ?? [],
      total: data.value?.expiredTotal ?? 0,
    },
    { key: "soon", title: t("home.expiring_soon"), items: data.value?.soon ?? [], total: data.value?.soonTotal ?? 0 },
  ]);
  const anything = computed(() => lists.value.some(l => l.items.length > 0));

  function href(e: ExpiryEntry) {
    return `/item/${e.id}`;
  }
</script>

<template>
  <section v-if="anything" data-testid="expiring-card">
    <Subtitle> {{ $t("home.expiry") }} </Subtitle>
    <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
      <template v-for="list in lists" :key="list.key">
        <BaseCard v-if="list.items.length > 0">
          <template #title>
            <h3 class="p-4 pb-0 text-base font-medium">
              {{ list.title }}
              <span class="text-sm font-normal text-muted-foreground">({{ list.total }})</span>
            </h3>
          </template>
          <ul class="divide-y p-2">
            <li v-for="e in list.items" :key="e.id" class="flex items-center justify-between gap-2 p-2">
              <NuxtLink :to="href(e)" class="min-w-0 truncate hover:underline">{{ e.name }}</NuxtLink>
              <ExpiryBadge :expires-at="e.expiresAt as string" :kind="e.expiryKind" />
            </li>
          </ul>
          <p v-if="list.total > list.items.length" class="px-4 pb-3 text-xs text-muted-foreground">
            {{ $t("home.and_more", { n: list.total - list.items.length }) }}
          </p>
        </BaseCard>
      </template>
    </div>
  </section>
</template>
