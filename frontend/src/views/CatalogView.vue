<script setup lang="ts">
import { computed, defineAsyncComponent, reactive, ref, toRef } from 'vue'
import { useRouter } from 'vue-router'
import { api } from '../api/http.ts'
import { fetchState } from '../api/state.ts'
import type { CatalogItem, DownloadRequest, DownloadVersion, State } from '../api/types.ts'
import { useActive, useAlive } from '../composables/lifetime.ts'
import { useReadError } from '../composables/useAction.ts'
import { useCatalog } from '../composables/useCatalog.ts'
import { useListScreen } from '../composables/useListScreen.ts'
import { percent } from '../i18n/format.ts'
import { errorText, t, useErrorMessage } from '../i18n/index.ts'
import CatalogRow from '../components/CatalogRow.vue'
import ConfirmationContent from '../components/ConfirmationContent.vue'
import Icon from '../components/Icon.vue'
import ListMenu from '../components/ListMenu.vue'
import MenuButton from '../components/MenuButton.vue'
import MessageDialog from '../components/MessageDialog.vue'
import Modal from '../components/Modal.vue'
import ToggleSwitch from '../components/ToggleSwitch.vue'
import AppTable from './AppTable.vue'
import { appColumns } from './columns.ts'
import { loadDownloadDialog, warm } from './lazy.ts'

const DownloadDialog = defineAsyncComponent(loadDownloadDialog)
const props = defineProps<{ state: State; systemNotice?: boolean }>()
const emit = defineEmits<{ expired: []; state: [value: State]; downloads: [] }>()
const router = useRouter()
const alive = useAlive()
const { code: errorCode, message: error } = useErrorMessage()
const list = reactive(
  useCatalog({
    state: toRef(props, 'state'),
    error: useReadError(errorCode),
    active: useActive(),
  }),
)
const {
  notice,
  running: busyTask,
  busy,
  run,
  dialog,
  openDialog,
  closeDialog,
  toggle,
  selectAll,
  clearSelection,
  toggleAll,
  chosen,
} = useListScreen<CatalogItem, 'clearCatalog'>(list, errorCode, () => emit('expired'))

// Apple omits sizes of delisted apps, so the column appears only when it has values.
const showSize = computed(() => !list.delisted && list.rows.some((row) => row.size != null))
const columns = computed(() =>
  appColumns(
    showSize.value ? ['name', 'status', 'size', 'purchasedAt'] : ['name', 'status', 'purchasedAt'],
    list.canSort,
  ),
)
const dialogTitle = computed(() =>
  dialog.value === 'clearCatalog'
    ? errorCode.value
      ? t.value.actionFailed
      : t.value.clearAvailableCache
    : t.value.more,
)

const refreshPending = ref(false)
const refreshing = computed(() => refreshPending.value || props.state.catalog.status === 'loading')
const refreshPercent = computed(() => percent(props.state.catalog.progress ?? 0))
const refreshLabel = computed(() =>
  refreshing.value ? t.value.refreshing(refreshPercent.value) : t.value.refresh,
)
const waiting = computed(() => refreshing.value && !list.rows.length)

async function refresh() {
  if (refreshing.value || busy.value) return
  refreshPending.value = true
  await run(async () => {
    await api.post('/catalog/refresh')
    const next = await fetchState()
    if (!alive()) return
    emit('state', next)
    refreshPending.value = false
    list.clearPages()
    await list.load(true)
  }, 'refresh')
  refreshPending.value = false
}

function clearCatalog() {
  if (refreshing.value || !props.state.catalog.cached) return closeDialog()
  return run(async () => {
    await api.delete('/catalog')
    list.reset()
    const next = await fetchState()
    if (!alive()) return
    emit('state', next)
    closeDialog()
  }, 'clearCatalog')
}

const downloading = ref<{ ids: string[]; names: Record<string, string> } | null>(null)

function download() {
  if (!chosen()) return
  return run(async () => {
    await list.updateSelectedDownloads()
    const ids = [...list.marked]
    if (!alive() || !chosen(ids.length)) return
    downloading.value = {
      ids,
      names: Object.fromEntries(list.rows.map((row) => [row.id, row.name])),
    }
  }, 'download')
}

