<script setup lang="ts">
import { computed, nextTick, onMounted, ref, useId, watch } from 'vue'
import { useResizeObserver } from '@vueuse/core'
import Icon from './Icon.vue'
import { t } from '../i18n/index.ts'

const props = defineProps<{
  modelValue: string | string[]
  label: string
  options: ReadonlyArray<{ value: string; label: string }>
  mobile: boolean
  clearValue?: string
}>()
const emit = defineEmits<{ choose: [value: string]; close: [] }>()
const query = ref('')
const active = ref(0)
const search = ref<HTMLInputElement>()
const list = ref<HTMLElement>()
const height = ref<string>()
const id = useId()
const searchable = computed(() => props.options.length > 10)

const normalize = (value: string) =>
  value
    .normalize('NFKD')
    .replace(/\p{Diacritic}/gu, '')
    .toLowerCase()

const indexed = computed(() =>
  props.options.map((option) => ({
    ...option,
    text: normalize(option.label + ' ' + option.value),
  })),
)
const filtered = computed(() => {
  const terms = normalize(query.value).trim().split(/\s+/)
  return indexed.value.filter((option) => terms.every((term) => option.text.includes(term)))
})
const activeId = computed(() =>
  filtered.value[active.value] ? `${id}-${active.value}` : undefined,
)

async function reveal() {
  await nextTick()
  const item = list.value?.children[active.value] as HTMLElement | undefined
  if (!item || !list.value) return
  const bounds = item.getBoundingClientRect()
  const viewport = list.value.getBoundingClientRect()
  if (bounds.top < viewport.top) list.value.scrollTop -= viewport.top - bounds.top
  else if (bounds.bottom > viewport.bottom) list.value.scrollTop += bounds.bottom - viewport.bottom
}

function selected(value: string) {
  return Array.isArray(props.modelValue)
    ? props.modelValue.includes(value) || (!props.modelValue.length && value === props.clearValue)
    : value === props.modelValue
}

function activate(index: number) {
  active.value = Math.max(0, Math.min(filtered.value.length - 1, index))
  void reveal()
}

function activateSelected() {
  activate(filtered.value.findIndex((option) => selected(option.value)))
}

watch(filtered, activateSelected, { immediate: true })
watch(
  () => props.modelValue,
  () => {
    // Toggling one of several choices keeps the keyboard position.
    if (!Array.isArray(props.modelValue)) activateSelected()
  },
)

function key(event: KeyboardEvent) {
  if (event.isComposing) return
  const typing = event.target === search.value
  if (event.key === 'Escape') {
    event.stopPropagation()
    emit('close')
  } else if (event.key === 'ArrowDown') activate(active.value + 1)
  else if (event.key === 'ArrowUp') activate(active.value - 1)
  else if (event.key === 'Home' && !typing) activate(0)
  else if (event.key === 'End' && !typing) activate(filtered.value.length - 1)
  else if (event.key === 'Enter' || (event.key === ' ' && !typing)) {
    const option = filtered.value[active.value]
    if (option) emit('choose', option.value)
  } else return
  event.preventDefault()
}

// The popover sizes the list after it mounts; keep the active option in view.
useResizeObserver(list, () => void reveal())
onMounted(async () => {
  await nextTick()
  // Searching narrows the list; the popover keeps the size it opened with.
  if (!props.mobile && list.value) height.value = list.value.offsetHeight + 'px'
  ;(!props.mobile && searchable.value ? search.value : list.value)?.focus({ preventScroll: true })
  void reveal()
})
</script>
<template>
  <div class="choice-options" @keydown="key">
    <label v-if="searchable" class="choice-search">
      <Icon name="search" :size="18" />
      <input
        ref="search"
        v-model="query"
        type="search"
        role="combobox"
        autocomplete="off"
        :aria-label="t.choiceSearch(label)"
        :placeholder="t.choiceSearchPlaceholder"
        aria-autocomplete="list"
        aria-expanded="true"
        :aria-controls="id"
        :aria-activedescendant="activeId"
      />
    </label>
    <ul
      ref="list"
      :id="id"
      role="listbox"
      :aria-label="label"
      :aria-multiselectable="Array.isArray(modelValue) ? true : undefined"
      tabindex="0"
      :aria-activedescendant="activeId"
      class="choice-results"
      data-scrollbar
      :class="{ multiple: Array.isArray(modelValue) }"
      :style="{ height }"
    >
      <li
        v-for="(option, index) in filtered"
        :id="`${id}-${index}`"
        :key="option.value"
        role="option"
        :aria-selected="selected(option.value)"
        :class="{ active: index === active }"
        @click="emit('choose', option.value)"
        @pointermove="active = index"
      >
        <span class="choice-label">{{ option.label }}</span>
        <template v-if="selected(option.value)">
          <span v-if="Array.isArray(modelValue)" class="choice-dot" aria-hidden="true"></span>
          <Icon v-else name="check" :size="18" />
        </template>
      </li>
      <li v-if="!filtered.length" class="choice-empty" role="presentation">
        <span role="status">{{ t.emptySearch }}</span>
      </li>
    </ul>
  </div>
</template>

<style>
.choice-options {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
  min-width: 0;
}
.choice-search {
  display: flex;
  align-items: center;
  gap: var(--s2);
  border: 1px solid var(--line);
  border-radius: var(--r2);
  padding: var(--s2) var(--s3);
  min-height: var(--control);
  flex-shrink: 0;
  margin-bottom: var(--s2);
  color: var(--muted);
}
.choice-search:focus-within {
  border-color: var(--accent);
}
.choice-search input {
  min-width: 0;
  width: 100%;
  border: 0;
  outline: 0;
  margin: 0;
  padding: 0;
  background: transparent;
  color: var(--text);
  font-size: var(--md);
}
.choice-results {
  list-style: none;
  margin: 0;
  padding: 0;
  overflow-y: auto;
  overscroll-behavior: contain;
  min-height: 0;
  max-height: 320px;
}
.choice-results:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
  border-radius: var(--r2);
}
.choice-results > li {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--s3);
  min-height: var(--control);
  padding: var(--s2) var(--s3);
  border-radius: var(--r2);
  cursor: pointer;
  overflow-wrap: anywhere;
}
.choice-results > li > span {
  min-width: 0;
}
.choice-results > li > svg {
  flex-shrink: 0;
}
.choice-results > li.active {
  background: var(--soft);
}
.choice-results > li[aria-selected='true'] {
  color: var(--accent);
  background: var(--selected);
}
.choice-results.multiple > li {
  border-radius: var(--r1);
}
.choice-results.multiple > li + li {
  margin-top: var(--s1);
}
.choice-results.multiple > li[aria-selected='true'] {
  background: transparent;
}
.choice-results.multiple > li.active {
  background: var(--soft);
}
.choice-dot {
  width: 8px;
  height: 8px;
  margin-inline: 5px;
  border-radius: 50%;
  background: currentColor;
  flex-shrink: 0;
}
.choice-results > .choice-empty {
  color: var(--muted);
  padding: var(--s3);
  cursor: default;
}
</style>
