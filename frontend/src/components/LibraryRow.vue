<script setup lang="ts">
import { computed } from 'vue'
import type { LibraryItem } from '../api/types.ts'
import { bytes, bitrate, date } from '../i18n/format.ts'
import { t, errorText } from '../i18n/index.ts'
import AppCell from './AppCell.vue'
import Icon from './Icon.vue'
import TableRow from './TableRow.vue'

const props = defineProps<{
  row: LibraryItem
  marked: boolean
  busy: boolean
  canceling: boolean
}>()
defineEmits<{ toggle: []; cancel: [] }>()
const version = computed(() =>
  props.row.version && props.row.minimumOs
    ? `${props.row.version} (${props.row.minimumOs}+)`
    : props.row.version,
)
const size = computed(() => bytes(props.row.status === 'done' ? props.row.size : props.row.total))
const progress = computed(() =>
  props.row.total > 0
    ? Math.min(100, Math.max(0, (props.row.received / props.row.total) * 100))
    : undefined,
)
const status = computed(() => {
  const labels: Record<string, string> = {
    preparing: t.value.preparing,
    canceling: t.value.canceling,
    queued: t.value.waiting,
    downloading: t.value.downloading,
    done: t.value.readyToHost,
    failed: t.value.failed,
    canceled: t.value.canceled,
  }
  return labels[props.row.status] ?? props.row.status
})
</script>

<template>
  <TableRow
    selection
    selectable
    :selected="marked"
    :disabled="busy"
    :label="row.name"
    @toggle="$emit('toggle')"
  >
    <AppCell :app="row">
      <span class="mobile-only app-meta">
        <template v-if="version">{{ version }} ·</template>
        {{ size }}
      </span>
    </AppCell>
    <td class="job-status">
      <div class="transfer-status">
        <div class="transfer-copy">
          <div class="transfer-line">
            <span class="badge" :class="{ warning: row.status === 'failed' }">{{ status }}</span>
            <span v-if="row.status === 'downloading' && row.speed > 0" class="transfer-speed">
              {{ bitrate(row.speed) }}
            </span>
          </div>
          <small v-if="row.error">{{ errorText(row.error) }}</small>
          <span v-if="row.status === 'downloading'" class="transfer-meta">
            {{ bytes(row.received) }}
            <template v-if="row.total > 0">/ {{ bytes(row.total) }}</template>
          </span>
          <div
            v-if="row.status === 'downloading' || row.status === 'preparing'"
            class="download-progress"
            :class="{ indeterminate: row.status === 'preparing' }"
            role="progressbar"
            :aria-label="row.name"
            :aria-valuenow="row.status === 'preparing' ? undefined : progress"
            :aria-valuemin="0"
            :aria-valuemax="100"
          >
            <span :key="row.status" :style="{ transform: `scaleX(${(progress ?? 0) / 100})` }" />
          </div>
        </div>
        <button
          v-if="row.canManage && ['queued', 'downloading', 'preparing'].includes(row.status)"
          class="danger cancel-download"
          :disabled="busy"
          :aria-label="t.cancelDownload + ' — ' + row.name"
          :title="t.cancelDownload"
          :aria-busy="canceling"
          @click.stop="$emit('cancel')"
        >
          <span class="cancel-download-face">
            <span v-if="canceling" class="spinner"></span>
            <Icon v-else name="close" :size="16" />
          </span>
        </button>
      </div>
    </td>
    <td class="detail-cell version-cell">{{ version || t.noValue }}</td>
    <td class="detail-cell size-cell">{{ size }}</td>
    <td class="detail-cell date-cell">{{ date(row.createdAt) }}</td>
  </TableRow>
</template>

<style>
.job-status small {
  display: block;
  margin-top: var(--s2);
}
@media (max-width: 600px) {
  .job-status {
    grid-column: 2 / -1;
    margin-left: var(--s10);
  }
}
.download-progress {
  display: block;
  height: var(--s1);
  width: 100%;
  overflow: hidden;
  background: var(--subtle-line);
  border-radius: var(--pill);
  margin-top: var(--s1);
}
.download-progress span {
  display: block;
  height: 100%;
  width: 100%;
  background: var(--accent);
  transform-origin: left;
  transform: scaleX(0);
  transition: transform var(--ease) linear;
}
.download-progress.indeterminate span {
  width: 35%;
  transform: none !important;
  animation: shimmer 1.5s linear infinite;
}
.transfer-status {
  display: flex;
  align-items: center;
  gap: var(--s2);
}
.transfer-copy {
  flex: 1;
  min-width: 0;
}
.transfer-line {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--s1) var(--s2);
}
.transfer-speed,
.transfer-meta {
  color: var(--muted);
  font-size: var(--xs);
  font-variant-numeric: tabular-nums;
}
.transfer-meta {
  display: block;
  margin-top: var(--s1);
}
.cancel-download {
  width: var(--control);
  min-height: var(--control);
  height: var(--control);
  flex-shrink: 0;
  padding: 0;
  border: 0;
  border-radius: var(--r2);
  background: transparent;
}
.cancel-download:not(:disabled):hover,
.cancel-download:disabled {
  background: transparent;
}
.cancel-download-face {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: 100%;
  flex-shrink: 0;
  border: 1px solid currentColor;
  border-radius: inherit;
  background: var(--soft);
}
.cancel-download:not(:disabled):hover .cancel-download-face {
  background: var(--selected);
}
</style>
