import { onMounted, onScopeDispose, ref, watch, type Ref, type WatchSource } from 'vue'
import { useMediaQuery, useResizeObserver } from '@vueuse/core'

export function useHeaderLayout(header: Ref<HTMLElement | undefined>, contents: WatchSource[]) {
  const sectionsInline = ref(false)
  const narrow = useMediaQuery('(max-width: 600px)')
  let headerFrame: number | undefined

  function schedule() {
    if (headerFrame !== undefined) return
    headerFrame = requestAnimationFrame(() => {
      headerFrame = undefined
      const root = header.value
      const tabs = root?.querySelector<HTMLElement>('.tabs')
      const brand = root?.querySelector<HTMLElement>('.brand')
      const controls = root?.querySelector<HTMLElement>('.preferences')
      if (!root || !tabs || !brand || !controls) return
      if (narrow.value) {
        sectionsInline.value = false
        const selected = tabs.querySelector<HTMLElement>('a.chosen')
        if (selected) {
          const menu = tabs.getBoundingClientRect()
          const item = selected.getBoundingClientRect()
          if (item.left < menu.left) tabs.scrollLeft += item.left - menu.left
          else if (item.right > menu.right) tabs.scrollLeft += item.right - menu.right
        }
        return
      }
      // Measure the menu's natural width even when it currently fills a separate row.
      tabs.classList.add('measuring-tabs')
      const menuWidth = tabs.getBoundingClientRect().width
      tabs.classList.remove('measuring-tabs')
      const gap = parseFloat(getComputedStyle(root).getPropertyValue('--header-gap'))
      const available =
        root.clientWidth -
        brand.getBoundingClientRect().width -
        controls.getBoundingClientRect().width -
        gap * 2
      sectionsInline.value = menuWidth <= available
    })
  }

  useResizeObserver(
    () =>
      header.value
        ? [header.value, ...header.value.querySelectorAll<HTMLElement>('.brand, .preferences')]
        : [],
    schedule,
  )
  onMounted(schedule)
  watch([...contents, narrow], schedule, { flush: 'post' })
  onScopeDispose(() => {
    if (headerFrame !== undefined) cancelAnimationFrame(headerFrame)
  })
  return sectionsInline
}
