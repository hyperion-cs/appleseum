<script setup lang="ts">
import { ref } from 'vue'
import Modal from './Modal.vue'
import { t } from '../i18n/index.ts'
defineProps<{ label: string; text: string }>()
const open = ref(false)
</script>
<template>
  <button
    type="button"
    class="field-help"
    :aria-label="t.fieldHelp(label)"
    aria-haspopup="dialog"
    @click="open = true"
  >
    <span aria-hidden="true">?</span>
  </button>
  <Modal v-if="open" :title="label" @close="open = false">
    <p>{{ text }}</p>
  </Modal>
</template>

<style>
.field-help {
  width: var(--control);
  min-height: var(--control);
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--muted);
  flex-shrink: 0;
}
.field-help span {
  display: grid;
  place-items: center;
  width: var(--s5);
  height: var(--s5);
  border: 1px solid currentColor;
  border-radius: var(--pill);
  font-size: var(--sm);
}
</style>
