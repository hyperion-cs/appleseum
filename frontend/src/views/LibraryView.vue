<script setup lang="ts">
import { computed, defineAsyncComponent, reactive, ref, toRef, useTemplateRef, watch } from 'vue'
import { apiErrorCode } from '../api/errors.ts'
import { api } from '../api/http.ts'
import { fetchState } from '../api/state.ts'
import type { FileExport, LibraryItem, Selection, Share, State } from '../api/types.ts'
import { useActive, useAlive } from '../composables/lifetime.ts'
import { useReadError } from '../composables/useAction.ts'
import { useFileDrop } from '../composables/useFileDrop.ts'
import { useIpaImport } from '../composables/useIpaImport.ts'
import { useLibrary } from '../composables/useLibrary.ts'
import { useListScreen } from '../composables/useListScreen.ts'
import { errorText, t, useErrorMessage } from '../i18n/index.ts'
import { downloadFile } from '../platform/browser.ts'
import ConfirmationContent from '../components/ConfirmationContent.vue'
import Icon from '../components/Icon.vue'
import LibraryRow from '../components/LibraryRow.vue'
import ListMenu from '../components/ListMenu.vue'
import MenuButton from '../components/MenuButton.vue'
import MessageDialog from '../components/MessageDialog.vue'
import Modal from '../components/Modal.vue'
import SegmentedControl from '../components/SegmentedControl.vue'
import ToggleSwitch from '../components/ToggleSwitch.vue'
import AppTable from './AppTable.vue'
import SharesView from './SharesView.vue'
import { appColumns } from './columns.ts'
import { loadShareDialog, warm } from './lazy.ts'

const ShareDialog = defineAsyncComponent(loadShareDialog)
const props = defineProps<{ state: State; systemNotice?: boolean; downloadsStarted?: number }>()
const emit = defineEmits<{ expired: []; state: [value: State] }>()
const section = defineModel<'downloads' | 'shares'>('section', { required: true })
const alive = useAlive()
const active = useActive()
const { code: errorCode, message: error } = useErrorMessage()

const {
  importing,
  failures: importFailures,
  transfer: browserTransfer,
  upload,
  cancel: cancelBrowserImport,
} = useIpaImport(async (id, completed) => {
  if (completed) return list.reloadFromStart()
  list.clearPages()
  await list.load()
  list.deselect([id])
})
const list = reactive(
  useLibrary(
    { state: toRef(props, 'state'), error: useReadError(errorCode), active },
    importing,
    browserTransfer,
  ),
)
watch(
  () => props.downloadsStarted,
  () => void list.reloadFromStart(),
  { flush: 'sync' },
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
} = useListScreen<LibraryItem, 'import' | 'delete' | 'deleteAll'>(list, errorCode, () =>
  emit('expired'),
)

const columns = computed(() =>
  appColumns(['name', 'status', 'version', 'size', 'createdAt'], list.canSort),
)
const showingShares = computed(() => section.value === 'shares' && props.state.shareCount > 0)
const sections = computed(() => [
  { value: 'downloads' as const, label: t.value.libraryFiles, icon: 'download' },
  {
    value: 'shares' as const,
    label: t.value.activeShares,
    icon: 'share',
    count: t.value.itemCount(props.state.shareCount),
  },
])
const confirmation = computed(() => {
  if (dialog.value !== 'delete' && dialog.value !== 'deleteAll') return null
  const all = dialog.value === 'deleteAll'
  return {
    title: all ? t.value.confirmDeleteAll : t.value.confirmDelete,
    confirm: () =>
      remove(all ? { ids: [], filter: { query: '', delisted: false, mine: false } } : selection()),
  }
})
const dialogTitle = computed(() => {
  if (confirmation.value) return errorCode.value ? t.value.actionFailed : confirmation.value.title
  return dialog.value === 'import' ? t.value.importIpaTitle : t.value.more
})

function dismissMessage() {
  errorCode.value = notice.value = ''
  if (!importing.value) importFailures.value = []
}

function selection(ids = list.selectedIds()): Selection {
  return { ids, filter: null }
}

const canImport = computed(
  () =>
    !showingShares.value &&
    (!props.state.mode.public || !!props.state.account || props.state.mode.service),
)
const fileInput = useTemplateRef<HTMLInputElement>('fileInput')

async function importFiles(files: File[]) {
  if (!canImport.value || busy.value || importing.value || !files.length) return
  closeDialog()
  notice.value = ''
  const sessionError = await upload(files)
  if (!alive()) return
  if (sessionError) emit('expired')
  list.clearPages()
  await list.load()
}

