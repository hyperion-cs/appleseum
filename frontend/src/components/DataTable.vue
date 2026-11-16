<script setup lang="ts">
import { computed, ref, type HTMLAttributes } from 'vue'
import type { PageDirection } from '../api/pagination.ts'
import { t } from '../i18n/index.ts'
import type { TableColumn, TablePagination, TableSelection } from './table.ts'
import Icon from './Icon.vue'
import PageSize from './PageSize.vue'
import SelectionHeader from './SelectionHeader.vue'
import { useHeightPageSize } from './useHeightPageSize.ts'

defineOptions({ inheritAttrs: false })
const props = withDefaults(
  defineProps<{
    columns: readonly TableColumn[]
    sort?: string
    direction?: string
    selection?: TableSelection
    pagination?: TablePagination
    pageSizeMode?: 'fixed' | 'height'
    compact?: boolean
    responsive?: boolean
    dense?: boolean
    loading?: boolean
    empty?: boolean
    emptyText?: string
    skeletonRows?: number
    skeletonVisible?: boolean
    wrapperClass?: HTMLAttributes['class']
    // The table scrolls inside its wrapper and shows the app's scrollbar there.
    scrollable?: boolean
  }>(),
  { responsive: true, skeletonVisible: true },
)
const emit = defineEmits<{
  sort: [key: string]
  toggleAll: []
  pageSize: [value: number]
  fitPageSize: [value: number]
  autoPageSize: []
  navigate: [direction: PageDirection]
}>()
const root = ref<HTMLElement>()
useHeightPageSize(
  root,
  () =>
    props.pageSizeMode === 'height' &&
    !!props.pagination &&
    !props.compact &&
    !props.loading &&
    !props.empty &&
    !props.pagination.disabled,
  (size) => emit('fitPageSize', size),
)
const columnCount = computed(
  () => props.columns.length + (props.selection && !props.compact ? 1 : 0),
)

function sortable(column: TableColumn) {
  return props.sort !== undefined && column.sortable !== false
}

function ariaSort(column: TableColumn) {
  if (!sortable(column)) return undefined
  if (props.sort !== column.key) return 'none'
  return props.direction === 'asc' ? 'ascending' : 'descending'
}
</script>

<template>
  <div ref="root" class="data-table" :class="{ 'fixed-table': !responsive, 'dense-table': dense }">
    <slot v-if="!compact" name="toolbar" />
    <slot name="before" />
    <div class="table-wrap" :class="wrapperClass" :data-scrollbar="scrollable ? '' : undefined">
      <slot name="overlay" />
      <table v-bind="$attrs" :class="{ compact }">
        <slot name="columns" />
        <thead v-if="!compact">
          <tr>
            <th v-if="selection" class="check-col">
              <SelectionHeader v-bind="selection" @toggle="$emit('toggleAll')" />
            </th>
            <th
              v-for="column in columns"
              :key="column.key"
              :class="column.class"
              :title="column.title"
              :aria-sort="ariaSort(column)"
            >
              <button
                v-if="sortable(column)"
                class="sort-heading"
                @click="$emit('sort', column.key)"
              >
                {{ column.label }}
                <Icon
                  v-if="sort === column.key"
                  :name="direction === 'asc' ? 'up' : 'down'"
                  :size="14"
                />
              </button>
              <span v-else>{{ column.label }}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          <template v-if="loading">
            <tr v-if="!skeletonRows">
              <td :colspan="columnCount" class="table-placeholder">
                <slot name="loading"><span class="spinner"></span></slot>
              </td>
            </tr>
            <tr v-for="n in skeletonRows ?? 0" :key="n" class="skeleton-row">
              <td :colspan="columnCount">
                <div class="skeleton" :class="{ visible: skeletonVisible }"></div>
              </td>
            </tr>
          </template>
          <tr v-else-if="empty">
            <td :colspan="columnCount" class="table-placeholder table-empty">
              <slot name="empty">{{ emptyText ?? t.emptySearch }}</slot>
            </td>
          </tr>
          <slot v-else />
        </tbody>
      </table>
    </div>
    <div v-if="pagination && !compact" class="pagination">
      <PageSize
        :model-value="pagination.size"
        :automatic="pageSizeMode === undefined ? undefined : pageSizeMode === 'height'"
        @update:model-value="$emit('pageSize', $event)"
        @automatic="$emit('autoPageSize')"
      />
      <span v-if="pagination.total !== undefined" class="list-total">
        {{ t.totalItems }}: {{ pagination.total }}
      </span>
      <div>
        <button
          v-for="direction in ['previous', 'next'] as const"
          :key="direction"
          class="icon-button"
          :disabled="pagination.disabled || !pagination[direction]"
          :aria-label="direction === 'previous' ? t.previous : t.next"
          :aria-busy="pagination.pending === direction"
          @click="$emit('navigate', direction)"
        >
          <span v-if="pagination.pending === direction" class="spinner"></span>
          <Icon v-else :name="direction === 'previous' ? 'back' : 'next'" />
        </button>
      </div>
    </div>
  </div>
