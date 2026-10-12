<script setup lang="ts">
  import { useI18n } from "vue-i18n";
  import { toast } from "@/components/ui/sonner";
  import { Button } from "@/components/ui/button";
  import { Badge } from "@/components/ui/badge";
  import { Checkbox } from "@/components/ui/checkbox";
  import { Label } from "@/components/ui/label";
  import FormTextField from "~/components/Form/TextField.vue";
  import FormCheckbox from "~/components/Form/Checkbox.vue";
  import DateTime from "@/components/global/DateTime.vue";
  import MdiLoading from "~icons/mdi/loading";
  import MdiCheckCircle from "~icons/mdi/check-circle";
  import MdiAlertCircle from "~icons/mdi/alert-circle";
  import type {
    EntityLookupListItem,
    LookupCounts,
    LookupSettingsIn,
    LookupSettingsOut,
    LookupTestOut,
  } from "~~/lib/api/types/data-contracts";

  definePageMeta({
    middleware: ["auth"],
  });

  const { t } = useI18n();
  useHead({ title: `HomeBox | ${t("collection.tabs.lookups")}` });

  const api = useUserApi();

  // ---- connection ----------------------------------------------------------------------------

  const settings = ref<LookupSettingsOut | null>(null);
  const form = reactive({ url: "", token: "", auto: false });
  const saving = ref(false);
  const testing = ref(false);
  const testResult = ref<LookupTestOut | null>(null);

  async function loadSettings() {
    const resp = await api.lookups.settings();
    if (resp.error || !resp.data) return;
    settings.value = resp.data;
    // An address that comes from the server is shown as a hint, not as the form's value.
    form.url = resp.data.fromServer ? "" : resp.data.url;
    form.auto = resp.data.auto;
  }

  function formBody(): LookupSettingsIn {
    return { url: form.url.trim(), token: form.token === "" ? undefined : form.token, auto: form.auto };
  }

  async function save() {
    saving.value = true;
    const resp = await api.lookups.saveSettings(formBody());
    saving.value = false;
    if (resp.error) {
      toast.error(t("lookups.settings.save_failed"));
      return;
    }
    toast.success(t("lookups.settings.saved"));
    settings.value = resp.data;
    form.token = "";
    form.url = resp.data.fromServer ? "" : resp.data.url;
    // The menu action on items depends on this.
    await refreshNuxtData("product-lookup-settings");
    void loadBulkCount();
  }

  async function testConnection() {
    testing.value = true;
    testResult.value = null;
    const resp = await api.lookups.testSettings(formBody());
    testing.value = false;
    testResult.value = resp.error
      ? { ok: false, message: t("lookups.settings.save_failed"), sources: [], webSearch: false }
      : resp.data;
  }

  // ---- activity ------------------------------------------------------------------------------

  const items = ref<EntityLookupListItem[]>([]);
  const counts = ref<LookupCounts>({ pending: 0, done: 0, failed: 0 });
  const busy = ref(false);

  async function loadActivity() {
    const resp = await api.lookups.activity();
    if (resp.error || !resp.data) return;
    items.value = resp.data.items ?? [];
    counts.value = resp.data.counts;
  }

  // Searches happen in the background, so keep the list current while it is open.
  let timer: ReturnType<typeof setInterval> | undefined;
  onMounted(() => {
    void loadSettings();
    void loadActivity();
    void loadBulkCount();
    timer = setInterval(() => {
      if (!document.hidden) void loadActivity();
    }, 4000);
  });
  onBeforeUnmount(() => clearInterval(timer));

  async function retryOne(item: EntityLookupListItem) {
    const resp = await api.lookups.request(item.entityId);
    if (resp.error) toast.error(t("items.lookup.toast.request_failed"));
    await loadActivity();
  }

  async function dismissOne(item: EntityLookupListItem) {
    await api.lookups.dismiss(item.entityId);
    await loadActivity();
  }

  async function retryFailed() {
    busy.value = true;
    const resp = await api.lookups.retryFailed();
    busy.value = false;
    if (!resp.error) toast.success(t("lookups.activity.retried", { count: resp.data.retried }));
    await loadActivity();
  }

  async function clearFinished() {
    busy.value = true;
    const resp = await api.lookups.clear("finished");
    busy.value = false;
    if (!resp.error) toast.success(t("lookups.activity.cleared", { count: resp.data.cleared }));
    await loadActivity();
  }

  // ---- bulk ----------------------------------------------------------------------------------

  const BULK_FIELDS = ["description", "manufacturer", "modelNumber", "photo"] as const;
  const missing = ref<string[]>(["description", "manufacturer", "modelNumber"]);
  const matched = ref<number | null>(null);
  const queueing = ref(false);

  function toggleMissing(field: string, on: boolean) {
    missing.value = on ? [...missing.value, field] : missing.value.filter(f => f !== field);
  }

  async function loadBulkCount() {
    if (missing.value.length === 0) {
      matched.value = 0;
      return;
    }
    const resp = await api.lookups.queue({ missing: missing.value, dryRun: true });
    matched.value = resp.error ? null : resp.data.matched;
  }
  watch(missing, () => void loadBulkCount());

  async function queueAll() {
    queueing.value = true;
    const resp = await api.lookups.queue({ missing: missing.value, dryRun: false });
    queueing.value = false;
    if (resp.error) {
      toast.error(t("lookups.bulk.failed"));
      return;
    }
    toast.success(t("lookups.bulk.queued", { count: resp.data.queued }));
    if (resp.data.matched > resp.data.queued) {
      toast.info(t("lookups.bulk.more", { limit: resp.data.queued }));
    }
    await Promise.all([loadActivity(), loadBulkCount()]);
  }

  const statusVariant = (status: string) =>
    status === "failed" ? "destructive" : status === "done" ? "default" : "secondary";
