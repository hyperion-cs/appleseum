<script setup lang="ts">
import { t, errorText } from '../i18n/index.ts'

defineProps<{
  code?: string
  message?: string
  details?: readonly { name: string; code: string }[]
}>()
defineEmits<{ close: [] }>()
</script>

<template>
  <div class="message" role="alert">
    <p v-if="code">{{ errorText(code) }}</p>
    <p v-if="message">{{ message }}</p>
    <ul v-if="details?.length">
      <li v-for="(item, index) in details" :key="index">
        {{ item.name }}: {{ errorText(item.code) }}
      </li>
    </ul>
  </div>
  <div class="modal-actions">
    <button class="primary" @click="$emit('close')">{{ t.gotIt }}</button>
  </div>
</template>

<style scoped>
.message {
  font-size: var(--sm);
  overflow-wrap: anywhere;
}
</style>
