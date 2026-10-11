<script setup lang="ts">
  import { useI18n } from "vue-i18n";
  import { toast } from "@/components/ui/sonner";
  import { Button } from "@/components/ui/button";
  import { Badge } from "@/components/ui/badge";
  import { Label } from "@/components/ui/label";
  import { Checkbox } from "@/components/ui/checkbox";
  import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
  import { useDialog } from "@/components/ui/dialog-provider";
  import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
  import FormTextField from "~/components/Form/TextField.vue";
  import FormCheckbox from "~/components/Form/Checkbox.vue";
  import DateTime from "@/components/global/DateTime.vue";
  import MdiWebhook from "~icons/mdi/webhook";
  import MdiDelete from "~icons/mdi/delete";
  import MdiPencil from "~icons/mdi/pencil";
  import MdiContentCopy from "~icons/mdi/content-copy";
  import { DialogID } from "~/components/ui/dialog-provider/utils";
  import { useEntityTypeStore } from "~/stores/entityTypes";
  import { useTagStore } from "~/stores/tags";
  import { deliverySummary, groupEvents, humanDuration, lateBySeconds, toggleIn } from "~/lib/webhooks";
  import type { WebhookDeliveryOut, WebhookEndpointIn, WebhookEndpointOut } from "~~/lib/api/types/data-contracts";

  definePageMeta({
    middleware: ["auth"],
  });

  const { t } = useI18n();
  useHead({ title: `HomeBox | ${t("collection.tabs.webhooks")}` });

  const api = useUserApi();
  const pubApi = usePublicApi();
  const confirm = useConfirm();
  const { openDialog, closeDialog } = useDialog();
  const entityTypeStore = useEntityTypeStore();
  const tagStore = useTagStore();

  const { data: status } = useAsyncData("webhooks-status", async () => (await pubApi.status()).data);
  const enabled = computed(() => status.value?.webhooks?.enabled ?? false);

  const endpoints = useAsyncData("webhooks", async () => {
    if (!enabled.value) return [];
    const { data } = await api.webhooks.getAll();
    return data ?? [];
  });
  watch(enabled, () => void endpoints.refresh());

  const catalog = useAsyncData("webhook-events", async () => {
    if (!enabled.value) return [];
    const { data } = await api.webhooks.events();
    return data ?? [];
  });
  watch(enabled, () => void catalog.refresh());
  const eventGroups = computed(() => groupEvents(catalog.data.value ?? []));

  onMounted(() => {
    void entityTypeStore.ensureFetched();
    void tagStore.ensureAllTagsFetched();
  });
  const itemTypes = computed(() => entityTypeStore.itemTypes);
  const tags = computed(() => tagStore.tags);

  const ANY = "any";
  type Form = {
    id: string;
    name: string;
    url: string;
    events: string[];
    entityTypeId: string;
    tagId: string;
    isActive: boolean;
  };
  const blank = (): Form => ({
    id: "",
    name: "",
    url: "",
    events: ["item.created", "item.updated"],
    entityTypeId: ANY,
    tagId: ANY,
    isActive: true,
  });
  const form = ref<Form>(blank());
  const saving = ref(false);

  function openEditor(e: WebhookEndpointOut | null) {
    form.value = e
      ? {
          id: e.id,
          name: e.name,
          url: "",
          events: [...e.events],
          entityTypeId: e.entityTypeId ?? ANY,
          tagId: e.tagId ?? ANY,
          isActive: e.isActive,
        }
      : blank();
    openDialog(DialogID.EditWebhook);
  }

  const secret = ref("");
  async function copySecret() {
    try {
      await navigator.clipboard.writeText(secret.value);
      toast.success(t("webhooks.secret_copied"));
    } catch {
      toast.error(t("webhooks.copy_failed"));
    }
  }

  async function save() {
    if (saving.value) return;
    if (form.value.events.length === 0) {
      toast.error(t("webhooks.need_event"));
      return;
    }
    saving.value = true;
    const body: WebhookEndpointIn = {
      name: form.value.name.trim(),
      // On edit a blank URL keeps the stored one.
      url: form.value.url.trim() === "" ? null : form.value.url.trim(),
      events: form.value.events,
      entityTypeId: form.value.entityTypeId === ANY ? null : form.value.entityTypeId,
      tagId: form.value.tagId === ANY ? null : form.value.tagId,
      isActive: form.value.isActive,
    };
    if (form.value.id) {
      const res = await api.webhooks.update(form.value.id, body);
      saving.value = false;
      if (res.error) {
        toast.error(t("webhooks.toast.save_failed"));
        return;
      }
      toast.success(t("webhooks.toast.saved"));
      closeDialog(DialogID.EditWebhook);
    } else {
      const res = await api.webhooks.create(body);
      saving.value = false;
      if (res.error || !res.data) {
        toast.error(t("webhooks.toast.save_failed"));
        return;
      }
      closeDialog(DialogID.EditWebhook);
      secret.value = res.data.secret;
      openDialog(DialogID.WebhookSecret);
    }
    await endpoints.refresh();
  }

  async function remove(e: WebhookEndpointOut) {
    const { isCanceled } = await confirm.open(t("webhooks.delete_confirm", { name: e.name }));
    if (isCanceled) return;
    const res = await api.webhooks.delete(e.id);
    if (res.error) {
      toast.error(t("webhooks.toast.delete_failed"));
      return;
    }
    await endpoints.refresh();
  }

  async function rotate(e: WebhookEndpointOut) {
    const { isCanceled } = await confirm.open(t("webhooks.rotate_confirm", { name: e.name }));
    if (isCanceled) return;
    const res = await api.webhooks.rotateSecret(e.id);
    if (res.error || !res.data) {
      toast.error(t("webhooks.toast.save_failed"));
      return;
    }
    secret.value = res.data.secret;
    openDialog(DialogID.WebhookSecret);
  }

  async function sendTest(e: WebhookEndpointOut) {
    const res = await api.webhooks.test(e.id);
    if (res.error || !res.data) {
      toast.error(t("webhooks.toast.test_failed"));
      return;
    }
    if (res.data.ok) toast.success(t("webhooks.toast.test_ok", { code: res.data.statusCode }));
    else toast.error(t("webhooks.toast.test_rejected", { why: res.data.error || res.data.statusCode }));
    if (current.value?.id === e.id) await loadDeliveries();
  }

  // ---- delivery log ----
  const current = ref<WebhookEndpointOut | null>(null);
  const deliveries = ref<WebhookDeliveryOut[]>([]);
  const total = ref(0);
  const page = ref(1);
  const pageSize = 10;
  const loadingLog = ref(false);

  async function loadDeliveries() {
    if (!current.value) return;
    loadingLog.value = true;
    const res = await api.webhooks.deliveries(current.value.id, page.value, pageSize);
    loadingLog.value = false;
    deliveries.value = res.data?.items ?? [];
    total.value = res.data?.total ?? 0;
  }
  async function openLog(e: WebhookEndpointOut) {
    current.value = e;
    page.value = 1;
    openDialog(DialogID.WebhookDeliveries);
    await loadDeliveries();
  }
  async function redeliver(d: WebhookDeliveryOut) {
    if (!current.value) return;
    const res = await api.webhooks.redeliver(current.value.id, d.id);
    if (res.error) {
      toast.error(t("webhooks.toast.redeliver_failed"));
      return;
    }
    toast.success(t("webhooks.toast.redelivering"));
    setTimeout(() => void loadDeliveries(), 1500);
  }
  const lastPage = computed(() => Math.max(1, Math.ceil(total.value / pageSize)));
  watch(page, () => void loadDeliveries());

  const statusVariant = (s: string) => (s === "delivered" ? "default" : s === "failed" ? "destructive" : "secondary");
