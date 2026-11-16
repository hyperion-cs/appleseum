<script setup lang="ts">
import { computed, reactive } from 'vue'
import { minSearchLength, type Share, type State } from '../api/types.ts'
import { useShares } from '../composables/useShares.ts'
import { date } from '../i18n/format.ts'
import { t, errorText, useErrorMessage } from '../i18n/index.ts'
import AppArtwork from '../components/AppArtwork.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import DataTable from '../components/DataTable.vue'
import Icon from '../components/Icon.vue'
import MenuButton from '../components/MenuButton.vue'
import MessageDialog from '../components/MessageDialog.vue'
import Modal from '../components/Modal.vue'
import SortOptions from '../components/SortOptions.vue'
import TableRow from '../components/TableRow.vue'
import TableToolbar from '../components/TableToolbar.vue'

const props = defineProps<{ state: State; active: boolean; systemNotice?: boolean }>()
const emit = defineEmits<{ view: [share: Share]; changed: []; stopped: [ids: string[]] }>()
const { code: errorCode, message: error } = useErrorMessage()
const shares = reactive(
  useShares(
    props,
    { changed: () => emit('changed'), stopped: (ids) => emit('stopped', ids) },
    errorCode,
  ),
)
const columns = computed(() => [
  { key: 'name', label: t.value.name, class: 'share-app' },
  ...(props.state.mode.service
    ? [{ key: 'owner', label: t.value.shareOwner, class: 'share-owner' }]
    : []),
  { key: 'createdAt', label: t.value.shareStarted, class: 'share-created' },
  { key: 'actions', label: '', class: 'share-view', sortable: false },
])

function closeDialog() {
  shares.menu = false
  shares.confirming = null
  errorCode.value = ''
}

defineExpose({ refresh: shares.refresh })
</script>