function pickFiles() {
  const input = fileInput.value!
  const files = [...(input.files ?? [])]
  input.value = ''
  void importFiles(files)
}

const fileDrop = useFileDrop({
  enabled: () => canImport.value,
  ready: () => !busy.value && !importing.value,
  drop: (files) => void importFiles(files),
})
watch(active, () => {
  fileDrop.reset()
  if (dialog.value === 'import') closeDialog()
})

const share = ref<Share | null>(null)
const sharesView = useTemplateRef('sharesView')

async function syncShares() {
  const next = await fetchState()
  if (!alive()) return
  emit('state', next)
  await sharesView.value?.refresh()
}

function startShare() {
  if (!chosen()) return
  return run(async () => {
    const requested = selection()
    const started = await api.post<Share>('/shares', requested)
    if (!alive()) return
    list.deselect(requested.ids)
    section.value = 'shares'
    share.value = started
    await syncShares()
  }, 'share')
}

function showShare(active: Share) {
  share.value = active
  notice.value = ''
}

function sharesStopped(ids: string[]) {
  if (share.value && ids.includes(share.value.id)) share.value = null
}

// Each state poll re-reads the share on screen, which may have ended elsewhere.
watch(
  () => props.state,
  async () => {
    const id = share.value?.id
    if (!id || busyTask.value === 'share') return
    try {
      const current = await api.get<Share>('/shares/' + id)
      if (alive() && share.value?.id === id) share.value = current
    } catch (e) {
      const ended = ['not_found', 'sign_in_required'].includes(apiErrorCode(e, ''))
      if (ended && alive() && share.value?.id === id) share.value = null
    }
  },
)

function exportSelected() {
  if (!list.canShare) return
  return run(async () => {
    const { url } = await api.post<FileExport>('/library/export', selection())
    if (!alive()) return
    downloadFile(url)
    dialog.value = null
  }, 'export')
}

function requestRemoval() {
  if (chosen() && list.canManage) openDialog('delete')
}

function remove(input: Selection) {
  return run(async () => {
    const localId = browserTransfer.value?.id
    if (localId && (input.filter || input.ids.includes(localId))) {
      await cancelBrowserImport(localId)
      input.ids = input.ids.filter((id) => id !== localId)
    }
    if (input.filter || input.ids.length) await api.post('/library/delete', input)
    closeDialog()
    list.setSelection([])
    list.clearPages()
    await list.load()
  }, 'delete')
}

