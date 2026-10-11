<script setup lang="ts">
  import { useI18n } from "vue-i18n";
  import { toast } from "@/components/ui/sonner";
  import { Button } from "@/components/ui/button";
  import { Badge } from "@/components/ui/badge";
  import { Input } from "@/components/ui/input";
  import { Label } from "@/components/ui/label";
  import { Checkbox } from "@/components/ui/checkbox";
  import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
  import { useDialog } from "@/components/ui/dialog-provider";
  import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
  import FormTextField from "~/components/Form/TextField.vue";
  import FormCheckbox from "~/components/Form/Checkbox.vue";
  import MdiBellRing from "~icons/mdi/bell-ring";
  import MdiDelete from "~icons/mdi/delete";
  import MdiPencil from "~icons/mdi/pencil";
  import { DialogID } from "~/components/ui/dialog-provider/utils";
  import { useEntityTypeStore } from "~/stores/entityTypes";
  import { useTagStore } from "~/stores/tags";
  import {
    PRESET_OFFSETS,
    TRIGGERS,
    isTrigger,
    offsetLabel,
    parseOffset,
    toggleOffset,
    type Trigger,
  } from "~/lib/alert-rules";
  import type { NotificationRuleIn, NotificationRuleOut } from "~~/lib/api/types/data-contracts";

  definePageMeta({
    middleware: ["auth"],
  });

  const { t } = useI18n();
  useHead({ title: `HomeBox | ${t("collection.tabs.alerts")}` });

  const api = useUserApi();
  const confirm = useConfirm();
  const { openDialog, closeDialog } = useDialog();
  const entityTypeStore = useEntityTypeStore();
  const tagStore = useTagStore();

  const rules = useAsyncData("alert-rules", async () => {
    const { data } = await api.notificationRules.getAll();
    return data ?? [];
  });
  const notifiers = useAsyncData("alert-rule-notifiers", async () => {
    const { data } = await api.notifiers.getAll();
    return data ?? [];
  });
  onMounted(() => {
    void entityTypeStore.ensureFetched();
    void tagStore.ensureAllTagsFetched();
  });
  const itemTypes = computed(() => entityTypeStore.itemTypes);
  const tags = computed(() => tagStore.tags);

  // "any" stands for no filter, because a select cannot hold an empty value.
  const ANY = "any";

  type Form = {
    id: string;
    name: string;
    trigger: Trigger;
    offsetsDays: number[];
    entityTypeId: string;
    tagId: string;
    notifierIds: string[];
    isActive: boolean;
  };
  const blank = (): Form => ({
    id: "",
    name: "",
    trigger: "expires_at",
    offsetsDays: [7, 1, 0],
    entityTypeId: ANY,
    tagId: ANY,
    notifierIds: [],
    isActive: true,
  });
  const form = ref<Form>(blank());
  const customOffset = ref("");
  const saving = ref(false);

  function openEditor(r: NotificationRuleOut | null) {
    customOffset.value = "";
    form.value = r
      ? {
          id: r.id,
          name: r.name,
          trigger: isTrigger(r.trigger) ? r.trigger : "expires_at",
          offsetsDays: [...r.offsetsDays],
          entityTypeId: r.entityTypeId ?? ANY,
          tagId: r.tagId ?? ANY,
          notifierIds: [...r.notifierIds],
          isActive: r.isActive,
        }
      : blank();
    openDialog(DialogID.EditAlertRule);
  }

  function addCustom() {
    const n = parseOffset(customOffset.value);
    if (n === null) {
      toast.error(t("alerts.invalid_offset"));
      return;
    }
    if (!form.value.offsetsDays.includes(n)) form.value.offsetsDays = toggleOffset(form.value.offsetsDays, n);
    customOffset.value = "";
  }

  function toggleNotifier(id: string, on: boolean) {
    const set = new Set(form.value.notifierIds);
    if (on) set.add(id);
    else set.delete(id);
    form.value.notifierIds = [...set];
  }

  async function save() {
    if (saving.value) return;
    if (form.value.offsetsDays.length === 0) {
      toast.error(t("alerts.need_offset"));
      return;
    }
    saving.value = true;
    const body: NotificationRuleIn = {
      name: form.value.name.trim(),
      trigger: form.value.trigger,
      offsetsDays: form.value.offsetsDays,
      entityTypeId:
        form.value.trigger === "maintenance_due" || form.value.entityTypeId === ANY ? null : form.value.entityTypeId,
      tagId: form.value.tagId === ANY ? null : form.value.tagId,
      notifierIds: form.value.notifierIds,
      isActive: form.value.isActive,
    };
    const res = form.value.id
      ? await api.notificationRules.update(form.value.id, body)
      : await api.notificationRules.create(body);
    saving.value = false;
    if (res.error) {
      toast.error(t("alerts.toast.save_failed"));
      return;
    }
    toast.success(t("alerts.toast.saved"));
    closeDialog(DialogID.EditAlertRule);
    await rules.refresh();
  }

  async function remove(r: NotificationRuleOut) {
    const { isCanceled } = await confirm.open(t("alerts.delete_confirm", { name: r.name }));
    if (isCanceled) return;
    const res = await api.notificationRules.delete(r.id);
    if (res.error) {
      toast.error(t("alerts.toast.delete_failed"));
      return;
    }
    await rules.refresh();
  }

  async function toggleActive(r: NotificationRuleOut) {
    const res = await api.notificationRules.update(r.id, {
      name: r.name,
      trigger: r.trigger,
      offsetsDays: r.offsetsDays,
      entityTypeId: r.entityTypeId ?? null,
      tagId: r.tagId ?? null,
      notifierIds: r.notifierIds,
      isActive: !r.isActive,
    });
    if (res.error) toast.error(t("alerts.toast.save_failed"));
    await rules.refresh();
  }

  function notifierNames(r: NotificationRuleOut): string {
    if (r.notifierIds.length === 0) return t("alerts.all_notifiers");
    const byId = new Map((notifiers.data.value ?? []).map(n => [n.id, n.name]));
    return r.notifierIds.map(id => byId.get(id) ?? "?").join(", ");
  }
