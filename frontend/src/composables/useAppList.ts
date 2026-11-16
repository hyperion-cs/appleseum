import { computed, onScopeDispose, ref, watch, type Ref } from 'vue'
import { useTimeoutFn } from '@vueuse/core'
import { apiErrorCode } from '../api/errors.ts'
import { api } from '../api/http.ts'
import { createPageCache } from '../api/pageCache.ts'
import type { PageDirection } from '../api/pagination.ts'
import type {
  CatalogPage,
  LibraryPage,
  LibrarySelectionResult,
  SelectionResult,
  State,
  CatalogView,
  Direction,
  LibraryView,
  Selection,
} from '../api/types.ts'
import { useAlive, useLatest } from './lifetime.ts'
import { usePager } from './usePager.ts'
import { useSelectionSummary } from './useSelectionSummary.ts'
import { useTableSettings } from './useTableSettings.ts'

export type Resource = 'catalog' | 'library'
type Page = CatalogPage | LibraryPage

export interface ListContext {
  state: Readonly<Ref<State>>
  error: Ref<string>
  // A tab kept alive off screen neither loads nor polls.
  active: Readonly<Ref<boolean>>
}

// What a list adds to the shared paging, search, settings and selection.
export interface ListHooks<Row> {
  // Rows shown besides the server page.
  rows?: (items: Row[]) => Row[]
  params?: (params: URLSearchParams, filter: boolean) => void
  sortable?: (key: string, filter: boolean) => boolean
  // Adjusts a new selection before it applies; `rows` are the visible ones.
  select?: (ids: Set<string>, rows: Row[]) => void
  matched?: (result: SelectionResult | LibrarySelectionResult) => void
  loaded?: (items: Row[], current: () => boolean) => Promise<void> | void
}

const searchDelayMs = 300
const slowDelayMs = 300
// The catalog's filter shows delisted apps; the library's shows the user's own files.
type View = CatalogView | LibraryView

const defaultViews: Record<Resource, View> = {
  catalog: { sort: 'purchasedAt', direction: 'desc', delisted: true },
  library: { sort: 'createdAt', direction: 'desc', mine: true },
}

const filterOf = (view: View) => ('delisted' in view ? view.delisted : view.mine)

function viewOf(resource: Resource, sort: string, direction: Direction, filter: boolean): View {
  return resource === 'catalog'
    ? { sort, direction, delisted: filter }
    : { sort, direction, mine: filter }
}

// Apple reports no status for delisted apps, so that view cannot be sorted by it.
function normalize(resource: Resource, value: View): View {
  const view = viewOf(resource, value.sort, value.direction, filterOf(value))
  return resource === 'catalog' && filterOf(view) && view.sort === 'status'
    ? viewOf(resource, 'name', 'asc', true)
    : view
}

