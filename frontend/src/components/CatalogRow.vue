<script setup lang="ts">
import type { CatalogItem } from '../api/types.ts'
import { bytes, purchaseDate } from '../i18n/format.ts'
import { t } from '../i18n/index.ts'
import AppCell from './AppCell.vue'
import Icon from './Icon.vue'
import TableRow from './TableRow.vue'

defineProps<{ row: CatalogItem; marked: boolean; busy: boolean; showSize: boolean }>()
defineEmits<{ toggle: [] }>()
</script>

<template>
  <TableRow
    selection
    :selectable="!row.inLibrary || row.updateAvailable"
    :selected="marked"
    :disabled="busy"
    :label="row.name"
    @toggle="$emit('toggle')"
  >
    <AppCell :app="row">
      <span class="app-meta app-id">
        ID: {{ row.id }}
        <span v-if="row.purchasedAt" class="mobile-only">
          · {{ purchaseDate(row.purchasedAt) }}
        </span>
      </span>
    </AppCell>
    <td class="status-cell">
      <div class="available-badges">
        <span class="badge" :class="{ warning: row.delisted }">
          {{ row.delisted ? t.delisted : t.listed }}
        </span>
        <span v-if="row.updateAvailable" class="badge warning">{{ t.updateAvailable }}</span>
        <span
          v-if="row.inLibrary"
          class="badge downloaded-badge"
          :title="t.downloaded"
          :aria-label="t.downloaded"
        >
          <Icon name="download" :size="14" />
          {{ t.downloaded }}
        </span>
      </div>
    </td>
    <td v-if="showSize" class="detail-cell size-cell">
      <span v-if="row.size != null">{{ bytes(row.size) }}</span>
      <span v-else :title="row.delisted ? t.delistedSizeUnknown : t.unknownSize">
        {{ t.noValue }}
      </span>
    </td>
    <td class="detail-cell purchased-cell">
      {{ purchaseDate(row.purchasedAt) }}
    </td>
  </TableRow>
</template>

<style>
.app-id {
  display: block;
  font-variant-numeric: tabular-nums;
  user-select: text;
}
.available-badges {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--s1);
}
.badge.downloaded-badge {
  align-items: center;
  background: var(--selected);
  color: var(--accent);
  gap: var(--s1);
}
@media (max-width: 600px) {
  /* Component styles load before the global ones, so the warning badge is named too. */
  .available-badges .badge,
  .available-badges .badge.warning {
    padding: 0;
    background: transparent;
  }
}
</style>
