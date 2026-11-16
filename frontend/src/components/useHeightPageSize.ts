import { onMounted, onUpdated, shallowRef, type Ref } from 'vue'
import { useEventListener, useResizeObserver, useTimeoutFn } from '@vueuse/core'

// Read actual row bounds after layout. This only removes rows; spare room never
// triggers a larger page or a network request.
export function useHeightPageSize(
  root: Ref<HTMLElement | undefined>,
  enabled: () => boolean,
  reduce: (size: number) => void,
) {
  const observed = shallowRef<HTMLElement[]>([])
  const { isPending, start } = useTimeoutFn(measure, 80, { immediate: false })

  function observe() {
    const element = root.value
    const next = element
      ? [
          element,
          element.closest<HTMLElement>('.shell') ?? element,
          ...element.querySelectorAll<HTMLElement>('tbody > tr'),
        ]
      : []
    if (
      next.length !== observed.value.length ||
      next.some((element, i) => element !== observed.value[i])
    )
      observed.value = next
  }

  function measure() {
    observe()
    const element = root.value
    if (!enabled() || !element?.getClientRects().length) return
    // A phone's keyboard should not permanently reduce the list's page size.
    if (
      document.activeElement?.matches('input, textarea, [contenteditable="true"]') &&
      (window.visualViewport?.height ?? innerHeight) < innerHeight * 0.8
    )
      return
    const rows = [...element.querySelectorAll<HTMLElement>('tbody > tr')]
    if (rows.length < 2) return
    let scroller: HTMLElement | null = element.parentElement
    while (scroller && !/(auto|scroll)/.test(getComputedStyle(scroller).overflowY))
      scroller = scroller.parentElement
    const viewportTop = scroller?.getBoundingClientRect().top ?? 0
    const scrollTop = scroller?.scrollTop ?? window.scrollY
    const height = Math.min(
      scroller?.clientHeight ?? innerHeight,
      window.visualViewport?.height ?? innerHeight,
    )
    const first = rows[0]!.getBoundingClientRect()
    const last = rows.at(-1)!.getBoundingClientRect()
    const boundary = element.closest('main') ?? element
    // Keep the controls below the rows visible; the site footer may scroll.
    const tail = Math.max(0, boundary.getBoundingClientRect().bottom - last.bottom)
    const available = height - (first.top - viewportTop + scrollTop) - tail - 2
    let count = 0
    for (const row of rows) {
      if (row.getBoundingClientRect().bottom - first.top > available) break
      count++
    }
    if (count < rows.length) reduce(Math.max(1, count))
  }

  function schedule() {
    if (!isPending.value) start()
  }

  useResizeObserver(observed, schedule)
  useEventListener(window, 'resize', schedule)
  useEventListener(window.visualViewport, 'resize', schedule)
  useEventListener(document, 'focusout', schedule)
  onMounted(schedule)
  onUpdated(schedule)
}
