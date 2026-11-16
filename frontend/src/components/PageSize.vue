<script setup lang="ts">
import Icon from './Icon.vue'
import PageSizeOptions from './PageSizeOptions.vue'
import Popover from './Popover.vue'
import { t } from '../i18n/index.ts'

withDefaults(defineProps<{ modelValue: number; automatic?: boolean }>(), {
  automatic: undefined,
})
const emit = defineEmits<{ 'update:modelValue': [value: number]; automatic: [] }>()
</script>

<template>
  <div class="page-size">
    <span>{{ t.pageSize }}</span>
    <Popover
      class="page-size-button"
      panel-class="page-size-popover"
      scrollable
      :label="t.pageSize"
      :width="192"
    >
      <template #trigger>
        <span>{{ modelValue }}</span>
        <Icon name="down" :size="16" />
      </template>
      <template #default="{ mobile, close }">
        <PageSizeOptions
          :model-value="modelValue"
          :automatic="automatic"
          :autofocus="!mobile"
          @update:model-value="emit('update:modelValue', $event)"
          @automatic="(emit('automatic'), close())"
          @choose="(emit('update:modelValue', $event), close())"
        />
      </template>
    </Popover>
  </div>
</template>

<style>
.page-size {
  position: relative;
  display: flex;
  align-items: center;
  gap: var(--s2);
}
.page-size-button {
  min-width: 76px;
  min-height: var(--control);
  padding: var(--s2) var(--s3);
  gap: var(--s3);
  font-size: var(--sm);
  justify-content: space-between;
  font-variant-numeric: tabular-nums;
}
.page-size-popover {
  position: fixed;
  width: 192px;
  overflow-y: auto;
  z-index: 11;
  border: 1px solid var(--subtle-line);
  border-radius: var(--r2);
  background: var(--surface);
  box-shadow: var(--shadow);
  padding: var(--s1);
  outline: none;
}
</style>
