<script setup lang="ts">
import { minSearchLength } from '../api/types.ts'
import type { PageDirection } from '../api/pagination.ts'
import { t } from '../i18n/index.ts'
import DataTable from '../components/DataTable.vue'
import Icon from '../components/Icon.vue'
import TableToolbar from '../components/TableToolbar.vue'
import type { TableColumn } from '../components/table.ts'

// The parts of an app list the table shows and drives.
interface AppTableList {
  readonly page: { previous?: boolean; next?: boolean } | null
  readonly rows: readonly unknown[]
  readonly total: number
  readonly limit: number
  readonly automatic: boolean
  readonly loading: boolean
  readonly slow: boolean
  readonly navigating: PageDirection | ''
  readonly sort: string
  readonly direction: string
  readonly allSelected: boolean
  readonly someSelected: boolean
  readonly query: string
  changeSort(key: string): void
  choosePageSize(size: number): void
  fitPageSize(size: number): void
  autoPageSize(): void
  navigate(to: PageDirection): void
}

defineProps<{
  list: AppTableList
  columns: readonly TableColumn[]
  label: string
  busy: boolean
  // A first page that is still being produced keeps the placeholder rows.
  waiting?: boolean
}>()
const query = defineModel<string>('query', { required: true })
defineEmits<{ toggleAll: []; menu: [] }>()
</script>

<template>
  <!-- Rows shown before the first server page, such as an upload, count as content. -->
  <DataTable
    :columns="columns"
    :page-size-mode="list.automatic ? 'height' : 'fixed'"
    :aria-label="label"
    :aria-busy="list.loading"
    :sort="list.sort"
    :direction="list.direction"
    :selection="{
      checked: list.allSelected,
      partial: list.someSelected,
      disabled: busy || !list.total,
    }"
    :loading="(!list.page && list.loading && !list.rows.length) || waiting"
    :skeleton-rows="5"
    :skeleton-visible="list.slow || waiting"
    :empty="!!list.page && !list.rows.length && !waiting"
    :wrapper-class="{ updating: list.loading && !!list.page }"
    :pagination="{
      size: list.limit,
      total: list.page || list.rows.length ? list.total : undefined,
      previous: list.page?.previous,
      next: list.page?.next,
      disabled: list.loading,
      pending: list.navigating,
    }"
    @sort="list.changeSort"
    @toggle-all="$emit('toggleAll')"
    @page-size="list.choosePageSize"
    @fit-page-size="list.fitPageSize"
    @auto-page-size="list.autoPageSize"
    @navigate="list.navigate"
  >
    <template #toolbar>
      <TableToolbar
        v-model:query="query"
        :min-search-length="minSearchLength"
        :search-label="t.search"
        menu
        @menu="$emit('menu')"
      >
        <slot name="actions" />
      </TableToolbar>
    </template>
    <template #before><slot name="before" /></template>
    <template #overlay><div v-if="list.slow && list.page" class="load-line"></div></template>
    <template #empty>
      <div class="empty">
        <Icon name="library" :size="34" />
        <slot name="empty" />
      </div>
    </template>
    <slot />
  </DataTable>
</template>