function cancelDownload(id: string) {
  return run(async () => {
    if (browserTransfer.value?.id === id) await cancelBrowserImport(id)
    else await api.post('/downloads/' + id + '/cancel')
    list.deselect([id])
    list.clearPages()
    await list.load(true)
  }, 'cancel:' + id)
}
</script>
<template>
  <main class="workspace">
    <section
      class="list-section"
      :class="{ 'import-dragging': fileDrop.dragging.value }"
      :aria-label="t.downloads"
      v-on="fileDrop.handlers"
    >
      <template v-if="state.shareCount > 0">
        <SegmentedControl
          v-model="section"
          class="library-sections"
          :label="t.librarySections"
          :options="sections"
        />
        <SharesView
          v-show="showingShares"
          ref="sharesView"
          :active="showingShares && active"
          :state="state"
          :system-notice="systemNotice"
          @view="showShare"
          @stopped="sharesStopped"
          @changed="syncShares"
        />
      </template>
      <AppTable
        v-show="!showingShares"
        v-model:query="list.query"
        class="downloads-table"
        :list="list"
        :columns="columns"
        :label="t.downloads"
        :busy="busy"
        @toggle-all="toggleAll"
        @menu="openDialog('menu')"
      >
        <template #actions>
          <input
            ref="fileInput"
            type="file"
            accept=".ipa"
            multiple
            hidden
            :disabled="busy || importing || !canImport"
            :aria-label="t.importIpa"
            @change="pickFiles"
          />
          <button
            class="primary"
            :class="{ pending: busyTask === 'share' }"
            :disabled="busy || !list.canShare"
            :aria-busy="busyTask === 'share'"
            :aria-label="t.share"
            :title="t.share"
            @click="startShare"
            @pointerenter="list.canShare && warm(loadShareDialog)"
            @focus="list.canShare && warm(loadShareDialog)"
          >
            <Icon name="share" :size="18" />
            <span>{{ t.share }}</span>
            <span v-if="list.marked.size">({{ list.marked.size }})</span>
          </button>
          <button
            class="danger"
            :disabled="busy || !list.canManage"
            :aria-label="t.remove"
            :title="list.marked.size && !list.canManage ? t.errors.read_only : t.remove"
            @click="requestRemoval"
          >
            <Icon name="trash" :size="18" />
            <span>{{ t.remove }}</span>
          </button>
        </template>
        <template #before>
          <p v-if="state.storageError" class="notice" role="alert">
            {{ errorText(state.storageError) }}
          </p>
          <p v-if="list.backgroundError && !systemNotice && !errorCode" class="notice" role="alert">
            {{ errorText(list.backgroundError) }}
          </p>
          <div v-if="fileDrop.dragging.value" class="import-drop-overlay" aria-hidden="true">
            <Icon name="import" :size="32" />
            {{ t.dropIpa }}
          </div>
        </template>
        <template #empty>
          <strong>{{ list.query ? t.emptySearch : t.empty }}</strong>
          <p>{{ list.query ? t.emptySearch : t.downloadsEmpty }}</p>
        </template>
        <LibraryRow
          v-for="row in list.rows"
          :key="row.id"
          :row="row"
          :marked="list.marked.has(row.id)"
          :busy="busy"
          :canceling="busyTask === 'cancel:' + row.id"
          @toggle="toggle(row)"
          @cancel="cancelDownload(row.id)"
        />
      </AppTable>
    </section>
    <MessageDialog
      v-if="!dialog && !share && (errorCode || notice || (!importing && importFailures.length))"
      :code="errorCode"
      :message="notice"
      :details="importing ? [] : importFailures"
      @close="dismissMessage"
    />
    <Modal v-if="dialog" :dismissible="!busy" :title="dialogTitle" @close="closeDialog">
      <ConfirmationContent
        v-if="confirmation"
        :action="t.remove"
        :busy="busy"
        :error="errorCode"
        @close="closeDialog"
        @confirm="confirmation.confirm"
      >
        <p>{{ t.deleteText }}</p>
      </ConfirmationContent>
      <div
        v-else-if="dialog === 'import'"
        class="import-picker"
        :class="{ 'import-dragging': fileDrop.dragging.value }"
        v-on="fileDrop.handlers"
      >
        <Icon name="import" :size="32" />
        <p v-if="fileDrop.dragging.value">{{ t.dropIpa }}</p>
        <p v-else>
          {{ t.importHint }}
          <span class="import-drop-hint">{{ t.importDropHint }}</span>
        </p>
        <div class="modal-actions">
          <button @click="closeDialog">{{ t.cancel }}</button>
          <button
            class="primary"
            :disabled="busy || importing || !canImport"
            @click="fileInput?.click()"
          >
            <Icon name="import" :size="18" />
            {{ t.chooseIpa }}
          </button>
        </div>
      </div>
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
        <ToggleSwitch v-if="list.canFilterMine" v-model="list.mine" :label="t.mineOnly" />
        <MenuButton
          v-if="canImport"
          icon="import"
          :disabled="busy || importing"
          :busy="importing"
          @click="openDialog('import')"
        >
          {{ t.importIpa }}
        </MenuButton>
        <MenuButton
          icon="download"
          :disabled="busy || !list.canShare"
          :busy="busyTask === 'export'"
          @click="exportSelected"
        >
          {{ list.marked.size > 1 ? t.exportZip : t.exportIpa }}
        </MenuButton>
        <MenuButton
          icon="trash"
          class="danger"
          :disabled="busy || (!list.rows.length && !list.query)"
          @click="openDialog('deleteAll')"
        >
          {{ t.removeAll }}
        </MenuButton>
      </ListMenu>
    </Modal>
    <ShareDialog
      v-if="share"
      :key="share.id"
      :share="share"
      :error="errorCode"
      :public-mode="state.mode.public"
      @close="((share = null), (errorCode = ''))"
    />
  </main>
</template>

<style>
.library-sections {
  margin: 0 auto var(--s3);
}
.import-picker > svg {
  color: var(--muted);
  margin-bottom: var(--s3);
}
.import-drop-hint {
  display: none;
}
@media (hover: hover) and (pointer: fine) {
  .import-drop-hint {
    display: block;
    margin-top: var(--s2);
  }
}
.import-dragging {
  outline: 2px dashed var(--accent);
  outline-offset: 4px;
}
.import-drop-overlay {
  position: absolute;
  inset: 0;
  z-index: 5;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--s3);
  background: var(--surface);
  border-radius: var(--r1);
  pointer-events: none;
}
</style>
