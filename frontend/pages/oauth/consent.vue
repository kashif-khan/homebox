<script setup lang="ts">
  import { useI18n } from "vue-i18n";
  import { toast } from "@/components/ui/sonner";
  import { Button } from "@/components/ui/button";
  import { Badge } from "@/components/ui/badge";
  import { Checkbox } from "@/components/ui/checkbox";
  import { Label } from "@/components/ui/label";
  import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
  import MdiLoading from "~icons/mdi/loading";
  import MdiRobot from "~icons/mdi/robot-outline";
  import MdiAlert from "~icons/mdi/alert-outline";
  import BaseContainer from "@/components/Base/Container.vue";
  import BaseCard from "@/components/Base/Card.vue";
  import type { OauthserverConsentInfo } from "~~/lib/api/types/data-contracts";

  definePageMeta({
    middleware: ["auth"],
    layout: "default",
  });

  const { t } = useI18n();
  useHead({ title: "HomeBox | " + t("oauth.consent.title") });

  const route = useRoute();
  const api = useUserApi();

  const requestId = computed(() => String(route.query.request ?? ""));

  const info = ref<OauthserverConsentInfo | null>(null);
  const loadError = ref<string | null>(null);
  const loading = ref(true);
  const submitting = ref(false);

  const groupId = ref("");
  // Every requested permission starts ticked; the user may untick, never add.
  const granted = reactive<Record<string, boolean>>({});

  const selectedCollection = computed(() => info.value?.collections.find(c => c.id === groupId.value));
  const selectedScopes = computed(() => (info.value?.scopes ?? []).filter(s => granted[s.scope]).map(s => s.scope));
  const asksToChange = computed(() => (info.value?.scopes ?? []).some(s => s.mutating && granted[s.scope]));

  // Permissions the chosen collection's owner has not allowed. They are kept on
  // the grant, but have no effect until the owner raises the access level.
  const blockedScopes = computed(() => {
    const c = selectedCollection.value;
    if (!c) return [];
    return selectedScopes.value.filter(s => !c.allowed.includes(s));
  });

  const usable = computed(() => (info.value?.collections ?? []).filter(c => c.mcpAccess !== "off"));
  const canApprove = computed(
    () => !!selectedCollection.value && selectedCollection.value.mcpAccess !== "off" && selectedScopes.value.length > 0
  );

  async function load() {
    loading.value = true;
    if (!requestId.value) {
      loadError.value = t("oauth.consent.missing_request");
      loading.value = false;
      return;
    }
    const { data, error } = await api.user.getOAuthRequest(requestId.value);
    loading.value = false;
    if (error || !data) {
      loadError.value = t("oauth.consent.expired");
      return;
    }
    info.value = data;
    for (const s of data.scopes) granted[s.scope] = true;
    groupId.value = (usable.value[0] ?? data.collections[0])?.id ?? "";
  }

  onMounted(() => {
    void load();
  });

  // The server returns a URL back to the assistant. A full navigation, not a router
  // push: the target is another site, or a native app's custom scheme.
  function leave(url: string) {
    window.location.assign(url);
  }

  async function approve() {
    if (!canApprove.value) return;
    submitting.value = true;
    const { data, error } = await api.user.approveOAuthRequest(requestId.value, {
      groupId: groupId.value,
      scopes: selectedScopes.value,
    });
    if (error || !data) {
      submitting.value = false;
      toast.error(t("oauth.consent.approve_failed"));
      return;
    }
    leave(data.redirectUrl);
  }

  async function deny() {
    submitting.value = true;
    const { data, error } = await api.user.denyOAuthRequest(requestId.value);
    if (error || !data) {
      submitting.value = false;
      toast.error(t("oauth.consent.deny_failed"));
      return;
    }
    leave(data.redirectUrl);
  }
</script>

<template>
  <BaseContainer class="mx-auto max-w-xl">
    <BaseCard>
      <template #title>
        <div class="flex items-center gap-2 p-4 text-xl font-semibold">
          <MdiRobot />
          {{ $t("oauth.consent.title") }}
        </div>
      </template>

      <div v-if="loading" class="flex items-center gap-2 p-4 text-sm text-muted-foreground">
        <MdiLoading class="animate-spin" /> {{ $t("global.loading") }}
      </div>

      <div v-else-if="loadError" class="p-4 text-sm" role="alert">{{ loadError }}</div>

      <form v-else-if="info" class="space-y-5 p-4" @submit.prevent="approve">
        <p>
          <i18n-t keypath="oauth.consent.intro" tag="span">
            <template #client>
              <strong>{{ info.clientName }}</strong>
            </template>
          </i18n-t>
        </p>
        <p class="text-sm text-muted-foreground">
          {{ $t("oauth.consent.redirect_note", { host: info.redirectHost }) }}
        </p>

        <div>
          <Label for="consent-collection">{{ $t("oauth.consent.collection") }}</Label>
          <Select id="consent-collection" v-model="groupId">
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-for="c in info.collections" :key="c.id" :value="c.id" :disabled="c.mcpAccess === 'off'">
                {{ c.name }}
                <template v-if="c.mcpAccess === 'off'"> ({{ $t("oauth.consent.collection_off") }})</template>
              </SelectItem>
            </SelectContent>
          </Select>
          <p v-if="usable.length === 0" class="mt-2 flex items-start gap-2 text-sm text-destructive" role="alert">
            <MdiAlert class="mt-0.5 shrink-0" /> {{ $t("oauth.consent.none_enabled") }}
          </p>
        </div>

        <fieldset>
          <legend class="mb-2 text-sm font-medium">{{ $t("oauth.consent.permissions") }}</legend>
          <ul class="divide-y rounded-md border">
            <li v-for="s in info.scopes" :key="s.scope" class="flex items-start gap-3 p-3">
              <Checkbox
                :id="`scope-${s.scope}`"
                :model-value="granted[s.scope]"
                @update:model-value="val => (granted[s.scope] = val === true)"
              />
              <label :for="`scope-${s.scope}`" class="flex-1 text-sm">
                <span class="block">{{ s.description || s.scope }}</span>
                <code class="text-xs text-muted-foreground">{{ s.scope }}</code>
              </label>
              <Badge :variant="s.mutating ? 'destructive' : 'secondary'">
                {{ s.mutating ? $t("oauth.consent.can_change") : $t("oauth.consent.read_only") }}
              </Badge>
            </li>
          </ul>
        </fieldset>

        <p v-if="asksToChange" class="flex items-start gap-2 text-sm text-muted-foreground">
          <MdiAlert class="mt-0.5 shrink-0" /> {{ $t("oauth.consent.can_change_warning") }}
        </p>
        <p v-if="blockedScopes.length > 0" class="text-sm text-muted-foreground">
          {{ $t("oauth.consent.blocked_by_owner", { count: blockedScopes.length }) }}
        </p>
        <p class="text-sm text-muted-foreground">{{ $t("oauth.consent.revoke_note") }}</p>

        <div class="flex justify-end gap-2">
          <Button type="button" variant="outline" :disabled="submitting" @click="deny">
            {{ $t("oauth.consent.deny") }}
          </Button>
          <Button type="submit" :disabled="submitting || !canApprove">
            <MdiLoading v-if="submitting" class="animate-spin" />
            {{ $t("oauth.consent.allow") }}
          </Button>
        </div>
      </form>
    </BaseCard>
  </BaseContainer>
</template>
