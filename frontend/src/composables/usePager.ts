import { computed, ref, type Ref } from 'vue'
import {
  pageCursor,
  pageMove,
  shortenPage,
  type CursorPage,
  type PageDirection,
} from '../api/pagination.ts'
import type { PageSizeResource } from './pageSizeStorage.ts'
import { usePageSizing } from './usePageSizing.ts'

// Page size and cursor position of one table. Fitting a page to the window only hides
// loaded rows: the last visible row's cursor continues to the hidden ones.
export function usePager<P extends CursorPage>(
  resource: () => PageSizeResource,
  page: Readonly<Ref<P | null>>,
  reload: () => void,
  extraRows: () => number = () => 0,
) {
  const sizing = usePageSizing(resource)
  const { limit, automatic } = sizing
  const cursor = ref('')
  const direction = ref<PageDirection>('next')
  let loaded = limit.value
  const visible = computed(
    () =>
      page.value &&
      (automatic.value
        ? shortenPage(page.value, Math.max(1, limit.value - extraRows()))
        : page.value),
  )

  // A refresh repeats the loaded request, so a fitted page keeps its size and boundary.
  function query(filters: URLSearchParams, refresh = false) {
    if (!refresh || !page.value) loaded = limit.value
    filters.set('limit', String(loaded))
    if (cursor.value) {
      filters.set('cursor', cursor.value)
      filters.set('move', pageMove(direction.value))
    }
    return filters.toString()
  }

  function step(to: PageDirection) {
    if (!visible.value?.[to]) return false
    cursor.value = pageCursor(visible.value, to)
    direction.value = to
    return true
  }

  function fitPageSize(rows: number) {
    // Rows shown above the server page do not move its boundary off a server row.
    const size = Math.max(1 + extraRows(), rows)
    if (page.value?.items[size - extraRows() - 1]?.cursor) sizing.fitPageSize(size)
  }

  function restart() {
    cursor.value = ''
    reload()
  }

  function choosePageSize(size: number) {
    sizing.choosePageSize(size)
    restart()
  }

  function autoPageSize() {
    sizing.autoPageSize()
    restart()
  }

  return {
    limit,
    automatic,
    cursor,
    direction,
    visible,
    query,
    step,
    fitPageSize,
    choosePageSize,
    autoPageSize,
  }
}
