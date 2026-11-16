<script setup lang="ts">
import { onUpdated, useId, useTemplateRef, watch } from 'vue'
import Modal from './Modal.vue'
import { usePopover } from './usePopover.ts'

// A button whose content opens next to it, or as a bottom sheet on touch and narrow screens.
defineOptions({ inheritAttrs: false })
const props = defineProps<{
  label: string
  width: number
  panelClass: string
  disabled?: boolean
  // The panel itself scrolls and shows the app's scrollbar.
  scrollable?: boolean
}>()
const trigger = useTemplateRef<HTMLButtonElement>('trigger')
const panel = useTemplateRef<HTMLElement>('panel')
const { open, mobile, position, close, blur, key, place } = usePopover(trigger, panel, props.width)
const id = useId()

watch(
  () => props.disabled,
  (disabled) => {
    if (disabled) close()
  },
)
onUpdated(place)
</script>

<template>
  <button
    ref="trigger"
    type="button"
    v-bind="$attrs"
    :disabled="disabled"
    :aria-label="label"
    aria-haspopup="dialog"
    :aria-expanded="open"
    :aria-controls="open && !mobile ? id : undefined"
    @click="open = !open"
    @keydown.down.prevent="open = true"
    @keydown.up.prevent="open = true"
  >
    <slot name="trigger" />
  </button>
  <Modal v-if="open && mobile" :title="label" sheet @close="close()">
    <slot :mobile="true" :close="close" />
  </Modal>
  <Teleport to="body">
    <div
      v-if="open && !mobile"
      ref="panel"
      :id="id"
      :class="panelClass"
      data-popover
      :data-scrollbar="scrollable ? '' : undefined"
      role="dialog"
      :aria-label="label"
      tabindex="-1"
      :style="position"
      @keydown="key"
      @focusout="blur"
    >
      <slot :mobile="false" :close="close" />
    </div>
  </Teleport>
</template>
