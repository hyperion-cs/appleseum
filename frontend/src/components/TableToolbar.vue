<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { t } from '../i18n/index.ts'
import Icon from './Icon.vue'

const props = defineProps<{
  searchLabel: string
  menu?: boolean
  mobileMenu?: boolean
  searchClass?: string
  minSearchLength?: number
}>()
const query = defineModel<string>('query', { required: true })
const draft = ref(query.value)
const accepted = computed(() =>
  [...draft.value].length >= (props.minSearchLength ?? 0) ? draft.value : '',
)
watch(query, (value) => {
  if (value !== accepted.value) draft.value = value
})
watch(accepted, (value) => (query.value = value))
defineEmits<{ menu: [] }>()
</script>

<template>
  <div class="toolbar">
    <div class="actions"><slot /></div>
    <slot name="filters" />
    <div class="search-tools">
      <label class="search" :class="searchClass">
        <Icon name="search" :size="18" />
        <input
          v-model="draft"
          type="search"
          maxlength="200"
          :minlength="minSearchLength"
          :aria-label="searchLabel"
          :placeholder="minSearchLength ? t.searchPlaceholder : searchLabel"
        />
      </label>
      <button
        v-if="menu"
        class="icon-button"
        :class="{ 'mobile-only': mobileMenu }"
        :aria-label="t.more"
        @click="$emit('menu')"
      >
        <Icon name="more" />
      </button>
    </div>
  </div>
</template>
