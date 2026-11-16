<script setup lang="ts">
import { t } from '../i18n/index.ts'
import MessageContent from './MessageContent.vue'
import Icon from './Icon.vue'

withDefaults(
  defineProps<{
    action: string
    error?: string
    busy?: boolean
    disabled?: boolean
    tone?: 'danger' | 'sharing-button'
    icon?: string
  }>(),
  { tone: 'danger' },
)
defineEmits<{ close: []; confirm: [] }>()
</script>

<template>
  <MessageContent v-if="error" :code="error" @close="$emit('close')" />
  <template v-else>
    <slot />
    <div class="modal-actions">
      <button :disabled="busy" @click="$emit('close')">{{ t.cancel }}</button>
      <button
        :class="tone"
        :disabled="busy || disabled"
        :aria-busy="busy"
        @click="$emit('confirm')"
      >
        <span v-if="busy" class="spinner"></span>
        <Icon v-else-if="icon" :name="icon" :size="18" />
        {{ action }}
      </button>
    </div>
  </template>
</template>