</script>

<template>
  <div class="space-y-4">
    <!-- Create / edit -->
    <Dialog :dialog-id="DialogID.EditWebhook">
      <DialogContent class="max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{{ form.id ? $t("webhooks.edit") : $t("webhooks.new") }}</DialogTitle>
        </DialogHeader>
        <form class="flex flex-col gap-3" @submit.prevent="save">
          <FormTextField v-model="form.name" :label="$t('global.name')" :max-length="255" :required="true" />
          <div>
            <FormTextField
              v-model="form.url"
              :label="$t('webhooks.url')"
              type="url"
              :placeholder="form.id ? $t('webhooks.url_keep') : 'https://'"
              :required="!form.id"
            />
            <p class="mt-1 text-xs text-muted-foreground">{{ $t("webhooks.url_hint") }}</p>
          </div>

          <div>
            <Label>{{ $t("webhooks.events") }}</Label>
            <div v-for="g in eventGroups" :key="g.key" class="mt-2">
              <p class="text-xs font-medium uppercase text-muted-foreground">{{ $t(`webhooks.groups.${g.key}`) }}</p>
              <div v-for="ev in g.events" :key="ev.name" class="mt-1 flex items-start gap-2">
                <Checkbox
                  :id="`wh-ev-${ev.name}`"
                  :model-value="form.events.includes(ev.name)"
                  class="mt-0.5 size-5"
                  @update:model-value="() => (form.events = toggleIn(form.events, ev.name))"
                />
                <Label :for="`wh-ev-${ev.name}`" class="cursor-pointer font-normal">
                  <code class="text-xs">{{ ev.name }}</code>
                  <span class="block text-xs text-muted-foreground">{{ ev.description }}</span>
                </Label>
              </div>
            </div>
            <p class="mt-2 text-xs text-muted-foreground">{{ $t("webhooks.dated_hint") }}</p>
          </div>

          <div>
            <Label for="wh-type">{{ $t("webhooks.only_type") }}</Label>
            <Select id="wh-type" v-model="form.entityTypeId">
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem :value="ANY">{{ $t("alerts.any") }}</SelectItem>
                <SelectItem v-for="et in itemTypes" :key="et.id" :value="et.id">{{ et.name }}</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div>
            <Label for="wh-tag">{{ $t("webhooks.only_tag") }}</Label>
            <Select id="wh-tag" v-model="form.tagId">
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem :value="ANY">{{ $t("alerts.any") }}</SelectItem>
                <SelectItem v-for="tg in tags" :key="tg.id" :value="tg.id">{{ tg.name }}</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <FormCheckbox v-model="form.isActive" :label="$t('profile.enabled')" />
          <DialogFooter>
            <Button type="submit" :disabled="saving">{{ $t("global.save") }}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <!-- Secret, shown once -->
    <Dialog :dialog-id="DialogID.WebhookSecret">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ $t("webhooks.secret_title") }}</DialogTitle>
        </DialogHeader>
        <p class="text-sm">{{ $t("webhooks.secret_once") }}</p>
        <div class="flex items-center gap-2">
          <code class="min-w-0 flex-1 break-all rounded-md border bg-muted p-2 text-xs" data-testid="webhook-secret">{{
            secret
          }}</code>
          <Button variant="outline" size="icon" :aria-label="$t('webhooks.copy')" @click="copySecret">
            <MdiContentCopy />
          </Button>
        </div>
        <p class="text-xs text-muted-foreground">{{ $t("webhooks.verify_hint") }}</p>
        <DialogFooter>
          <Button @click="closeDialog(DialogID.WebhookSecret)">{{ $t("webhooks.done") }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- Delivery log -->
    <Dialog :dialog-id="DialogID.WebhookDeliveries">
      <DialogContent class="max-h-[90vh] max-w-3xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{{ $t("webhooks.log_title", { name: current?.name ?? "" }) }}</DialogTitle>
        </DialogHeader>
        <p v-if="!loadingLog && deliveries.length === 0" class="py-4 text-center text-sm">
          {{ $t("webhooks.log_empty") }}
        </p>
        <ul v-else class="divide-y" data-testid="webhook-deliveries">
          <li v-for="d in deliveries" :key="d.id" class="flex flex-wrap items-center gap-2 py-2 text-sm">
            <Badge :variant="statusVariant(d.status)">{{ $t(`webhooks.status.${d.status}`) }}</Badge>
            <code class="text-xs">{{ d.eventType }}</code>
            <span class="text-muted-foreground">
              <DateTime format="relative" datetime-type="time" :date="d.createdAt" />
            </span>
            <span v-if="deliverySummary(d)" class="text-xs text-muted-foreground">{{ deliverySummary(d) }}</span>
            <span v-if="d.attempts > 1" class="text-xs text-muted-foreground">
              {{ $t("webhooks.attempts", { n: d.attempts }) }}
            </span>
            <Badge v-if="lateBySeconds(d) > 0" variant="outline" :title="$t('webhooks.late_hint')">
              {{ $t("webhooks.late", { by: humanDuration(lateBySeconds(d)) }) }}
            </Badge>
            <span class="grow" />
            <Button variant="ghost" size="sm" @click="redeliver(d)">{{ $t("webhooks.redeliver") }}</Button>
          </li>
        </ul>
        <div v-if="lastPage > 1" class="flex items-center justify-between pt-2">
          <Button variant="outline" size="sm" :disabled="page <= 1" @click="page--">{{ $t("webhooks.prev") }}</Button>
          <span class="text-xs">{{ page }} / {{ lastPage }}</span>
          <Button variant="outline" size="sm" :disabled="page >= lastPage" @click="page++">{{
            $t("webhooks.next")
          }}</Button>
        </div>
      </DialogContent>
    </Dialog>

    <div class="rounded-md border bg-card p-4">
      <header class="mb-2 flex items-center gap-2">
        <MdiWebhook class="-mt-1" />
        <div>
          <h2 class="text-lg font-semibold">{{ $t("webhooks.title") }}</h2>
          <p class="text-sm text-muted-foreground">{{ $t("webhooks.subtitle") }}</p>
        </div>
      </header>

      <p v-if="status && !enabled" class="rounded-md border border-dashed p-3 text-sm" data-testid="webhooks-disabled">
        {{ $t("webhooks.disabled") }} <code>HBOX_WEBHOOKS_ENABLED=true</code>
      </p>

      <template v-else>
        <div v-if="endpoints.data.value" class="mx-1 divide-y rounded-md border" data-testid="webhook-list">
          <p v-if="endpoints.data.value.length === 0" class="p-2 text-center text-sm">{{ $t("webhooks.none") }}</p>
          <article v-for="e in endpoints.data.value" v-else :key="e.id" class="p-2">
            <div class="flex flex-wrap items-center gap-2">
              <p class="mr-auto text-lg">{{ e.name }}</p>
              <Button variant="outline" size="sm" @click="sendTest(e)">{{ $t("webhooks.send_test") }}</Button>
              <Button variant="outline" size="sm" @click="openLog(e)">{{ $t("webhooks.log") }}</Button>
              <Button variant="outline" size="sm" @click="rotate(e)">{{ $t("webhooks.rotate") }}</Button>
              <Button variant="outline" size="icon" :aria-label="$t('global.edit')" @click="openEditor(e)"
                ><MdiPencil
              /></Button>
              <Button variant="destructive" size="icon" :aria-label="$t('global.delete')" @click="remove(e)"
                ><MdiDelete
              /></Button>
            </div>
            <div class="flex flex-wrap items-center gap-1 py-1 text-sm">
              <Badge :variant="e.isActive ? 'default' : 'destructive'">
                {{ e.isActive ? $t("profile.active") : $t("profile.inactive") }}
              </Badge>
              <code class="text-xs text-muted-foreground">{{ e.urlHost }}</code>
              <Badge v-for="ev in e.events" :key="ev" variant="outline">{{ ev }}</Badge>
            </div>
          </article>
        </div>
        <div class="mt-4">
          <Button variant="secondary" size="sm" @click="openEditor(null)">{{ $t("global.create") }}</Button>
        </div>
      </template>
    </div>
  </div>
</template>
