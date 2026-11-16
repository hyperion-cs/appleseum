<script setup lang="ts">
import { computed, reactive } from 'vue'
import type { DebugEvent, State } from '../api/types.ts'
import { useDebugLog } from '../composables/useDebugLog.ts'
import { clock, decimal } from '../i18n/format.ts'
import { t, errorText, useErrorMessage } from '../i18n/index.ts'
import ChoiceSelect from '../components/ChoiceSelect.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import DataTable from '../components/DataTable.vue'
import Icon from '../components/Icon.vue'
import MenuButton from '../components/MenuButton.vue'
import MessageDialog from '../components/MessageDialog.vue'
import Modal from '../components/Modal.vue'
import TableRow from '../components/TableRow.vue'
import TableToolbar from '../components/TableToolbar.vue'

const bytesPerMebibyte = 1_048_576

defineProps<{ state: State; systemNotice?: boolean }>()
const { code: errorCode, message: error } = useErrorMessage()
const log = reactive(useDebugLog(errorCode))
const sourceOptions = computed(() => [
  { value: '', label: t.value.debugAll },
  { value: 'Apple', label: t.value.debugApple },
  { value: 'API', label: t.value.debugApi },
  { value: 'Hosting', label: t.value.debugHosting },
  { value: 'System', label: t.value.debugSystem },
])
const sourceLabels = computed<Record<string, string>>(() => ({
  ...Object.fromEntries(sourceOptions.value.map((option) => [option.value, option.label])),
  Auth: t.value.debugAuth,
}))
const columns = computed(() => [
  { key: 'time', label: t.value.debugTime },
  { key: 'source', label: t.value.debugSource },
  { key: 'request', label: t.value.debugRequest },
  { key: 'status', label: t.value.debugStatus },
  { key: 'duration', label: t.value.debugDuration, title: t.value.debugHeadersTime },
  { key: 'details', label: t.value.debugDetails },
])
// The command that started the server, quoted so it can be pasted into a shell.
const launchCommand = computed(() =>
  (log.info ? [log.info.executable, ...log.info.arguments] : [])
    .map((value) =>
      /^[A-Za-z0-9_@%+=:,./-]+$/.test(value) ? value : "'" + value.replaceAll("'", "'\\''") + "'",
    )
    .join(' '),
)

function eventTarget(event: DebugEvent) {
  const hosting: Record<string, string> = {
    sharing_started: t.value.debugSharingStarted,
    sharing_stopped: t.value.debugSharingStopped,
  }
  return (event.source === 'Hosting' && hosting[event.target]) || event.target
}

function failed(event: DebugEvent) {
  return (
    event.status >= 400 ||
    event.source === 'System' ||
    (event.status === 0 && event.source === 'Apple')
  )
}

function closeDialog() {
  log.menu = log.confirmingClear = false
  errorCode.value = ''
}
</script>

