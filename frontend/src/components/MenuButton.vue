<script setup lang="ts">
import Icon from './Icon.vue'

defineProps<{ icon?: string; busy?: boolean }>()
</script>

<template>
  <button type="button" class="menu-button" :aria-busy="busy || undefined">
    <span v-if="busy" class="spinner"></span>
    <Icon v-else-if="icon" :name="icon" :size="18" />
    <span class="menu-button-label"><slot /></span>
  </button>
</template>

<style scoped>
.menu-button {
  justify-content: center;
}
.menu-button > svg,
.menu-button > .spinner {
  flex-shrink: 0;
}
.menu-button-label {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--s2);
  min-width: 0;
  white-space: normal;
}
</style>
