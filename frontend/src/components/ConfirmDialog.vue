<script setup lang="ts">
import { t } from '../i18n/index.ts'
import Modal from './Modal.vue'
import ConfirmationContent from './ConfirmationContent.vue'

defineProps<{
  title: string
  action: string
  error?: string
  busy?: boolean
  disabled?: boolean
  tone?: 'danger' | 'sharing-button'
  icon?: string
}>()
defineEmits<{ close: []; confirm: [] }>()
</script>

<template>
  <Modal :title="error ? t.actionFailed : title" :dismissible="!busy" @close="$emit('close')">
    <ConfirmationContent
      :action="action"
      :error="error"
      :busy="busy"
      :disabled="disabled"
      :tone="tone"
      :icon="icon"
      @close="$emit('close')"
      @confirm="$emit('confirm')"
    >
      <slot />
    </ConfirmationContent>
  </Modal>
</template>
