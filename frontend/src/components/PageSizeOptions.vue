<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import Icon from './Icon.vue'
import { t } from '../i18n/index.ts'
import { maxPageSize } from '../api/types.ts'

const presets = [5, 10, 20, 50, 100]
const props = withDefaults(
  defineProps<{ modelValue: number; automatic?: boolean; autofocus?: boolean }>(),
  { automatic: undefined },
)
const emit = defineEmits<{
  'update:modelValue': [value: number]
  choose: [value: number]
  automatic: []
}>()
const root = ref<HTMLElement>()
const values = computed(() => [...new Set([...presets, props.modelValue])].sort((a, b) => a - b))

async function focusSelected(options?: FocusOptions) {
  await nextTick()
  root.value?.querySelector<HTMLElement>('[aria-selected="true"]')?.focus(options)
}

function step(delta: number) {
  const value = Math.max(1, Math.min(maxPageSize, props.modelValue + delta))
  if (value !== props.modelValue) emit('update:modelValue', value)
}

function key(event: KeyboardEvent) {
  if (['ArrowLeft', '-', 'ArrowRight', '+'].includes(event.key)) {
    event.preventDefault()
    step(event.key === 'ArrowLeft' || event.key === '-' ? -1 : 1)
    // The stepped value becomes the selected option; keep the focus on it.
    if ((event.target as HTMLElement).getAttribute('role') === 'option') void focusSelected()
  } else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault()
    const options = [...(root.value?.querySelectorAll<HTMLElement>('[role="option"]') ?? [])]
    const focused = options.indexOf(document.activeElement as HTMLElement)
    const next =
      focused < 0
        ? options.findIndex((option) => option.getAttribute('aria-selected') === 'true')
        : (focused + (event.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length
    options[next]?.focus()
  }
}

onMounted(() => props.autofocus && focusSelected({ preventScroll: true }))
</script>
<template>
  <div ref="root" class="page-size-options" @keydown="key">
    <div class="page-size-stepper">
      <button
        class="icon-button"
        :disabled="modelValue <= 1"
        :aria-label="t.fewerRows"
        @click="step(-1)"
      >
        <Icon name="minus" :size="18" />
      </button>
      <output aria-live="polite">{{ modelValue }}</output>
      <button
        class="icon-button"
        :disabled="modelValue >= maxPageSize"
        :aria-label="t.moreRows"
        @click="step(1)"
      >
        <Icon name="plus" :size="18" />
      </button>
    </div>
    <div
      role="listbox"
      :aria-label="t.pageSize"
      class="page-size-values"
      :style="automatic !== undefined ? { '--page-size-options': 7 } : undefined"
    >
      <button
        v-if="automatic !== undefined"
        role="option"
        :aria-selected="automatic"
        :tabindex="automatic ? 0 : -1"
        @click="emit('automatic')"
      >
        <span>{{ t.fitRows }}</span>
        <span v-if="automatic" class="page-size-dot" aria-hidden="true"></span>
      </button>
      <button
        v-for="value in values"
        :key="value"
        role="option"
        :aria-selected="!automatic && value === modelValue"
        :tabindex="!automatic && value === modelValue ? 0 : -1"
        @click="emit('choose', value)"
      >
        <span>{{ value }}</span>
        <span
          v-if="!automatic && value === modelValue"
          class="page-size-dot"
          aria-hidden="true"
        ></span>
      </button>
    </div>
  </div>
</template>

<style>
.page-size-options {
  --page-size-row: var(--control);
}
.page-size-stepper {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--s2);
  padding-bottom: var(--s1);
  margin-bottom: var(--s1);
  border-bottom: 1px solid var(--subtle-line);
}
.page-size-stepper button {
  width: var(--page-size-row);
  min-height: var(--page-size-row);
  padding: var(--s1);
  border-color: transparent;
  background: transparent;
}
.page-size-stepper output {
  font-weight: var(--bold);
  font-variant-numeric: tabular-nums;
  font-size: var(--md);
  color: var(--text);
}
.page-size-values {
  display: flex;
  flex-direction: column;
}
.page-size-values button {
  min-height: var(--page-size-row);
  padding: var(--s1) var(--s3);
  font-size: var(--sm);
  border-radius: var(--r1);
  justify-content: space-between;
  width: 100%;
  border-color: transparent;
  background: transparent;
  font-weight: var(--normal);
  font-variant-numeric: tabular-nums;
}
.page-size-values button:focus-visible {
  outline-offset: -2px;
}
.page-size-dot {
  width: 6px;
  height: 6px;
  border-radius: var(--pill);
  background: var(--accent);
}
@media (max-width: 600px) {
  .page-size-values {
    /* Reserve the custom value so stepping cannot move the sheet's controls. */
    min-height: calc(var(--page-size-options, 6) * var(--page-size-row));
  }
  .page-size-dot {
    width: var(--s2);
    height: var(--s2);
  }
}
</style>