// One server-paged app table: search, sorting, one filter switch, paging and selection.
// The catalog's filter shows delisted apps; the library's shows the user's own files.
export function useAppList<P extends Page>(
  resource: Resource,
  { state, error, active }: ListContext,
  hooks: ListHooks<P['items'][number]> = {},
) {
  type Row = P['items'][number]
  const alive = useAlive()
  const latest = useLatest()
  let saved = normalize(resource, state.value.tables?.[resource]?.value ?? defaultViews[resource])

  const query = ref('')
  const sort = ref(saved.sort)
  const direction = ref(saved.direction)
  const filter = ref(filterOf(saved))
  const loaded = ref<P | null>(null) as Ref<P | null>
  const loading = ref(false)
  const slow = ref(false)
  const navigating = ref<PageDirection | ''>('')
  const backgroundError = ref('')

  const shown = (items: Row[]) => hooks.rows?.(items) ?? items
  const extraRows = computed(
    () => shown(loaded.value?.items ?? []).length - (loaded.value?.items.length ?? 0),
  )
  const pager = usePager(
    () => resource,
    loaded,
    () => void load(),
    () => extraRows.value,
  )
  const rows = computed(() =>
    shown(loaded.value?.items ?? []).slice(
      0,
      (pager.visible.value?.items.length ?? 0) + extraRows.value,
    ),
  )

  const marked = ref(new Set<string>())
  const summary = useSelectionSummary(marked, () => JSON.stringify([query.value, filter.value]))

  const canSort = (key: string) => hooks.sortable?.(key, filter.value) ?? true

  function setSelection(ids: Iterable<string>) {
    const selected = new Set(ids)
    hooks.select?.(selected, rows.value)
    marked.value = selected
  }

  function deselect(ids: Iterable<string>) {
    const removed = new Set(ids)
    setSelection([...marked.value].filter((id) => !removed.has(id)))
  }

  // False when the limit forbids selecting one more.
  function toggle(id: string) {
    const next = summary.toggled(id)
    if (next) setSelection(next)
    return next !== null
  }

  // Selects every match on the server; false when the search changed meanwhile.
  async function selectAll() {
    const search = query.value
    const filtered = filter.value
    const input: Selection = {
      ids: [],
      filter: {
        query: search,
        delisted: resource === 'catalog' && filtered,
        mine: resource === 'library' && filtered,
      },
    }
    const result = await api.post<SelectionResult | LibrarySelectionResult>(
      `/${resource}/selection`,
      input,
    )
    if (!alive() || query.value !== search || filter.value !== filtered) return false
    setSelection(result.ids)
    hooks.matched?.(result)
    summary.rememberAll()
    return true
  }

  const pages = createPageCache((key, signal) => api.get<P>('/' + key, signal))
  const { start: searchLater, stop: cancelSearch } = useTimeoutFn(
    () => void load(),
    searchDelayMs,
    { immediate: false },
  )
  const { start: showSlow, stop: cancelSlow } = useTimeoutFn(
    () => {
      slow.value = true
    },
    slowDelayMs,
    { immediate: false },
  )

  function params() {
    const params = new URLSearchParams({ sort: sort.value, direction: direction.value })
    if (query.value) params.set('q', query.value)
    hooks.params?.(params, filter.value)
    return params
  }

  function invalidate() {
    latest.cancel()
    cancelSearch()
    cancelSlow()
    loading.value = slow.value = false
    navigating.value = ''
  }

  async function load(background = false) {
    if (!alive() || !active.value || (background && loading.value)) return
    invalidate()
    const current = latest.start()
    const key = resource + '?' + pager.query(params(), background)
    if (!background) {
      loaded.value = pages.peek(key) ?? loaded.value
      error.value = ''
      loading.value = true
      showSlow()
    }
    try {
      const next = await pages.read(key, true)
      if (!current()) return
      loaded.value = next
      backgroundError.value = ''
      await hooks.loaded?.(shown(next.items), current)
      if (!current()) return
      if (!next.items.length && pager.cursor.value) {
        pager.cursor.value = ''
        await load(background)
      }
    } catch (e) {
      if (current()) (background ? backgroundError : error).value = apiErrorCode(e)
    } finally {
      if (current()) {
        loading.value = slow.value = false
        navigating.value = ''
        cancelSlow()
      }
    }
  }

  function clearPages() {
    invalidate()
    pages.clear()
  }

  function changeSort(key: string) {
    if (!canSort(key)) return
    direction.value = sort.value === key && direction.value === 'asc' ? 'desc' : 'asc'
    sort.value = key
    pager.cursor.value = ''
    saveSettings()
    void load()
  }

  function navigate(to: PageDirection) {
    if (loading.value || !pager.step(to)) return
    void load()
    navigating.value = to
  }

  const tableSettings = useTableSettings(
    () => state.value.tables,
    (table, incoming) => {
      if (table !== resource) return
      const value = normalize(resource, incoming as View)
      if (
        value.sort === saved.sort &&
        value.direction === saved.direction &&
        filterOf(value) === filterOf(saved)
      )
        return
      apply((saved = value))
      void load()
    },
    error,
  )

  function saveSettings() {
    saved = viewOf(resource, sort.value, direction.value, filter.value)
    tableSettings.save(resource, saved)
  }

  // Settings saved elsewhere must not look like the user's own filter change.
  let applying = false

  function apply(value: View) {
    applying = true
    sort.value = value.sort
    direction.value = value.direction
    filter.value = filterOf(value)
    pager.cursor.value = ''
    applying = false
  }

  watch(
    [query, filter],
    ([, filterNow], [, filterBefore]) => {
      if (applying) return
      invalidate() // Ignore the old response during the search debounce, too.
      pager.cursor.value = ''
      if (filterNow !== filterBefore) {
        if (!canSort(sort.value)) {
          sort.value = 'name'
          direction.value = 'asc'
        }
        saveSettings()
        void load()
      } else {
        loading.value = true
        searchLater()
      }
    },
    { flush: 'sync' },
  )
  watch(active, (on) => (on ? void load() : invalidate()), { flush: 'sync' })
  onScopeDispose(clearPages)
  void load()

  return {
    query,
    sort,
    direction,
    filter,
    limit: pager.limit,
    automatic: pager.automatic,
    fitPageSize: pager.fitPageSize,
    choosePageSize: pager.choosePageSize,
    autoPageSize: pager.autoPageSize,
    cursor: pager.cursor,
    // The whole server page; `page` is the part that fits on screen.
    loaded,
    page: pager.visible,
    rows,
    total: computed(() => (loaded.value?.total ?? 0) + extraRows.value),
    loading,
    slow,
    navigating,
    backgroundError,
    marked,
    allSelected: summary.allSelected,
    someSelected: summary.someSelected,
    canSort,
    load,
    navigate,
    changeSort,
    setSelection,
    deselect,
    toggle,
    selectAll,
    clearPages,
  }
}
