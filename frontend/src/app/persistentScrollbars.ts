import { shallowRef } from 'vue'
import { useEventListener, useMutationObserver, useResizeObserver } from '@vueuse/core'

// Scrolling elements opt in with data-scrollbar; floating panels mark themselves data-popover.
const selector = '[data-scrollbar]'

export function scrollbarGeometry(view: number, content: number, offset: number, track: number) {
  if (view <= 0 || content <= view || track <= 0) return { length: Math.max(0, track), position: 0 }
  const length = Math.min(track, Math.max(24, (view / content) * track))
  const position =
    (Math.max(0, Math.min(content - view, offset)) / (content - view)) * (track - length)
  return { length, position }
}

// Keep scroll indicators visible without replacing native content scrolling.
export function installPersistentScrollbars() {
  // Keep Safari's viewport stationary to suppress its separate scroll indicator.
  const page = document.getElementById('app')!
  const previousTabIndex = page.getAttribute('tabindex')
  page.tabIndex = -1
  document.documentElement.classList.add('persistent-scroll-root')
  document.body.classList.add('persistent-scroll-native')
  const layer = document.createElement('div')
  layer.className = 'persistent-scrollbars'
  layer.setAttribute('aria-hidden', 'true')
  document.body.append(layer)
  type Bar = { track: HTMLDivElement; thumb: HTMLSpanElement }
  const entries = new Map<HTMLElement, { vertical: Bar; horizontal: Bar }>()
  let frame = 0
  let stopped = false
  let discover = true
  const observed = shallowRef<(HTMLElement | SVGElement)[]>([])
  useResizeObserver(observed, () => schedule())

  function schedule(scan = false) {
    discover ||= scan
    if (!frame && !stopped) frame = requestAnimationFrame(update)
  }

  function makeBar(element: HTMLElement, horizontal: boolean): Bar {
    const track = document.createElement('div')
    track.className = 'persistent-scrollbar ' + (horizontal ? 'horizontal' : 'vertical')
    const thumb = document.createElement('span')
    track.append(thumb)
    layer.append(track)
    track.addEventListener('pointerdown', (event) => {
      event.preventDefault()
      track.setPointerCapture(event.pointerId)
      const rect = track.getBoundingClientRect()
      const size = horizontal ? rect.width : rect.height
      const view = horizontal ? element.clientWidth : element.clientHeight
      const content = horizontal ? element.scrollWidth : element.scrollHeight
      const thumbRect = thumb.getBoundingClientRect()
      const thumbLength = horizontal ? thumbRect.width : thumbRect.height
      const start = horizontal ? rect.left : rect.top
      const point = horizontal ? event.clientX : event.clientY
      const grab =
        event.target === thumb
          ? point - (horizontal ? thumbRect.left : thumbRect.top)
          : thumbLength / 2

      function move(event: PointerEvent) {
        const point = (horizontal ? event.clientX : event.clientY) - start - grab
        const fraction = Math.max(0, Math.min(1, point / Math.max(1, size - thumbLength)))
        if (horizontal) element.scrollLeft = fraction * (content - view)
        else element.scrollTop = fraction * (content - view)
        schedule()
      }

      function end() {
        track.removeEventListener('pointermove', move)
        track.removeEventListener('lostpointercapture', end)
      }

      track.addEventListener('pointermove', move)
      track.addEventListener('lostpointercapture', end)
      if (event.target !== thumb) move(event)
    })
    track.addEventListener(
      'wheel',
      (event) => {
        event.preventDefault()
        const scale = event.deltaMode === 1 ? 16 : event.deltaMode === 2 ? element.clientHeight : 1
        if (horizontal) element.scrollLeft += (event.deltaX || event.deltaY) * scale
        else element.scrollTop += event.deltaY * scale
      },
      { passive: false },
    )
    return { track, thumb }
  }

  function update() {
    frame = 0
    if (discover) {
      discover = false
      const current = new Set<HTMLElement>([
        page,
        ...document.querySelectorAll<HTMLElement>(selector),
      ])
      for (const [element, bars] of entries) {
        if (current.has(element) && element.isConnected) continue
        bars.vertical.track.remove()
        bars.horizontal.track.remove()
        entries.delete(element)
        element.classList.remove('persistent-scroll-native')
      }
      const next: (HTMLElement | SVGElement)[] = []
      for (const element of current) {
        if (!entries.has(element)) {
          entries.set(element, {
            vertical: makeBar(element, false),
            horizontal: makeBar(element, true),
          })
          element.classList.add('persistent-scroll-native')
        }
        next.push(element, ...element.querySelectorAll<HTMLElement | SVGElement>(':scope > *'))
      }
      if (
        next.length !== observed.value.length ||
        next.some((element, i) => element !== observed.value[i])
      )
        observed.value = next
    }
    const viewport = window.visualViewport
    const scale = viewport?.scale || 1
    // Absolute coordinates avoid Safari shifting fixed elements again while panning.
    const origin = layer.getBoundingClientRect()
    const left = viewport?.offsetLeft ?? 0
    const top = viewport?.offsetTop ?? 0
    const right = left + (viewport?.width ?? innerWidth)
    const bottom = top + (viewport?.height ?? innerHeight)
    const modal = [...document.querySelectorAll('.overlay')].at(-1)
    document.documentElement.classList.toggle(
      'persistent-scroll-locked',
      document.body.style.overflow === 'hidden',
    )
    for (const [element, bars] of entries) {
      const style = getComputedStyle(element)
      const rect = element.getBoundingClientRect()
      const popup = element.closest('[data-popover]')
      const visible =
        element.getClientRects().length > 0 && (!modal || modal.contains(element) || popup)
      let x1 = Math.max(left, rect.left)
      let y1 = Math.max(top, rect.top)
      let x2 = Math.min(right, rect.right)
      let y2 = Math.min(bottom, rect.bottom)
      // Clip nested indicators to the same ancestors that clip their content.
      for (
        let parent = element.parentElement;
        parent && parent !== document.body;
        parent = parent.parentElement
      ) {
        const clipping = getComputedStyle(parent)
        const bounds = parent.getBoundingClientRect()
        if (/(auto|scroll|hidden|clip)/.test(clipping.overflowX)) {
          x1 = Math.max(x1, bounds.left)
          x2 = Math.min(x2, bounds.right)
        }
        if (/(auto|scroll|hidden|clip)/.test(clipping.overflowY)) {
          y1 = Math.max(y1, bounds.top)
          y2 = Math.min(y2, bounds.bottom)
        }
      }
      for (const horizontal of [false, true]) {
        const bar = horizontal ? bars.horizontal : bars.vertical
        const view = horizontal ? element.clientWidth : element.clientHeight
        const content = horizontal ? element.scrollWidth : element.scrollHeight
        const overflow = horizontal ? style.overflowX : style.overflowY
        const length = (horizontal ? x2 - x1 : y2 - y1) * scale - 16
        const show = visible && /auto|scroll/.test(overflow) && content > view + 1 && length > 24
        bar.track.hidden = !show
        if (!show) continue
        const offset = horizontal ? element.scrollLeft : element.scrollTop
        const geometry = scrollbarGeometry(view, content, offset, length)
        Object.assign(bar.track.style, {
          left: (horizontal ? x1 + 8 / scale : x2 - 11 / scale) - origin.left + 'px',
          top: (horizontal ? y2 - 11 / scale : y1 + 8 / scale) - origin.top + 'px',
          transform: `scale(${1 / scale})`,
          width: horizontal ? length + 'px' : '10px',
          height: horizontal ? '10px' : length + 'px',
          zIndex: popup ? '12' : modal?.contains(element) ? '11' : '5',
        })
        Object.assign(
          bar.thumb.style,
          horizontal
            ? { width: geometry.length + 'px', left: geometry.position + 'px' }
            : { height: geometry.length + 'px', top: geometry.position + 'px' },
        )
      }
    }
  }

  useMutationObserver(
    document.body,
    (records) => {
      if (records.some((record) => !layer.contains(record.target))) schedule(true)
    },
    {
      childList: true,
      subtree: true,
      characterData: true,
      attributes: true,
      attributeFilter: ['class', 'style', 'hidden', 'open'],
    },
  )

  const scroll = () => schedule()

  const layout = () => schedule(true)

  function key(event: KeyboardEvent) {
    if (
      event.defaultPrevented ||
      event.altKey ||
      event.ctrlKey ||
      event.metaKey ||
      document.body.style.overflow === 'hidden'
    )
      return
    if (
      document.activeElement !== document.body &&
      document.activeElement !== document.documentElement
    )
      return
    const steps: Record<string, number> = {
      ArrowDown: 40,
      ArrowUp: -40,
      PageDown: page.clientHeight * 0.9,
      PageUp: -page.clientHeight * 0.9,
      Home: -page.scrollHeight,
      End: page.scrollHeight,
      ' ': page.clientHeight * (event.shiftKey ? -0.9 : 0.9),
    }
    if (steps[event.key] === undefined) return
    event.preventDefault()
    page.scrollTop += steps[event.key]!
  }

  useEventListener(document, 'scroll', scroll, true)
  useEventListener(document, 'keydown', key)
  useEventListener(window, 'resize', layout)
  useEventListener(window.visualViewport, 'resize', layout)
  useEventListener(window.visualViewport, 'scroll', scroll)
  schedule(true)
  void document.fonts.ready.then(() => schedule(true))
  return () => {
    stopped = true
    cancelAnimationFrame(frame)
    for (const element of entries.keys()) element.classList.remove('persistent-scroll-native')
    document.body.classList.remove('persistent-scroll-native')
    document.documentElement.classList.remove('persistent-scroll-root', 'persistent-scroll-locked')
    if (previousTabIndex === null) page.removeAttribute('tabindex')
    else page.setAttribute('tabindex', previousTabIndex)
    layer.remove()
  }
}