</script>

<template>
  <div class="space-y-4">
    <Dialog :dialog-id="DialogID.EditAlertRule">
      <DialogContent class="max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{{ form.id ? $t("alerts.edit_rule") : $t("alerts.new_rule") }}</DialogTitle>
        </DialogHeader>

        <form class="flex flex-col gap-3" @submit.prevent="save">
          <FormTextField v-model="form.name" :label="$t('global.name')" :max-length="255" :required="true" />

          <div>
            <Label for="alert-trigger">{{ $t("alerts.trigger") }}</Label>
            <Select id="alert-trigger" v-model="form.trigger">
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem v-for="tr in TRIGGERS" :key="tr" :value="tr">{{ $t(`alerts.triggers.${tr}`) }}</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div>
            <Label>{{ $t("alerts.when") }}</Label>
            <div class="mt-1 flex flex-wrap gap-1" data-testid="offset-presets">
              <Button
                v-for="o in PRESET_OFFSETS"
                :key="o"
                type="button"
                size="sm"
                :variant="form.offsetsDays.includes(o) ? 'default' : 'outline'"
                @click="form.offsetsDays = toggleOffset(form.offsetsDays, o)"
              >
                {{ offsetLabel(o, $t) }}
              </Button>
            </div>
            <div class="mt-2 flex gap-2">
              <Input
                v-model="customOffset"
                inputmode="numeric"
                :placeholder="$t('alerts.custom_offset')"
                @keydown.enter.prevent="addCustom"
              />
              <Button type="button" variant="secondary" @click="addCustom">{{ $t("global.add") }}</Button>
            </div>
            <div
              v-if="form.offsetsDays.some(o => !(PRESET_OFFSETS as readonly number[]).includes(o))"
              class="mt-2 flex flex-wrap gap-1"
            >
              <Badge
                v-for="o in form.offsetsDays.filter(o => !(PRESET_OFFSETS as readonly number[]).includes(o))"
                :key="o"
                class="cursor-pointer"
                @click="form.offsetsDays = toggleOffset(form.offsetsDays, o)"
              >
                {{ offsetLabel(o, $t) }} ×
              </Badge>
            </div>
            <p class="mt-1 text-xs text-muted-foreground">{{ $t("alerts.when_hint") }}</p>
          </div>

          <div v-if="form.trigger !== 'maintenance_due'">
            <Label for="alert-type">{{ $t("alerts.only_type") }}</Label>
            <Select id="alert-type" v-model="form.entityTypeId">
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem :value="ANY">{{ $t("alerts.any") }}</SelectItem>
                <SelectItem v-for="et in itemTypes" :key="et.id" :value="et.id">{{ et.name }}</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div>
            <Label for="alert-tag">{{ $t("alerts.only_tag") }}</Label>
            <Select id="alert-tag" v-model="form.tagId">
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem :value="ANY">{{ $t("alerts.any") }}</SelectItem>
                <SelectItem v-for="tg in tags" :key="tg.id" :value="tg.id">{{ tg.name }}</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div>
            <Label>{{ $t("alerts.send_to") }}</Label>
            <p class="text-xs text-muted-foreground">{{ $t("alerts.send_to_hint") }}</p>
            <p v-if="(notifiers.data.value ?? []).length === 0" class="mt-1 text-sm">{{ $t("alerts.no_notifiers") }}</p>
            <div v-for="n in notifiers.data.value ?? []" :key="n.id" class="mt-1 flex items-center gap-2">
              <Checkbox
                :id="`alert-n-${n.id}`"
                :model-value="form.notifierIds.includes(n.id)"
                class="size-5"
                @update:model-value="v => toggleNotifier(n.id, v === true)"
              />
              <Label :for="`alert-n-${n.id}`" class="cursor-pointer">{{ n.name }}</Label>
            </div>
          </div>

          <FormCheckbox v-model="form.isActive" :label="$t('profile.enabled')" />

          <DialogFooter>
            <Button type="submit" :disabled="saving">{{ $t("global.save") }}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>

    <div class="rounded-md border bg-card p-4">
      <header class="mb-2 flex items-center gap-2">
        <MdiBellRing class="-mt-1" />
        <div>
          <h2 class="text-lg font-semibold">{{ $t("alerts.title") }}</h2>
          <p class="text-sm text-muted-foreground">{{ $t("alerts.subtitle") }}</p>
        </div>
      </header>

      <div v-if="rules.data.value" class="mx-1 divide-y rounded-md border" data-testid="alert-rules">
        <p v-if="rules.data.value.length === 0" class="p-2 text-center text-sm">{{ $t("alerts.none") }}</p>
        <article v-for="r in rules.data.value" v-else :key="r.id" class="p-2">
          <div class="flex flex-wrap items-center gap-2">
            <p class="mr-auto text-lg">{{ r.name }}</p>
            <Button variant="outline" size="sm" @click="toggleActive(r)">
              {{ r.isActive ? $t("alerts.turn_off") : $t("alerts.turn_on") }}
            </Button>
            <Button variant="outline" size="icon" :aria-label="$t('global.edit')" @click="openEditor(r)">
              <MdiPencil />
            </Button>
            <Button variant="destructive" size="icon" :aria-label="$t('global.delete')" @click="remove(r)">
              <MdiDelete />
            </Button>
          </div>
          <div class="flex flex-wrap items-center gap-1 py-1 text-sm">
            <Badge :variant="r.isActive ? 'default' : 'destructive'">
              {{ r.isActive ? $t("profile.active") : $t("profile.inactive") }}
            </Badge>
            <Badge variant="secondary">{{ $t(`alerts.triggers.${r.trigger}`) }}</Badge>
            <Badge v-for="o in r.offsetsDays" :key="o" variant="outline">{{ offsetLabel(o, $t) }}</Badge>
          </div>
          <p class="text-xs text-muted-foreground">{{ $t("alerts.sent_to", { who: notifierNames(r) }) }}</p>
        </article>
      </div>

      <div class="mt-4">
        <Button variant="secondary" size="sm" @click="openEditor(null)">{{ $t("global.create") }}</Button>
      </div>
    </div>
  </div>
</template>