</script>

<template>
  <div class="space-y-6">
    <p class="text-sm text-muted-foreground">{{ $t("lookups.intro") }}</p>

    <!-- Connection -->
    <section class="space-y-3 rounded-md border p-4">
      <h2 class="text-lg font-medium">{{ $t("lookups.settings.title") }}</h2>
      <form class="flex max-w-xl flex-col gap-3" @submit.prevent="save">
        <div>
          <FormTextField
            v-model="form.url"
            :label="$t('lookups.settings.url')"
            type="url"
            :placeholder="settings?.fromServer ? settings.url : 'http://'"
          />
          <p class="mt-1 text-xs text-muted-foreground">{{ $t("lookups.settings.url_hint") }}</p>
          <p v-if="settings?.fromServer" class="mt-1 text-xs text-muted-foreground">
            {{ $t("lookups.settings.from_server") }}
          </p>
        </div>
        <div>
          <FormTextField
            v-model="form.token"
            :label="$t('lookups.settings.token')"
            type="password"
            autocomplete="off"
            :placeholder="settings?.tokenSet ? $t('lookups.settings.token_saved') : ''"
          />
          <p class="mt-1 text-xs text-muted-foreground">{{ $t("lookups.settings.token_hint") }}</p>
        </div>
        <div>
          <FormCheckbox v-model="form.auto" :label="$t('lookups.settings.auto')" />
          <p class="mt-1 text-xs text-muted-foreground">{{ $t("lookups.settings.auto_hint") }}</p>
        </div>

        <div class="flex flex-wrap gap-2">
          <Button type="submit" :disabled="saving">{{ $t("lookups.settings.save") }}</Button>
          <Button type="button" variant="outline" :disabled="testing" @click="testConnection">
            <MdiLoading v-if="testing" class="mr-2 size-4 animate-spin" />
            {{ testing ? $t("lookups.settings.testing") : $t("lookups.settings.test") }}
          </Button>
        </div>
      </form>

      <div v-if="testResult" class="max-w-xl rounded-md border p-3 text-sm" data-testid="lookup-test-result">
        <p class="flex items-center gap-2 font-medium">
          <MdiCheckCircle v-if="testResult.ok" class="size-5 text-green-600" />
          <MdiAlertCircle v-else class="size-5 text-destructive" />
          {{ testResult.message }}
        </p>
        <ul v-if="testResult.sources?.length" class="mt-2 space-y-1">
          <li v-for="s in testResult.sources" :key="s.name" class="flex items-center gap-2 text-xs">
            <MdiCheckCircle v-if="s.ok" class="size-4 text-green-600" />
            <MdiAlertCircle v-else class="size-4 text-destructive" />
            <code>{{ s.name }}</code>
            <span class="text-muted-foreground">
              {{ s.ok ? s.detail || $t("lookups.settings.source_ok") : s.detail || $t("lookups.settings.source_down") }}
            </span>
          </li>
        </ul>
      </div>
    </section>

    <!-- Activity -->
    <section class="space-y-3 rounded-md border p-4" data-testid="lookup-activity">
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h2 class="text-lg font-medium">{{ $t("lookups.activity.title") }}</h2>
          <p class="text-xs text-muted-foreground">
            {{ $t("lookups.activity.summary", { pending: counts.pending, done: counts.done, failed: counts.failed }) }}
          </p>
        </div>
        <div class="flex flex-wrap gap-2">
          <Button v-if="counts.failed" size="sm" variant="outline" :disabled="busy" @click="retryFailed">
            {{ $t("lookups.activity.retry_failed") }}
          </Button>
          <Button v-if="counts.done || counts.failed" size="sm" variant="ghost" :disabled="busy" @click="clearFinished">
            {{ $t("lookups.activity.clear_finished") }}
          </Button>
        </div>
      </div>

      <p v-if="items.length === 0" class="py-4 text-center text-sm text-muted-foreground">
        {{ $t("lookups.activity.empty") }}
      </p>
      <div v-else class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead class="text-left text-xs text-muted-foreground">
            <tr>
              <th class="p-2">{{ $t("lookups.activity.item") }}</th>
              <th class="p-2">{{ $t("lookups.activity.status") }}</th>
              <th class="hidden p-2 md:table-cell">{{ $t("lookups.activity.searched_for") }}</th>
              <th class="hidden p-2 md:table-cell">{{ $t("lookups.activity.when") }}</th>
              <th class="p-2"></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in items" :key="item.entityId" class="border-t align-top">
              <td class="p-2 font-medium">
                <NuxtLink :to="`/item/${item.entityId}`" class="hover:underline">{{ item.entityName }}</NuxtLink>
              </td>
              <td class="p-2">
                <Badge :variant="statusVariant(item.status)" class="gap-1 whitespace-nowrap">
                  <MdiLoading v-if="item.status === 'pending'" class="size-3 animate-spin" />
                  {{ $t(`lookups.activity.${item.status}`) }}
                </Badge>
                <p v-if="item.status === 'done'" class="mt-1 text-xs text-muted-foreground">
                  {{
                    item.matches === 0
                      ? $t("lookups.activity.no_matches")
                      : item.matches === 1
                        ? $t("lookups.activity.matches_one")
                        : $t("lookups.activity.matches", { count: item.matches })
                  }}
                </p>
                <p v-if="item.status === 'failed'" class="mt-1 max-w-xs text-xs text-muted-foreground">
                  {{ item.error }}
                </p>
              </td>
              <td class="hidden p-2 text-muted-foreground md:table-cell">{{ item.query }}</td>
              <td class="hidden p-2 text-muted-foreground md:table-cell"><DateTime :date="item.updatedAt" /></td>
              <td class="p-2 text-right">
                <div class="flex justify-end gap-1">
                  <Button v-if="item.status === 'done' && item.matches" size="sm" as-child>
                    <NuxtLink :to="`/item/${item.entityId}`">{{ $t("lookups.activity.review") }}</NuxtLink>
                  </Button>
                  <Button v-if="item.status === 'failed'" size="sm" variant="outline" @click="retryOne(item)">
                    {{ $t("lookups.activity.retry") }}
                  </Button>
                  <Button v-if="item.status !== 'pending'" size="sm" variant="ghost" @click="dismissOne(item)">
                    {{ $t("lookups.activity.dismiss") }}
                  </Button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <!-- Fill in the gaps -->
    <section class="space-y-3 rounded-md border p-4" data-testid="lookup-bulk">
      <h2 class="text-lg font-medium">{{ $t("lookups.bulk.title") }}</h2>
      <p class="text-sm text-muted-foreground">{{ $t("lookups.bulk.description") }}</p>

      <div>
        <p class="mb-1 text-sm font-medium">{{ $t("lookups.bulk.missing") }}</p>
        <div class="flex flex-wrap gap-x-5 gap-y-2">
          <div v-for="field in BULK_FIELDS" :key="field" class="flex items-center gap-2">
            <Checkbox
              :id="`bulk-${field}`"
              :model-value="missing.includes(field)"
              @update:model-value="toggleMissing(field, $event === true)"
            />
            <Label :for="`bulk-${field}`" class="cursor-pointer font-normal">
              {{ field === "photo" ? $t("lookups.bulk.photo") : $t(`items.lookup.field_${field}`) }}
            </Label>
          </div>
        </div>
      </div>

      <div class="flex flex-wrap items-center gap-3">
        <Button :disabled="queueing || !settings?.configured || !matched" @click="queueAll">
          <MdiLoading v-if="queueing" class="mr-2 size-4 animate-spin" />
          {{ matched === 1 ? $t("lookups.bulk.queue_one") : $t("lookups.bulk.queue", { count: matched ?? 0 }) }}
        </Button>
        <span class="text-sm text-muted-foreground" data-testid="lookup-bulk-count">
          <template v-if="!settings?.configured">{{ $t("lookups.bulk.set_up_first") }}</template>
          <template v-else-if="matched === null" />
          <template v-else-if="matched === 0">{{ $t("lookups.bulk.none") }}</template>
          <template v-else-if="matched === 1">{{ $t("lookups.bulk.count_one") }}</template>
          <template v-else>{{ $t("lookups.bulk.count", { count: matched }) }}</template>
        </span>
      </div>
    </section>
  </div>
</template>