</template>

<style>
.table-wrap {
  position: relative;
  border: 1px solid var(--subtle-line);
  border-radius: var(--r2);
  background: var(--surface);
  overflow: hidden;
}
.updating {
  pointer-events: none;
}
table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
  font-size: var(--sm);
}
th {
  font-weight: var(--bold);
  color: var(--text);
  text-align: left;
  background: var(--soft);
  padding: var(--s2) var(--s3);
  font-size: var(--xs);
}
td {
  padding: var(--s2) var(--s3);
  border-top: 1px solid var(--subtle-line);
  vertical-align: middle;
  overflow-wrap: anywhere;
}
.check-col {
  width: 48px;
  padding-right: 0;
}
tr.selectable {
  cursor: pointer;
}
tbody tr:nth-child(even) {
  background: var(--bg);
}
tbody tr.selected {
  background: var(--selected);
  box-shadow: inset 3px 0 0 var(--accent);
}
tr.selectable:hover {
  background: var(--soft);
}
.checkbox {
  -webkit-appearance: none;
  appearance: none;
  height: 22px;
  width: 22px;
  min-height: 0;
  padding: 0;
  border-radius: 6px;
  flex-shrink: 0;
  border: 1.5px solid var(--line);
  background: transparent;
}
.checkbox.checked {
  background: var(--primary);
  border-color: var(--primary);
  color: var(--on-primary);
}
.app-summary {
  display: flex;
  align-items: center;
  gap: var(--s3);
  min-height: var(--s10);
}
.check-col .checkbox {
  display: flex;
}
.pagination {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  align-items: center;
  margin-top: var(--s3);
  padding-top: var(--s3);
  gap: var(--s3);
  font-size: var(--xs);
  color: var(--muted);
}
.list-total {
  flex: 1;
  text-align: center;
  white-space: nowrap;
}
.pagination > div:not(.page-size) {
  display: flex;
  gap: var(--s2);
}
.sort-heading {
  border: 0;
  background: transparent;
  min-height: var(--s6);
  padding: 0;
  font-size: inherit;
  font-weight: inherit;
  white-space: normal;
  text-align: left;
  justify-content: flex-start;
}
.data-table.dense-table th,
.data-table.dense-table td {
  padding: var(--s2);
  vertical-align: top;
}
.data-table.dense-table .check-col {
  vertical-align: middle;
}

.data-table table.compact td {
  border-top: 0;
}
.data-table .table-placeholder {
  grid-column: 1 / -1;
  padding: var(--s4);
}
@media (max-width: 600px) {
  table {
    display: block;
  }
  thead {
    position: absolute;
    height: 1px;
    width: 1px;
    overflow: hidden;
    clip-path: inset(50%);
  }
  tbody {
    display: block;
  }
  tbody tr {
    display: grid;
    grid-template-columns: 32px minmax(0, 1fr) auto;
    align-items: center;
    gap: var(--s1) 0;
    padding: var(--s2);
    border-top: 1px solid var(--subtle-line);
  }
  tbody tr:first-child {
    border-top: 0;
  }
  td {
    border: 0;
    padding: 0;
  }
  .check-col {
    display: flex;
    align-items: center;
    grid-column: 1;
    grid-row: 1;
    align-self: stretch;
    width: 32px;
  }
  .pagination {
    gap: var(--s2);
  }
  .data-table.dense-table td {
    padding: 0;
  }

  .data-table table.compact tbody tr {
    grid-template-columns: minmax(0, 1fr) auto;
    column-gap: var(--s2);
  }
  .data-table table.compact td:first-child {
    grid-column: 1;
  }
  .data-table table.compact td:last-child {
    grid-column: 2;
    grid-row: 1;
  }
  .data-table.fixed-table table {
    display: table;
  }
  .data-table.fixed-table thead {
    position: static;
    display: table-header-group;
    height: auto;
    width: auto;
    overflow: visible;
    clip-path: none;
  }
  .data-table.fixed-table tbody {
    display: table-row-group;
  }
  .data-table.fixed-table tbody tr {
    display: table-row;
    padding: 0;
  }
  .data-table.fixed-table td {
    display: table-cell;
    padding: var(--s2) var(--s3);
    border-top: 1px solid var(--subtle-line);
  }
}
</style>
