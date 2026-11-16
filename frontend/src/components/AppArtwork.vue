<script setup lang="ts">
import { ref, watch } from 'vue'
import Icon from './Icon.vue'
const props = defineProps<{ src?: string }>()
const failed = ref(false)
watch(
  () => props.src,
  () => {
    failed.value = false
  },
)
</script>
<template>
  <span class="app-icon">
    <img
      v-if="src && !failed"
      :src="src"
      alt=""
      width="40"
      height="40"
      loading="lazy"
      decoding="async"
      @error="failed = true"
    />
    <Icon v-else name="app" :size="23" />
  </span>
</template>

<style>
.app-icon {
  height: var(--s10);
  width: var(--s10);
  background: var(--soft);
  border: 1px solid var(--subtle-line);
  border-radius: var(--r2);
  display: grid;
  place-items: center;
  flex-shrink: 0;
  color: var(--muted);
}
@media (max-width: 600px) {
  .app-icon {
    width: var(--s8);
    height: var(--s8);
    border-radius: var(--r1);
  }
}
.app-icon img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
  border-radius: inherit;
}
</style>
