<script setup lang="ts">
import { t } from '../i18n/index.ts'
import Icon from './Icon.vue'

defineProps<{ checked: boolean; partial: boolean; disabled?: boolean }>()
defineEmits<{ toggle: [] }>()
</script>

<template>
  <button
    type="button"
    class="checkbox"
    :class="{ checked: checked || partial }"
    role="checkbox"
    :aria-checked="checked ? true : partial ? 'mixed' : false"
    :aria-label="checked ? t.clear : t.all"
    :disabled="disabled"
    @click="$emit('toggle')"
  >
    <Icon v-if="checked || partial" :name="checked ? 'check' : 'minus'" :size="14" />
  </button>
</template>
