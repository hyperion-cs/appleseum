<script lang="ts">
// Only the top dialog owns Escape, Tab and back navigation.
const dialogs: string[] = []
const focusable =
  'button:not(:disabled),input:not(:disabled),textarea:not(:disabled),a[href],[tabindex="0"]'
const sheetMargin = 24
let bodyOverflow = ''
</script>
<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, useId } from 'vue'
import { useEventListener } from '@vueuse/core'
import Icon from './Icon.vue'
import { t } from '../i18n/index.ts'

const props = withDefaults(
  defineProps<{ title: string; sheet?: boolean; dismissible?: boolean }>(),
  { dismissible: true },
)
const emit = defineEmits<{ close: [] }>()
const root = ref<HTMLElement>()
const entry = useId()
// A sheet follows the visual viewport, so the on-screen keyboard cannot cover it.
const frame = ref<{ top: number; height: number }>()
let prior: Element | null = null

const isTop = () => dialogs.at(-1) === entry

function close() {
  if (props.dismissible) emit('close')
}

function fitViewport() {
  frame.value = {
    top: window.visualViewport?.offsetTop ?? 0,
    height: window.visualViewport?.height ?? innerHeight,
  }
}

function keepFocus(event: KeyboardEvent) {
  const dialog = root.value
  if (!dialog) return
  const items = [...dialog.querySelectorAll<HTMLElement>(focusable)].filter(
    (item) => item.getClientRects().length > 0,
  )
  const active = document.activeElement
  const edge = event.shiftKey ? items[0] : items.at(-1)
  if (items.length && dialog.contains(active) && active !== dialog && active !== edge) return
  event.preventDefault()
  ;((event.shiftKey ? items.at(-1) : items[0]) ?? dialog).focus()
}

function key(event: KeyboardEvent) {
  if (!isTop() || event.defaultPrevented) return
  if (event.key === 'Tab') keepFocus(event)
  else if (event.key === 'Escape') {
    event.preventDefault()
    close()
  }
}

// The dialog is a history step: "back" closes it, or stays put while it cannot close.
function pop() {
  if (!isTop() || history.state?.modal === entry) return
  if (props.dismissible) emit('close')
  else history.pushState({ ...history.state, modal: entry }, '')
}

useEventListener(document, 'keydown', key)
useEventListener(window, 'popstate', pop)
useEventListener(
  () => (props.sheet ? window.visualViewport : undefined),
  ['resize', 'scroll'],
  fitViewport,
)
useEventListener(() => (props.sheet ? window : undefined), 'resize', fitViewport)

onMounted(() => {
  if (!dialogs.length) {
    bodyOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
  }
  dialogs.push(entry)
  prior = document.activeElement
  history.pushState({ ...history.state, modal: entry }, '')
  root.value?.focus()
  if (props.sheet) fitViewport()
})
onBeforeUnmount(() => {
  const wasTop = isTop()
  if (dialogs.includes(entry)) dialogs.splice(dialogs.indexOf(entry), 1)
  if (!dialogs.length) document.body.style.overflow = bodyOverflow
  if (wasTop && prior instanceof HTMLElement && prior.isConnected)
    prior.focus({ preventScroll: true })
  if (history.state?.modal === entry) history.back()
})
</script>
<template>
  <Teleport to="body">
    <div
      class="overlay"
      :class="{ 'sheet-overlay': sheet }"
      :style="frame && { top: frame.top + 'px', height: frame.height + 'px', bottom: 'auto' }"
      @click.self="close"
    >
      <section
        ref="root"
        class="modal"
        :class="{ 'sheet-modal': sheet }"
        data-scrollbar
        :style="frame && { maxHeight: frame.height - sheetMargin + 'px' }"
        role="dialog"
        aria-modal="true"
        :aria-label="title"
        tabindex="-1"
      >
        <header class="modal-header">
          <h2>{{ title }}</h2>
          <button
            class="icon-button"
            :disabled="!dismissible"
            :aria-label="t.close"
            @click.stop="close"
          >
            <Icon name="close" />
          </button>
        </header>
        <div class="modal-body" :data-scrollbar="sheet ? '' : undefined"><slot /></div>
      </section>
    </div>
  </Teleport>
</template>

<style>
.overlay {
  position: fixed;
  inset: 0;
  background: var(--overlay);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--s5);
  z-index: 10;
}
.modal {
  width: 100%;
  max-width: var(--narrow);
  border: 1px solid var(--subtle-line);
  border-radius: var(--r3);
  background: var(--surface);
  box-shadow: var(--shadow);
  max-height: 90vh;
  max-height: 90dvh;
  overflow: auto;
  outline: none;
}
.modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--s4);
  padding: var(--s4) var(--s5);
  border-bottom: 1px solid var(--subtle-line);
}
.modal-header h2 {
  font-size: var(--lg);
}
.modal-body {
  padding: var(--s5);
}
.modal-body p {
  margin-top: 0;
  font-size: var(--sm);
}
.sheet-overlay {
  align-items: flex-end;
  padding: var(--s3);
}
.sheet-modal {
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.sheet-modal .modal-header {
  flex-shrink: 0;
}
.sheet-modal .modal-body {
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: auto;
}
.sheet-modal .choice-results {
  height: 50dvh;
  max-height: 50dvh;
}
</style>
