<script setup lang="ts" generic="T extends string">
import Icon from './Icon.vue'

const model = defineModel<T>({ required: true })
defineProps<{
  label: string
  options: readonly { value: T; label: string; icon?: string; count?: string }[]
}>()
</script>

<template>
  <div class="segmented-control" role="group" :aria-label="label">
    <button
      v-for="option in options"
      :key="option.value"
      type="button"
      :aria-pressed="model === option.value"
      @click="model = option.value"
    >
      <Icon v-if="option.icon" :name="option.icon" :size="18" />
      {{ option.label }}
      <span v-if="option.count !== undefined">· {{ option.count }}</span>
    </button>
  </div>
</template>

<style scoped>
.segmented-control {
  display: grid;
  grid-auto-flow: column;
  grid-auto-columns: 1fr;
  width: max-content;
  gap: var(--s1);
  padding: var(--s1);
  border-radius: var(--r2);
  background: var(--navigation-bg);
  max-width: 100%;
  font-size: var(--tab-font-size);
}
.segmented-control > button {
  min-height: var(--s8);
  padding: var(--s1) var(--s4);
  min-width: 0;
  border-color: transparent;
  background: transparent;
}
.segmented-control > button[aria-pressed='true'] {
  background: var(--segment-active);
  color: var(--on-segment-active);
}
@media (max-width: 600px) {
  .segmented-control {
    grid-auto-columns: auto;
    width: auto;
  }
}
</style>
