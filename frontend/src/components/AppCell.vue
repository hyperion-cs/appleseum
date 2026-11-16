<script setup lang="ts">
import AppArtwork from './AppArtwork.vue'

defineProps<{ app: { name: string; icon: string } }>()
</script>

<template>
  <td class="app-cell">
    <div class="app-summary">
      <AppArtwork :src="app.icon" />
      <div class="app-info">
        <strong :title="app.name">{{ app.name }}</strong>
        <slot />
      </div>
    </div>
  </td>
</template>

<style>
.app-info {
  min-width: 0;
}
</style>