<template>
  <main class="debug-panel">
    <MessageDialog
      v-if="errorCode && !log.menu && !log.confirmingClear"
      :code="errorCode"
      @close="errorCode = ''"
    />
    <div v-if="log.info" class="debug-summary panel">
      <span>{{ log.info.runtime }}</span>
      <span>{{ t.debugUptime }}: {{ decimal(log.info.uptimeSeconds) }} s</span>
      <span>{{ t.debugMemory }}: {{ decimal(log.info.heapBytes / bytesPerMebibyte) }} MiB</span>
      <span>{{ t.debugGoroutines }}: {{ log.info.goroutines }}</span>
      <span class="debug-session">
        <span v-if="state.account" class="connection-dot" aria-hidden="true"></span>
        {{ state.account ? t.debugSignedIn : t.debugSignedOut }}
      </span>
      <span v-if="state.catalog.status === 'loading'">
        {{ t.debugCatalogProgress(decimal(state.catalog.progress)) }}
      </span>
      <span class="debug-launch">
        <code>{{ launchCommand }}</code>
      </span>
    </div>
    <DataTable
      :columns="columns"
      :page-size-mode="log.automatic ? 'height' : 'fixed'"
      class="debug-table"
      dense
      :aria-label="t.debug"
      :aria-busy="log.busy || log.selecting"
      :selection="{
        checked: log.allSelected,
        partial: log.someSelected,
        disabled: log.selecting || !log.snapshot?.total,
      }"
      :loading="!log.snapshot"
      :skeleton-rows="log.limit"
      :skeleton-visible="log.showSkeleton"
      :empty="!!log.snapshot && !log.events.length"
      :pagination="{
        size: log.limit,
        total: log.snapshot?.total ?? 0,
        previous: log.visiblePage?.previous,
        next: log.visiblePage?.next,
        disabled: log.busy,
      }"
      @toggle-all="log.toggleAll"
      @page-size="log.choosePageSize"
      @fit-page-size="log.fitPageSize"
      @auto-page-size="log.autoPageSize"
      @navigate="log.navigate"
    >
      <template #toolbar>
        <TableToolbar
          v-model:query="log.query"
          :search-label="t.debugSearch"
          search-class="debug-search"
          class="debug-controls"
          menu
          mobile-menu
          @menu="log.menu = true"
        >
          <button
            class="debug-har"
            :disabled="!log.selected.size || log.downloading"
            :aria-busy="log.downloading"
            :aria-label="t.debugDownloadHar + (log.selected.size ? ` (${log.selected.size})` : '')"
            :title="t.debugDownloadHar"
            @click="log.downloadHar"
          >
            <Icon name="download" :size="16" />
            <span>
              <span class="debug-action-label">{{ t.debugDownloadHar }}</span>
              <span class="debug-har-short" aria-hidden="true">HAR</span>
              <template v-if="log.selected.size">({{ log.selected.size }})</template>
            </span>
          </button>
          <button :disabled="log.downloading" @click="log.download('/debug/csv', 'events.csv')">
            <Icon name="download" :size="16" />
            CSV
          </button>
          <button
            class="debug-clear"
            :disabled="log.busy"
            :aria-label="t.debugClear"
            :title="t.debugClear"
            @click="log.confirmingClear = true"
          >
            <Icon name="trash" :size="16" />
            <span class="debug-action-label">{{ t.debugClear }}</span>
          </button>

          <template #filters>
            <ChoiceSelect
              v-model="log.sources"
              :label="t.debugSource"
              clear-value=""
              :summary="log.sources.length > 1 ? t.debugSourceCount(log.sources.length) : undefined"
              class="debug-filter"
              :options="sourceOptions"
            />
          </template>
        </TableToolbar>
      </template>
      <template #before>
        <p v-if="log.backgroundError && !systemNotice && !errorCode" class="notice" role="alert">
          {{ errorText(log.backgroundError) }}
        </p>
      </template>
      <template #columns>
        <colgroup>
          <col class="debug-check-width" />
          <col class="debug-time-width" />
          <col class="debug-source-width" />
          <col class="debug-target-width" />
          <col class="debug-status-width" />
          <col class="debug-duration-width" />
          <col />
        </colgroup>
      </template>
      <template #empty>
        <span class="debug-description">{{ t.debugEmpty }}</span>
      </template>
      <TableRow
        v-for="event in log.events"
        :key="event.id"
        selection
        :selectable="event.hasResponse"
        :selected="log.selected.has(event.id)"
        :label="t.debugRequest + ' ' + event.id"
        @toggle="log.toggle(event)"
      >
        <td :title="event.at">{{ clock(event.at) }}</td>
        <td class="debug-source-cell">{{ sourceLabels[event.source] ?? event.source }}</td>
        <td class="debug-target-cell">{{ event.method }} {{ eventTarget(event) }}</td>
        <td :class="{ 'debug-error': failed(event) }">{{ event.status || '—' }}</td>
        <td class="debug-duration-cell">
          <template v-if="event.method">
            {{ decimal(event.durationMs) }}
            <span class="mobile-only">{{ t.debugDuration }}</span>
          </template>
          <template v-else>—</template>
        </td>
        <td
          class="debug-detail-cell"
          :class="{ 'debug-detail-empty': !event.detail && !event.hasResponse }"
        >
          {{ event.detail || (event.hasResponse ? '' : '—') }}
          <small v-if="event.hasResponse">
            {{ t.debugResponse }}: {{ decimal(event.responseBytes) }} {{ t.debugResponseByte }}
          </small>
          <small v-if="event.responseTruncated">{{ t.debugTruncated }}</small>
        </td>
      </TableRow>
    </DataTable>
    <Modal v-if="log.menu" :title="t.more" @close="closeDialog">
      <div class="menu-actions">
        <p v-if="error" class="error" role="alert">{{ error }}</p>
        <MenuButton
          icon="selectAll"
          :disabled="log.selecting || !log.snapshot?.total"
          @click="log.selectAll"
        >
          {{ t.all }}
        </MenuButton>
        <MenuButton
          icon="deselectAll"
          :disabled="!log.selected.size || log.selecting"
          @click="log.clearSelection"
        >
          {{ t.clear }}
        </MenuButton>
      </div>
    </Modal>
    <ConfirmDialog
      v-if="log.confirmingClear"
      :title="t.debugClear"
      :action="t.clearData"
      :busy="log.busy"
      :error="errorCode"
      @close="closeDialog"
      @confirm="log.clear"
    >
      <p>{{ t.debugClearConfirm }}</p>
    </ConfirmDialog>
  </main>
