<script setup lang="ts">
import Icon from './Icon.vue'

defineProps<{
  selection?: boolean
  selectable?: boolean
  selected?: boolean
  disabled?: boolean
  label?: string
}>()
defineEmits<{ toggle: [] }>()
</script>

<template>
  <tr :class="{ selected, selectable }" @click="selectable && !disabled && $emit('toggle')">
    <td v-if="selection" class="check-col">
      <button
        v-if="selectable"
        class="checkbox"
        role="checkbox"
        :aria-label="label"
        :aria-checked="!!selected"
        :class="{ checked: selected }"
        :disabled="disabled"
        @click.stop="$emit('toggle')"
      >
        <Icon v-if="selected" name="check" :size="14" />
      </button>
    </td>
    <slot />
  </tr>
</template>