<template>
  <section class="shares-section" :aria-label="t.activeShares">
    <DataTable
      :columns="columns"
      :page-size-mode="shares.automatic ? 'height' : 'fixed'"
      class="shares-table"
      :aria-label="t.activeShares"
      :aria-busy="shares.loading || shares.selecting"
      :compact="shares.compact"
      :sort="shares.sort"
      :direction="shares.direction"
      :selection="{
        checked: shares.allSelected,
        partial: shares.someSelected,
        disabled: shares.stopping || shares.selecting || !shares.page?.total,
      }"
      :loading="shares.loading"
      :empty="!!shares.page && !shares.rows.length"
      :empty-text="shares.query ? t.emptySearch : t.noShares"
      :pagination="{
        size: shares.limit,
        total: shares.page?.total ?? state.shareCount,
        previous: shares.visiblePage?.previous,
        next: shares.visiblePage?.next,
        disabled: shares.loading,
      }"
      @sort="shares.changeSort"
      @toggle-all="shares.toggleAll"
      @page-size="shares.choosePageSize"
      @fit-page-size="shares.fitPageSize"
      @auto-page-size="shares.autoPageSize"
      @navigate="shares.navigate"
    >
      <template #toolbar>
        <TableToolbar
          v-model:query="shares.query"
          :min-search-length="minSearchLength"
          :search-label="t.searchShares"
          menu
          mobile-menu
          @menu="shares.menu = true"
        >
          <button
            class="sharing-button"
            :aria-label="t.stop"
            :disabled="shares.stopping || !shares.selected.size"
            @click="shares.confirming = [...shares.selected]"
          >
            <Icon name="stop" :size="18" />
            <span>{{ t.stop }}</span>
            <span v-if="shares.selected.size">({{ shares.selected.size }})</span>
          </button>
        </TableToolbar>
      </template>
      <template #before>
        <p v-if="shares.backgroundError && !systemNotice && !errorCode" class="notice" role="alert">
          {{ errorText(shares.backgroundError) }}
        </p>
      </template>
      <TableRow
        v-for="row in shares.rows"
        :key="row.id"
        :selection="!shares.compact"
        :selectable="!shares.compact"
        :selected="!shares.compact && shares.selected.has(row.id)"
        :disabled="shares.stopping"
        :label="row.name"
        @toggle="shares.toggle(row.id)"
      >
        <td class="app-cell">
          <div class="app-summary">
            <AppArtwork :src="row.icon" />
            <div class="share-info">
              <strong>{{ row.name }}</strong>
              <span v-if="row.count > 1" class="muted">{{ t.moreSharedFiles(row.count - 1) }}</span>
              <span v-if="state.mode.service" class="mobile-only app-meta">{{ row.owner }}</span>
              <span class="mobile-only app-meta">{{ date(row.createdAt) }}</span>
            </div>
          </div>
        </td>
        <td v-if="state.mode.service" class="share-owner">{{ row.owner }}</td>
        <td class="share-created" :title="shares.compact ? t.shareStarted : undefined">
          {{ date(row.createdAt) }}
        </td>
        <td class="share-view">
          <div class="share-buttons">
            <button
              class="icon-button"
              :aria-label="t.viewShare + ' — ' + row.name"
              :title="t.viewShare"
              @click.stop="emit('view', row)"
            >
              <Icon name="eye" :size="18" />
            </button>
            <button
              v-if="shares.compact"
              class="icon-button sharing-button"
              :disabled="shares.stopping"
              :aria-label="t.stop + ' — ' + row.name"
              :title="t.stop"
              @click.stop="shares.confirming = [row.id]"
            >
              <Icon name="stop" :size="18" />
            </button>
          </div>
        </td>
      </TableRow>
    </DataTable>
    <MessageDialog
      v-if="active && errorCode && !shares.menu && !shares.confirming"
      :code="errorCode"
      @close="errorCode = ''"
    />
    <Modal v-if="shares.menu" :title="t.more" @close="closeDialog">
      <div class="menu-actions">
        <p v-if="error" class="error" role="alert">{{ error }}</p>
        <SortOptions
          :columns="columns"
          :sort="shares.sort"
          :direction="shares.direction"
          @change="shares.changeSort"
        />
        <MenuButton
          icon="selectAll"
          :disabled="!shares.page?.total || shares.stopping || shares.selecting"
          @click="shares.selectAll"
        >
          {{ shares.query ? t.found : t.all }}
        </MenuButton>
        <MenuButton
          icon="deselectAll"
          :disabled="!shares.selected.size || shares.stopping || shares.selecting"
          @click="shares.clearSelection"
        >
          {{ t.clear }}
        </MenuButton>
      </div>
    </Modal>
    <ConfirmDialog
      v-if="shares.confirming"
      :title="shares.confirming.length === 1 ? t.confirmStopShare : t.confirmStopShares"
      :action="t.stop"
      :busy="shares.stopping"
      :error="errorCode"
      tone="sharing-button"
      icon="stop"
      @close="closeDialog"
      @confirm="shares.stop"
    >
      <p>{{ t.stopShareText }}</p>
      <p v-if="shares.confirming.length > 1">{{ t.stop }}: {{ shares.confirming.length }}</p>
    </ConfirmDialog>
  </section>
</template>

<style>
.shares-section {
  min-width: 0;
}
.shares-section .share-app {
  width: auto;
}
.shares-section .share-owner {
  width: 22%;
  overflow-wrap: anywhere;
}
.shares-section .share-created {
  width: 12rem;
}
.shares-section .share-view {
  width: calc(var(--control) + 2 * var(--s3));
}
.shares-section .share-buttons {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--s2);
}
.shares-section .compact .share-view {
  width: calc(2 * var(--control) + var(--s2) + 2 * var(--s3));
}
.shares-section .share-info {
  display: flex;
  flex-direction: column;
  min-width: 0;
  overflow-wrap: anywhere;
}
@media (max-width: 600px) {
  .shares-section .share-owner,
  .shares-section .share-created {
    display: none;
  }
  .shares-section .shares-table .app-cell {
    grid-column: 2;
    grid-row: 1;
  }
  .shares-section .share-view {
    grid-column: 3;
    grid-row: 1;
  }
  .shares-section .share-view {
    width: 3.5rem;
  }
  .shares-section .compact .share-view {
    width: auto;
  }
  .shares-section .actions button > span {
    display: none;
  }
}
</style>