</template>

<style>
.debug-panel {
  display: grid;
  padding-top: var(--s3);
  gap: var(--s3);
  min-width: 0;
}
.debug-summary {
  display: flex;
  flex-wrap: wrap;
  gap: var(--s2) var(--s4);
  padding: var(--s3);
  font-size: var(--sm);
  color: var(--muted);
}
.debug-controls .actions {
  display: flex;
  gap: var(--s2);
}
.debug-har-short {
  display: none;
}
.debug-session {
  display: inline-flex;
  align-items: center;
  gap: var(--s2);
}
.debug-controls .debug-filter {
  width: 180px;
  max-width: 100%;
}
.debug-description {
  margin: 0;
  color: var(--muted);
  font-size: var(--sm);
}

.debug-table .debug-check-width {
  width: 40px;
}
.debug-table .debug-time-width {
  width: 88px;
}
.debug-table .debug-source-width {
  width: 104px;
}
.debug-table .debug-target-width {
  width: 28%;
}
.debug-table .debug-status-width {
  width: 64px;
}
.debug-table .debug-duration-width {
  width: 88px;
}
.debug-table th:nth-child(2) {
  width: auto;
}
.debug-table td:nth-child(2),
.debug-table td:nth-child(5),
.debug-table td:nth-child(6),
.debug-table th:nth-child(5),
.debug-table th:nth-child(6) {
  white-space: nowrap;
  overflow-wrap: normal;
  font-variant-numeric: tabular-nums;
}
.debug-table .debug-error {
  color: var(--danger);
}
.debug-detail-cell small {
  display: block;
  color: var(--muted);
}
.debug-controls .search-tools {
  flex: 1;
  min-width: 180px;
}
.debug-search {
  flex: 1;
  min-width: 0;
}
@media (max-width: 820px) and (min-width: 601px) {
  .debug-table .debug-source-width {
    width: 84px;
  }
  .debug-table .debug-time-width {
    width: 76px;
  }
  .debug-table .debug-duration-width {
    width: 76px;
  }
  .debug-table .debug-target-width {
    width: 24%;
  }
}
@media (max-width: 600px) {
  .debug-controls {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .debug-controls .actions {
    grid-column: 1 / -1;
  }
  .debug-controls .actions button {
    padding-inline: var(--s2);
  }
  .debug-har {
    flex: 1;
  }
  .debug-controls .actions .debug-clear {
    width: var(--control);
    flex-shrink: 0;
  }
  .debug-action-label {
    display: none;
  }
  .debug-har-short {
    display: inline;
  }
  .debug-controls .debug-filter,
  .debug-controls .search-tools,
  .debug-search {
    width: auto;
    min-width: 0;
  }
  .debug-filter .choice-trigger {
    padding-inline: var(--s2);
    gap: var(--s1);
    font-size: var(--sm);
    white-space: nowrap;
  }
  .debug-filter .choice-label {
    overflow-wrap: normal;
  }
  .debug-table colgroup {
    display: none;
  }
  .debug-table tbody tr {
    grid-template-columns: 32px minmax(0, 1fr) auto;
    padding: var(--s2);
    gap: var(--s1);
  }
  .debug-table .check-col {
    grid-column: 1;
    grid-row: 1;
  }
  .debug-target-cell {
    grid-column: 2 / -1;
    grid-row: 1;
    font-weight: var(--medium);
  }
  .debug-table td:nth-child(2) {
    grid-column: 2;
    grid-row: 2;
    color: var(--muted);
  }
  .debug-source-cell {
    grid-column: 3;
    grid-row: 2;
    color: var(--muted);
  }
  .debug-table td:nth-child(5) {
    grid-column: 2;
    grid-row: 3;
  }
  .debug-duration-cell {
    grid-column: 3;
    grid-row: 3;
    color: var(--muted);
    text-align: right;
  }
  .debug-detail-cell {
    grid-column: 2 / -1;
    grid-row: 4;
    color: var(--muted);
    font-size: var(--xs);
  }
  .debug-detail-empty {
    display: none;
  }
}

.debug-launch {
  flex-basis: 100%;
  min-width: 0;
}
.debug-launch code {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.debug-table .skeleton-row td {
  height: 42px;
}
.debug-table .skeleton-row .skeleton {
  height: var(--s4);
  border-radius: var(--r1);
}
@media (max-width: 600px) {
  .debug-table .skeleton-row td {
    grid-column: 1 / -1;
    min-height: var(--s16);
    width: 100%;
  }
}
.connection-dot {
  background: var(--success);
  height: 7px;
  width: 7px;
  flex-shrink: 0;
  border-radius: var(--pill);
}
</style>
