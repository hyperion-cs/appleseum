<script setup lang="ts">
defineProps<{ label: string }>()
const on = defineModel<boolean>({ required: true })
</script>

<template>
  <label class="toggle-label">
    {{ label }}
    <button
      class="switch"
      :class="{ on }"
      role="switch"
      :aria-label="label"
      :aria-checked="on"
      @click="on = !on"
    >
      <span></span>
    </button>
  </label>
</template>

<style>
.toggle-label {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: var(--sm);
  gap: var(--s3);
}
.switch {
  width: 46px;
  height: 28px;
  min-height: 28px;
  border-radius: var(--pill);
  padding: 3px;
  background: var(--soft);
  justify-content: flex-start;
}
.switch span {
  width: 20px;
  height: 20px;
  border-radius: var(--pill);
  background: var(--muted);
  transition: transform var(--ease);
}
.switch.on {
  background: var(--primary);
  border-color: var(--primary);
}
.switch.on span {
  background: var(--on-primary);
  transform: translateX(17px);
}
</style>
