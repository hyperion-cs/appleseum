import { computed, onScopeDispose, ref, watch, type Ref } from 'vue'
import { useTimeoutFn } from '@vueuse/core'
import { apiErrorCode } from '../api/errors.ts'
import { api } from '../api/http.ts'
import { createPageCache } from '../api/pageCache.ts'
import { adjacentPageQueries, type PageDirection } from '../api/pagination.ts'
import type { Selection, SelectionResult, SharePage, State } from '../api/types.ts'
import { useAlive, useLatest } from './lifetime.ts'
import { useAction, useReadError } from './useAction.ts'
import { usePager } from './usePager.ts'
import { usePolling } from './usePolling.ts'
import { useSelectionSummary } from './useSelectionSummary.ts'
import { useTableSettings } from './useTableSettings.ts'

const pollIntervalMs = 2_500
const searchDelayMs = 250

export function useShares(
  props: Readonly<{ state: State; active: boolean }>,
  events: { changed: () => void; stopped: (ids: string[]) => void },
  errorCode: Ref<string>,
) {
  const alive = useAlive()
  const latest = useLatest()
  const page = ref<SharePage | null>(null)
  const pager = usePager(() => 'shares', page, resetPage)
  const selected = ref(new Set<string>())
  const query = ref('')
  const sort = ref(props.state.tables?.shares?.value.sort ?? 'createdAt')
  const direction = ref(props.state.tables?.shares?.value.direction ?? 'desc')
  const loading = ref(false)
  const confirming = ref<string[] | null>(null)
  const menu = ref(false)
  const backgroundError = ref('')
  const readError = useReadError(errorCode)
  const { busy: selecting, run: runSelect } = useAction(errorCode)
  const { busy: stopping, run: runStop } = useAction(errorCode)
  const compact = computed(() => props.state.shareCount === 1)
  const { allSelected, someSelected, rememberAll, toggled } = useSelectionSummary(
    selected,
    () => query.value,
  )
  const pages = createPageCache((key, signal) => api.get<SharePage>('/shares?' + key, signal))
  const {
    start: searchLater,
    stop: cancelSearch,
    isPending: debouncing,
  } = useTimeoutFn(() => void load(true), searchDelayMs, { immediate: false })

  // A foreground read replaces the table and may reuse a prefetched page;
  // a background one refreshes the page in place.
  async function load(foreground = false) {
    if (!alive() || !props.active) return
    const current = latest.start()
    const key = pager.query(
      new URLSearchParams({ q: query.value, sort: sort.value, direction: direction.value }),
      !foreground,
    )
    if (foreground) readError.value = ''
    if (foreground || !page.value) {
      loading.value = true
      page.value = null
    }
    try {
      const next = await pages.read(key, !foreground)
      if (!current()) return
      page.value = next
      readError.value = backgroundError.value = ''
      if (!next.total) selected.value = new Set()
      if (!pager.automatic.value) pages.prefetch(adjacentPageQueries(key, next))
    } catch (e) {
      if (!current()) return
      const code = apiErrorCode(e)
      if (foreground || !page.value) readError.value = code
      if (!foreground) backgroundError.value = code
    } finally {
      if (current()) loading.value = false
    }
  }

  async function refresh() {
    pages.clear()
    await load()
  }

  function resetPage() {
    cancelSearch()
    latest.cancel()
    pager.cursor.value = ''
    pages.clear()
    void load(true)
  }

  function navigate(to: PageDirection) {
    if (pager.step(to)) void load(true)
  }

  function changeSort(key: string) {
    errorCode.value = ''
    direction.value = sort.value === key && direction.value === 'asc' ? 'desc' : 'asc'
    sort.value = key
    resetPage()
    tableSettings.save('shares', { sort: sort.value, direction: direction.value })
  }

  function toggle(id: string) {
    if (stopping.value || selecting.value || compact.value) return
    const next = toggled(id)
    if (next) selected.value = next
    else errorCode.value = 'selection_too_large'
  }

  function clearSelection() {
    selected.value = new Set()
    menu.value = false
  }

  function selectAll() {
    if (stopping.value) return
    const search = query.value
    return runSelect(async () => {
      const result = await api.post<SelectionResult>('/shares/selection', {
        ids: [],
        filter: { query: search, delisted: false, mine: false },
      } satisfies Selection)
      if (!alive() || !props.active || query.value !== search) return
      selected.value = new Set(result.ids)
      rememberAll()
      menu.value = false
    })
  }

  function toggleAll() {
    if (stopping.value || selecting.value) return
    if (allSelected.value) clearSelection()
    else void selectAll()
  }

  function stop() {
    if (!confirming.value?.length) return
    const ids = [...confirming.value]
    return runStop(async () => {
      await api.post('/shares/stop', { ids })
      if (!alive()) return
      selected.value = new Set()
      confirming.value = null
      events.stopped(ids)
      events.changed()
      await refresh()
    })
  }

  usePolling(async () => {
    if (!loading.value && !debouncing.value && !stopping.value && !confirming.value) await load()
  }, pollIntervalMs)
  const tableSettings = useTableSettings(
    () => props.state.tables,
    (table, value) => {
      if (table !== 'shares' || (sort.value === value.sort && direction.value === value.direction))
        return
      sort.value = value.sort
      direction.value = value.direction
      resetPage()
    },
    errorCode,
  )
  watch(compact, (single) => {
    clearSelection()
    if (single && query.value) {
      page.value = null
      loading.value = true
      query.value = ''
    } else resetPage()
  })
  watch(query, () => {
    latest.cancel()
    cancelSearch()
    pages.clear()
    selected.value = new Set()
    pager.cursor.value = ''
    searchLater()
  })
  watch(
    () => props.active,
    (active) => {
      if (active) void refresh()
      else {
        latest.cancel()
        pages.clear()
        loading.value = false
      }
    },
  )
  onScopeDispose(() => {
    cancelSearch()
    pages.clear()
  })
  return {
    limit: pager.limit,
    automatic: pager.automatic,
    fitPageSize: pager.fitPageSize,
    choosePageSize: pager.choosePageSize,
    autoPageSize: pager.autoPageSize,
    page,
    visiblePage: pager.visible,
    rows: computed(() => pager.visible.value?.items ?? []),
    selected,
    query,
    sort,
    direction,
    loading,
    stopping,
    selecting,
    confirming,
    menu,
    backgroundError,
    compact,
    allSelected,
    someSelected,
    refresh,
    toggle,
    clearSelection,
    toggleAll,
    selectAll,
    navigate,
    changeSort,
    stop,
  }
}
