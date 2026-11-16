import { computed, ref, watch, type Ref } from 'vue'
import { onClickOutside, useMediaQuery } from '@vueuse/core'
import { autoUpdate, flip, hide, offset, shift, size, useFloating } from '@floating-ui/vue'

export function usePopover(
  trigger: Readonly<Ref<HTMLButtonElement | null>>,
  panel: Readonly<Ref<HTMLElement | null>>,
  width: number,
) {
  const gap = 8
  const open = ref(false)
  const mobile = useMediaQuery('(max-width: 600px), (hover: none) and (pointer: coarse)')
  const desktopOpen = computed(() => open.value && !mobile.value)
  const floating = useFloating(
    trigger,
    computed(() => (desktopOpen.value ? panel.value : null)),
    {
      open: desktopOpen,
      placement: 'bottom-start',
      strategy: 'fixed',
      whileElementsMounted: autoUpdate,
      middleware: [
        offset(gap),
        flip({ padding: gap }),
        shift({ padding: gap }),
        size({
          padding: gap,
          apply({ availableWidth, availableHeight, rects, elements }) {
            Object.assign(elements.floating.style, {
              width:
                Math.max(0, Math.min(Math.max(rects.reference.width, width), availableWidth)) +
                'px',
              maxHeight: Math.max(0, availableHeight) + 'px',
            })
          },
        }),
        hide(),
      ],
    },
  )

  function close(restoreFocus = true) {
    if (!open.value) return
    open.value = false
    if (restoreFocus) trigger.value?.focus({ preventScroll: true })
  }

  function blur(event: FocusEvent) {
    if (
      event.relatedTarget &&
      !panel.value?.contains(event.relatedTarget as Node) &&
      !trigger.value?.contains(event.relatedTarget as Node)
    )
      close(false)
  }

  function key(event: KeyboardEvent) {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      close()
    }
    // Restore the trigger before the browser advances to the next control.
    else if (event.key === 'Tab') close()
  }

  onClickOutside(
    panel,
    () => {
      if (!mobile.value) close(false)
    },
    { ignore: [trigger] },
  )
  watch(mobile, () => close())
  watch(
    () => floating.middlewareData.value.hide?.referenceHidden,
    (hidden) => {
      if (hidden) close(false)
    },
  )
  return {
    open,
    mobile,
    // Transparent, not hidden, until placed: the content takes focus as it mounts.
    position: computed(() => ({
      ...floating.floatingStyles.value,
      opacity: floating.isPositioned.value ? undefined : 0,
    })),
    close,
    blur,
    key,
    place: floating.update,
  }
}