function startDownloads(versions: DownloadVersion[]) {
  return run(async () => {
    await api.post('/downloads', {
      selection: { ids: downloading.value?.ids ?? [], filter: null },
      versions,
    } satisfies DownloadRequest)
    if (!alive()) return
    // Modal restores its history entry on unmount; finish that before routing.
    const closed = history.state?.modal
      ? new Promise((resolve) => window.addEventListener('popstate', resolve, { once: true }))
      : null
    downloading.value = null
    list.setSelection([])
    list.clearPages()
    emit('downloads')
    await closed
    if (alive()) await router.push({ name: 'library' })
  }, 'download')
}
</script>
<template>
  <main class="workspace">
    <section class="list-section" :aria-label="t.available">
      <AppTable
        v-model:query="list.query"
        :list="list"
        :columns="columns"
        :label="t.available"
        :busy="busy"
        :waiting="waiting"
        @toggle-all="toggleAll"
        @menu="openDialog('menu')"
      >
        <template #actions>
          <button
            class="primary"
            :class="{ pending: busyTask === 'download' }"
            :disabled="busy || !list.marked.size"
            :aria-busy="busyTask === 'download'"
            :aria-label="t.download"
            :title="t.download"
            @click="download"
            @pointerenter="list.marked.size && warm(loadDownloadDialog)"
            @focus="list.marked.size && warm(loadDownloadDialog)"
          >
            <Icon name="download" :size="18" />
            <span>{{ t.download }}</span>
            <span v-if="list.marked.size">({{ list.marked.size }})</span>
          </button>
          <button
            class="refresh-button"
            :class="{ pending: refreshing, refreshing }"
            :disabled="refreshing || busy"
            :aria-busy="refreshing"
            :aria-label="refreshLabel"
            :title="refreshLabel"
            @click="refresh"
          >
            <Icon name="refresh" :size="18" />
            <span>{{ refreshLabel }}</span>
            <span v-if="refreshing" class="refresh-mobile-progress">({{ refreshPercent }})</span>
          </button>
        </template>
        <template #before>
          <p v-if="list.backgroundError && !systemNotice && !errorCode" class="notice" role="alert">
            {{ errorText(list.backgroundError) }}
          </p>
        </template>
        <template #empty>
          <strong>{{ list.query ? t.emptySearch : t.empty }}</strong>
          <p>{{ list.query ? t.emptySearch : t.availableEmpty }}</p>
          <button
            v-if="!list.query && !refreshing"
            class="primary"
            :disabled="busy"
            @click="refresh"
          >
            <Icon name="refresh" :size="18" />
            {{ t.refresh }}
          </button>
        </template>
        <CatalogRow
          v-for="row in list.rows"
          :key="row.id"
          :row="row"
          :marked="list.marked.has(row.id)"
          :busy="busy"
          :show-size="showSize"
          @toggle="toggle(row)"
        />
      </AppTable>
    </section>
    <MessageDialog
      v-if="!dialog && !downloading && (errorCode || notice)"
      :code="errorCode"
      :message="notice"
      @close="errorCode = notice = ''"
    />
    <Modal v-if="dialog" :dismissible="!busy" :title="dialogTitle" @close="closeDialog">
      <ConfirmationContent
        v-if="dialog === 'clearCatalog'"
        :action="t.clearData"
        :busy="busy"
        :error="errorCode"
        @close="closeDialog"
        @confirm="clearCatalog"
      >
        <p>{{ t.clearAvailableCacheConfirm }}</p>
      </ConfirmationContent>
      <ListMenu
        v-else
        :columns="columns"
        :sort="list.sort"
        :direction="list.direction"
        :error="error"
        :busy="busy"
        :searching="!!list.query"
        :rows="list.rows.length"
        :selected="list.marked.size"
        @sort="list.changeSort"
        @select-all="selectAll"
        @clear="clearSelection"
      >
        <ToggleSwitch v-model="list.delisted" :label="t.delistedOnly" />
        <MenuButton
          icon="trash"
          :disabled="busy || refreshing || !state.catalog.cached"
          @click="openDialog('clearCatalog')"
        >
          {{ t.clearAvailableCache }}
        </MenuButton>
      </ListMenu>
    </Modal>
    <DownloadDialog
      v-if="downloading"
      :ids="downloading.ids"
      :names="downloading.names"
      :busy="busy"
      :error="error"
      @close="((downloading = null), (errorCode = ''))"
      @confirm="startDownloads"
      @expired="emit('expired')"
    />
  </main>
</template>

<style>
.refresh-mobile-progress {
  display: none;
}
@media (max-width: 600px) {
  .toolbar .actions > .refresh-button.refreshing {
    width: var(--s16);
    flex-direction: column;
    padding: var(--s1);
    gap: 0;
  }
  .toolbar .actions > .refresh-button > .refresh-mobile-progress {
    display: block;
    font-size: var(--xs);
    line-height: 1;
    font-variant-numeric: tabular-nums;
  }
}
</style>
