<template>
  <div v-if="lookup" class="mt-3" data-testid="lookup-panel">
    <div class="flex flex-wrap items-center gap-3 rounded-md border bg-muted/40 p-3 text-sm">
      <MdiLoading v-if="lookup.status === 'pending'" class="size-5 shrink-0 animate-spin" />
      <MdiAlertCircleOutline v-else-if="lookup.status === 'failed'" class="size-5 shrink-0 text-destructive" />
      <MdiTextSearch v-else class="size-5 shrink-0" />

      <div class="min-w-0 flex-1">
        <p v-if="lookup.status === 'pending'">{{ $t("items.lookup.searching") }}</p>
        <template v-else-if="lookup.status === 'failed'">
          <p class="font-medium">{{ $t("items.lookup.failed") }}</p>
          <p class="text-muted-foreground">
            {{ $t("items.lookup.failed_detail", { query: lookup.query, error: lookup.error }) }}
          </p>
        </template>
        <p v-else-if="lookup.candidates.length === 0">{{ $t("items.lookup.nothing_found") }}</p>
        <p v-else-if="lookup.candidates.length === 1">{{ $t("items.lookup.found_one") }}</p>
        <p v-else>{{ $t("items.lookup.found", { count: lookup.candidates.length }) }}</p>
      </div>

      <div class="flex items-center gap-2">
        <Button v-if="hasCandidates" size="sm" @click="openReview">
          {{ $t("items.lookup.review") }}
        </Button>
        <Button v-if="lookup.status !== 'pending' && !hasCandidates" size="sm" variant="outline" @click="request">
          {{ $t("items.lookup.find_again") }}
        </Button>
        <Button v-if="lookup.status !== 'pending'" size="sm" variant="ghost" @click="dismiss">
          {{ $t("items.lookup.dismiss") }}
        </Button>
      </div>
    </div>

    <Dialog :dialog-id="DialogID.ItemLookupReview">
      <DialogContent class="max-h-[90vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{{ $t("items.lookup.review_title") }}</DialogTitle>
          <DialogDescription>{{ $t("items.lookup.review_description") }}</DialogDescription>
        </DialogHeader>

        <div v-if="candidate" class="space-y-4">
          <div class="space-y-2" role="radiogroup" :aria-label="$t('items.lookup.review_title')">
            <button
              v-for="(c, idx) in lookup.candidates"
              :key="idx"
              type="button"
              role="radio"
              :aria-checked="idx === selected"
              class="w-full rounded-md border p-3 text-left transition-colors"
              :class="idx === selected ? 'border-primary bg-primary/5' : 'hover:bg-muted/50'"
              @click="select(idx)"
            >
              <div class="flex flex-wrap items-baseline justify-between gap-2">
                <span class="font-medium">{{ c.name }}</span>
                <span class="text-xs text-muted-foreground">
                  {{ $t("items.lookup.match_confidence", { percent: Math.round(c.confidence * 100) }) }}
                </span>
              </div>
              <p class="text-xs text-muted-foreground">
                <span v-if="c.manufacturer">{{ c.manufacturer }}</span>
                <span v-if="c.manufacturer && c.modelNumber"> · </span>
                <span v-if="c.modelNumber">{{ c.modelNumber }}</span>
              </p>
              <p v-if="c.sources.length" class="text-xs text-muted-foreground">
                {{ $t("items.lookup.sources", { sources: c.sources.join(", ") }) }}
              </p>
            </button>
          </div>

          <a
            v-if="safeSourceURL"
            :href="safeSourceURL"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-flex items-center gap-1 text-sm text-primary hover:underline"
          >
            <MdiOpenInNew class="size-4" />
            {{ $t("items.lookup.open_source") }}
          </a>

          <div class="overflow-x-auto rounded-md border">
            <table class="w-full text-sm">
              <thead class="bg-muted/50 text-left">
                <tr>
                  <th class="w-10 p-2"></th>
                  <th class="p-2">{{ $t("items.lookup.detail") }}</th>
                  <th class="p-2">{{ $t("items.lookup.current") }}</th>
                  <th class="p-2">{{ $t("items.lookup.suggested") }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="row in rows" :key="row.field" class="border-t align-top">
                  <td class="p-2">
                    <Checkbox
                      :id="`lookup-${row.field}`"
                      :model-value="chosen.has(row.field)"
                      @update:model-value="toggle(row.field, $event === true)"
                    />
                  </td>
                  <td class="p-2 font-medium">
                    <label :for="`lookup-${row.field}`" class="cursor-pointer">
                      {{ $t(`items.lookup.field_${row.field}`) }}
                    </label>
                  </td>
                  <td class="max-w-48 break-words p-2 text-muted-foreground">
                    <span v-if="row.current">{{ row.current }}</span>
                    <em v-else>{{ $t("items.lookup.empty") }}</em>
                  </td>
                  <td class="max-w-64 break-words p-2">{{ row.suggested }}</td>
                </tr>
                <tr v-if="candidate.imageURL" class="border-t align-top">
                  <td class="p-2">
                    <Checkbox
                      id="lookup-image"
                      :model-value="chosen.has('image')"
                      @update:model-value="toggle('image', $event === true)"
                    />
                  </td>
                  <td class="p-2 font-medium">
                    <label for="lookup-image" class="cursor-pointer">{{ $t("items.lookup.field_image") }}</label>
                  </td>
                  <td colspan="2" class="p-2 text-muted-foreground">{{ $t("items.lookup.image_available") }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        <DialogFooter class="gap-2">
          <Button variant="ghost" :disabled="applying" @click="dismissFromReview">
            {{ $t("items.lookup.dismiss") }}
          </Button>
          <Button
            :disabled="applying || chosen.size === 0"
            :title="chosen.size === 0 ? $t('items.lookup.nothing_selected') : undefined"
            @click="apply"
          >
            <MdiLoading v-if="applying" class="mr-2 size-4 animate-spin" />
            {{ $t("items.lookup.apply") }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>

<script setup lang="ts">
  import { useI18n } from "vue-i18n";
  import { toast } from "@/components/ui/sonner";
  import { Button } from "@/components/ui/button";
  import { Checkbox } from "@/components/ui/checkbox";
  import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
  } from "@/components/ui/dialog";
  import { useDialog } from "@/components/ui/dialog-provider";
  import { DialogID } from "~/components/ui/dialog-provider/utils";
  import type { EntityLookupOut, EntityOut, LookupCandidate } from "~~/lib/api/types/data-contracts";
  import MdiAlertCircleOutline from "~icons/mdi/alert-circle-outline";
  import MdiLoading from "~icons/mdi/loading";
  import MdiOpenInNew from "~icons/mdi/open-in-new";
  import MdiTextSearch from "~icons/mdi/text-search";

  const props = defineProps<{ item: EntityOut }>();
  const emit = defineEmits<{ applied: [item: EntityOut] }>();

  const { t } = useI18n();
  const api = useUserApi();
  const { openDialog, closeDialog } = useDialog();

  const POLL_MS = 3000;
  const TEXT_FIELDS = ["name", "description", "manufacturer", "modelNumber"] as const;

  const lookup = ref<EntityLookupOut | null>(null);
  const selected = ref(0);
  const chosen = ref(new Set<string>());
  const applying = ref(false);
  let timer: ReturnType<typeof setTimeout> | undefined;

  const hasCandidates = computed(() => lookup.value?.status === "done" && lookup.value.candidates.length > 0);
  const candidate = computed<LookupCandidate | undefined>(() => lookup.value?.candidates[selected.value]);

  // Only web addresses are linked, whatever a page claimed its address to be.
  const safeSourceURL = computed(() => {
    const url = candidate.value?.sourceURL ?? "";
    return /^https?:\/\//i.test(url) ? url : "";
  });

  const rows = computed(() => {
    const c = candidate.value;
    if (!c) return [];
    return TEXT_FIELDS.map(field => ({
      field,
      current: props.item[field] ?? "",
      suggested: c[field] ?? "",
    })).filter(r => r.suggested && r.suggested !== r.current);
  });

  function hasPhoto() {
    return props.item.attachments?.some(a => a.type === "photo") ?? false;
  }

  // Fill the empty details and the missing picture by default; replacing what
  // the user already wrote has to be asked for.
  function resetChoices() {
    const next = new Set<string>();
    for (const row of rows.value) {
      if (!row.current) next.add(row.field);
    }
    if (candidate.value?.imageURL && !hasPhoto()) next.add("image");
    chosen.value = next;
  }

  function select(idx: number) {
    selected.value = idx;
    resetChoices();
  }

  function toggle(field: string, on: boolean) {
    const next = new Set(chosen.value);
    if (on) next.add(field);
    else next.delete(field);
    chosen.value = next;
  }

  async function load() {
    const resp = await api.lookups.get(props.item.id);
    lookup.value = resp.error ? null : resp.data;
    schedulePoll();
  }

  function schedulePoll() {
    clearTimeout(timer);
    if (lookup.value?.status === "pending") {
      timer = setTimeout(load, POLL_MS);
    }
  }

  async function request() {
    const resp = await api.lookups.request(props.item.id);
    if (resp.error) {
      toast.error(resp.status === 501 ? t("items.lookup.toast.not_available") : t("items.lookup.toast.request_failed"));
      return;
    }
    lookup.value = resp.data;
    toast.success(t("items.lookup.toast.requested"));
    schedulePoll();
  }

  function openReview() {
    select(0);
    openDialog(DialogID.ItemLookupReview);
  }

  async function apply() {
    if (applying.value || chosen.value.size === 0) return;
    applying.value = true;
    const resp = await api.lookups.apply(props.item.id, { candidate: selected.value, fields: [...chosen.value] });
    applying.value = false;
    if (resp.error) {
      toast.error(t("items.lookup.toast.apply_failed"));
      return;
    }
    toast.success(t("items.lookup.toast.applied"));
    closeDialog(DialogID.ItemLookupReview);
    lookup.value = null;
    emit("applied", resp.data);
  }

  async function dismiss() {
    const resp = await api.lookups.dismiss(props.item.id);
    if (resp.error) {
      toast.error(t("items.lookup.toast.request_failed"));
      return;
    }
    lookup.value = null;
    clearTimeout(timer);
  }

  async function dismissFromReview() {
    await dismiss();
    closeDialog(DialogID.ItemLookupReview);
  }

  onMounted(load);
  onBeforeUnmount(() => clearTimeout(timer));
  watch(() => props.item.id, load);

  defineExpose({ request });
</script>
