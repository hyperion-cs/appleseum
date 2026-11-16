<script setup lang="ts" generic="T extends string | string[]">
import { computed, useId } from 'vue'
import ChoiceOptions from './ChoiceOptions.vue'
import Icon from './Icon.vue'
import Popover from './Popover.vue'

const props = defineProps<{
  id?: string
  label: string
  options: ReadonlyArray<{ value: string; label: string }>
  disabled?: boolean
  clearValue?: string
  summary?: string
}>()
const model = defineModel<T>({ required: true })
const valueId = useId()
const selected = computed(() => {
  const value = Array.isArray(model.value) ? (model.value[0] ?? props.clearValue) : model.value
  return props.summary ?? props.options.find((option) => option.value === value)?.label ?? value
})

// A single choice closes the list; multiple choices toggle and keep the options' order.
function choose(value: string, close: () => void) {
  if (!Array.isArray(model.value)) {
    model.value = value as T
    return close()
  }
  const chosen = new Set(value === props.clearValue ? [] : model.value)
  if (!chosen.delete(value) && value !== props.clearValue) chosen.add(value)
  model.value = props.options.map((option) => option.value).filter((item) => chosen.has(item)) as T
}
</script>

<template>
  <div class="choice-select">
    <Popover
      :id="id"
      class="choice-trigger"
      panel-class="choice-popover"
      :label="label"
      :width="280"
      :disabled="disabled"
      :aria-describedby="valueId"
    >
      <template #trigger>
        <span :id="valueId" class="choice-label">{{ selected }}</span>
        <Icon name="down" :size="18" />
      </template>
      <template #default="{ mobile, close }">
        <ChoiceOptions
          :model-value="model"
          :label="label"
          :options="options"
          :clear-value="clearValue"
          :mobile="mobile"
          @choose="choose($event, close)"
          @close="close()"
        />
      </template>
    </Popover>
  </div>
</template>

<style>
.choice-select {
  width: 100%;
  min-width: 0;
}
.choice-trigger {
  width: 100%;
  white-space: normal;
  justify-content: space-between;
  text-align: left;
  min-height: var(--control);
}
.choice-trigger > span {
  min-width: 0;
  flex: 1;
  overflow-wrap: anywhere;
}
.choice-trigger > svg {
  flex-shrink: 0;
  transition: transform var(--ease);
  color: var(--muted);
}
.choice-trigger[aria-expanded='true'] {
  border-color: var(--accent);
}
.choice-trigger[aria-expanded='true'] > svg {
  transform: rotate(180deg);
}
.choice-popover {
  position: fixed;
  z-index: 11;
  display: flex;
  flex-direction: column;
  padding: var(--s2);
  border: 1px solid var(--line);
  border-radius: var(--r2);
  background: var(--surface);
  box-shadow: var(--shadow);
  overflow: hidden;
  outline: none;
}
</style>
