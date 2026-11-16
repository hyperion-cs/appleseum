import { computed, onScopeDispose, ref, watch, type Ref } from 'vue'
import { useTimeoutFn } from '@vueuse/core'
import { apiErrorCode } from '../api/errors.ts'
import { api, request } from '../api/http.ts'
import { createPageCache } from '../api/pageCache.ts'
import { adjacentPageQueries, type PageDirection } from '../api/pagination.ts'
import type {
  DebugEvent,
  DebugExportRequest,
  DebugSelectionResult,
  DebugInfo,
  DebugPage,
} from '../api/types.ts'
import { downloadFile } from '../platform/browser.ts'
import { useAlive, useLatest } from './lifetime.ts'
import { useAction, useReadError } from './useAction.ts'
import { usePager } from './usePager.ts'
import { usePolling } from './usePolling.ts'
import { useSelectionSummary } from './useSelectionSummary.ts'

const pollIntervalMs = 2_500
const searchDelayMs = 300
const defaultSkeletonDelayMs = 300

export function useDebugLog(errorCode: Ref<string>) {
  const alive = useAlive()
  const latest = useLatest()
  const page = ref<DebugPage | null>(null)
  const pager = usePager(() => 'debug', page, resetPage)
  const query = ref('')
  const sources = ref<string[]>([])
  const selected = ref(new Set<number>())
  const busy = ref(false)
  const menu = ref(false)
  const confirmingClear = ref(false)
  const backgroundError = ref('')
  const readError = useReadError(errorCode)
  const { busy: selecting, run: runSelect } = useAction(errorCode)
  const { busy: downloading, run: runDownload } = useAction(errorCode)
  const info = ref<DebugInfo | null>(null)
  const pages = createPageCache((key, signal) => api.get<DebugPage>('/debug?' + key, signal))
  const showSkeleton = ref(false)
  const { start: scheduleSkeleton, stop: cancelSkeleton } = useTimeoutFn(
    () => {
      showSkeleton.value = true
    },
    () =>
      parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--skeleton-delay')) ||
      defaultSkeletonDelayMs,
    { immediate: false },
  )
  const { start: searchLater, stop: cancelSearch } = useTimeoutFn(firstPage, searchDelayMs, {
    immediate: false,
  })

  const selectionScope = () => JSON.stringify([query.value, sources.value])

  const { allSelected, someSelected, rememberAll, toggled } = useSelectionSummary(
    selected,
    selectionScope,
  )

  function filters() {
    const params = new URLSearchParams()
    for (const source of sources.value) params.append('source', source)
    if (query.value) params.set('q', query.value)
    return params
  }

  // A forced read follows the user's change; an unforced one is the background poll.
  async function refresh(force = false) {
    if (!alive() || (!force && (busy.value || confirmingClear.value))) return
    if (force) readError.value = ''
    const current = latest.start()
    let key = pager.query(filters(), !force)
    if (force) page.value = pages.peek(key) ?? page.value
    busy.value = true
    if (!page.value) scheduleSkeleton()
    try {
      const next = await pages.read(key, true).catch((e: unknown) => {
        if (apiErrorCode(e) !== 'invalid_request' || !pager.cursor.value || !current()) throw e
        // The log no longer has the row this cursor points at: start over.
        pager.cursor.value = ''
        selected.value.clear()
        pages.clear()
        key = pager.query(filters())
        return pages.read(key, true)
      })
      // The process summary changes with every poll but is no part of the page.
      const process = await api.get<DebugInfo>('/debug/info')
      if (!current()) return
      page.value = next
      info.value = process
      readError.value = backgroundError.value = ''
      if (!pager.automatic.value) pages.prefetch(adjacentPageQueries(key, next))
    } catch {
      if (!current()) return
      if (force || !page.value) readError.value = 'error'
      if (!force) backgroundError.value = 'error'
    } finally {
      if (current()) {
        busy.value = showSkeleton.value = false
        cancelSkeleton()
      }
    }
  }

  function firstPage() {
    pager.cursor.value = ''
    void refresh(true)
  }

  function resetPage() {
    cancelSearch()
    selected.value.clear()
    firstPage()
  }

  function navigate(to: PageDirection) {
    if (pager.step(to)) void refresh(true)
  }

  function toggle(event: DebugEvent) {
    if (!event.hasResponse || selecting.value) return
    const next = toggled(event.id)
    if (next) selected.value = next
    else errorCode.value = 'selection_too_large'
  }

  function clearSelection() {
    selected.value.clear()
    menu.value = false
  }

  function selectAll() {
    const scope = selectionScope()
    return runSelect(async () => {
      const result = await api.get<DebugSelectionResult>('/debug/selection?' + filters())
      if (!alive() || scope !== selectionScope()) return
      selected.value = new Set(result.ids)
      rememberAll()
      menu.value = false
    })
  }

  function toggleAll() {
    if (selecting.value) return
    if (allSelected.value) clearSelection()
    else void selectAll()
  }

  function download(path: string, filename: string, input?: DebugExportRequest) {
    return runDownload(async () => {
      const response = await request<Blob>(input ? 'POST' : 'GET', path, {
        body: input,
        responseType: 'blob',
      })
      downloadFile(response.data, filename)
    })
  }

  function downloadHar() {
    const ids = [...selected.value]
    if (ids.length === 1) void download('/debug/responses/' + ids[0], 'exchange-' + ids[0] + '.har')
    else if (ids.length) void download('/debug/responses', 'exchanges.har', { ids })
  }

  async function clear() {
    if (busy.value) return
    latest.cancel()
    cancelSearch()
    pages.clear()
    busy.value = true
    try {
      await api.delete('/debug')
      confirmingClear.value = false
      pages.clear()
      pager.cursor.value = ''
      selected.value.clear()
      if (page.value)
        Object.assign(page.value, { items: [], total: 0, next: false, previous: false })
      errorCode.value = ''
    } catch (e) {
      errorCode.value = apiErrorCode(e)
    } finally {
      busy.value = false
    }
  }

  watch(sources, resetPage)
  watch(
    query,
    () => {
      latest.cancel()
      pager.cursor.value = ''
      selected.value.clear()
      cancelSkeleton()
      showSkeleton.value = false
      searchLater()
    },
    { flush: 'sync' },
  )
  usePolling(() => refresh(), pollIntervalMs)
  onScopeDispose(() => {
    cancelSearch()
    cancelSkeleton()
    pages.clear()
  })
  return {
    limit: pager.limit,
    automatic: pager.automatic,
    fitPageSize: pager.fitPageSize,
    choosePageSize: pager.choosePageSize,
    autoPageSize: pager.autoPageSize,
    snapshot: page,
    info,
    visiblePage: pager.visible,
    events: computed(() => pager.visible.value?.items ?? []),
    query,
    sources,
    selected,
    busy,
    selecting,
    downloading,
    menu,
    confirmingClear,
    backgroundError,
    showSkeleton,
    allSelected,
    someSelected,
    toggle,
    toggleAll,
    selectAll,
    clearSelection,
    navigate,
    download,
    downloadHar,
    clear,
  }
}
