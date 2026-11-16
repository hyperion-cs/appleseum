<script setup lang="ts">
import { t } from '../i18n/index.ts'
import Modal from './Modal.vue'
import MessageContent from './MessageContent.vue'

defineProps<{
  code?: string
  message?: string
  details?: readonly { name: string; code: string }[]
}>()
defineEmits<{ close: [] }>()
</script>

<template>
  <Modal
    :title="code === 'sharing_duplicate' ? t.shareAlreadyStarted : t.actionFailed"
    @close="$emit('close')"
  >
    <MessageContent :code="code" :message="message" :details="details" @close="$emit('close')" />
  </Modal>
</template>
