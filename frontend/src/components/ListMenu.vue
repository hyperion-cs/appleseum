<script setup lang="ts">
import { nextTick, useTemplateRef, watch } from 'vue'
import { t } from '../i18n/index.ts'
import MenuButton from './MenuButton.vue'
import SortOptions from './SortOptions.vue'
import type { TableColumn } from './table.ts'

const props = defineProps<{
  columns: readonly TableColumn[]
  sort: string
  direction: string
  error: string
  busy: boolean
  searching: boolean
  rows: number
  selected: number
}>()
defineEmits<{ sort: [key: string]; selectAll: []; clear: [] }>()

const message = useTemplateRef<HTMLElement>('message')
watch(
  () => props.error,
  async (error) => {
    if (!error) return
    await nextTick()
    message.value?.scrollIntoView({ block: 'nearest' })
  },
)
</script>

<template>
  <div class="menu-actions">
    <p v-if="error" ref="message" class="error" role="alert">{{ error }}</p>
    <SortOptions
      :columns="columns"
      :sort="sort"
      :direction="direction"
      @change="$emit('sort', $event)"
    />
    <MenuButton
      icon="selectAll"
      class="mobile-only"
      :disabled="busy || !rows"
      @click="$emit('selectAll')"
    >
      {{ searching ? t.found : t.all }}
    </MenuButton>
    <MenuButton
      icon="deselectAll"
      class="mobile-only"
      :disabled="busy || !selected"
      @click="$emit('clear')"
    >
      {{ t.clear }}
    </MenuButton>
    <slot />
  </div>
</template>
