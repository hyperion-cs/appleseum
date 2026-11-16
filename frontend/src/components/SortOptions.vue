<script setup lang="ts">
import MenuButton from './MenuButton.vue'
import { t } from '../i18n/index.ts'
import type { TableColumn } from './table.ts'

defineProps<{
  columns: readonly TableColumn[]
  sort: string
  direction: string
}>()
defineEmits<{ change: [key: string] }>()
</script>

<template>
  <fieldset class="sort-options">
    <legend>{{ t.sort }}</legend>
    <MenuButton
      v-for="column in columns.filter((column) => column.sortable !== false)"
      :key="column.key"
      :class="{ chosen: sort === column.key }"
      @click="$emit('change', column.key)"
    >
      {{ column.label }}
      <span v-if="sort === column.key" class="sort-direction">
        {{ direction === 'asc' ? t.ascending : t.descending }}
      </span>
    </MenuButton>
  </fieldset>
</template>

<style scoped>
.sort-options {
  border: 0;
  padding: 0;
  margin: 0;
  display: none;
  flex-direction: column;
  gap: var(--s2);
}
.sort-options legend {
  color: var(--muted);
  font-size: var(--sm);
  padding: 0 0 var(--s2);
}
.sort-options .chosen {
  background: var(--selected);
}
.sort-direction {
  color: var(--muted);
  font-size: var(--xs);
}
@media (max-width: 600px) {
  .sort-options {
    display: flex;
  }
}
</style>
