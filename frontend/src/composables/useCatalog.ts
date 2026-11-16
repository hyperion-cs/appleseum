import { watch } from 'vue'
import { api } from '../api/http.ts'
import type { CatalogItem, CatalogPage, Selection, SelectionResult } from '../api/types.ts'
import { useAlive } from './lifetime.ts'
import { useAppList, type ListContext } from './useAppList.ts'

// The account's purchases as Apple last reported them.
export function useCatalog(context: ListContext) {
  const { state, error } = context
  const alive = useAlive()
  const list = useAppList<CatalogPage>('catalog', context, {
    params(params, delisted) {
      if (delisted) params.set('delisted', 'true')
    },
    // Apple reports no status for delisted apps.
    sortable: (key, delisted) => key !== 'status' || !delisted,
    select(ids, rows) {
      for (const row of rows) if (!canSelect(row)) ids.delete(row.id)
    },
    async loaded(_, current) {
      list.setSelection(list.marked.value)
      await updateSelectedDownloads()
      if (current()) error.value = failure()
    },
  })

  const canSelect = (row: CatalogItem) => !row.inLibrary

  const failure = () => (state.value.catalog.status === 'failed' ? state.value.catalog.error : '')

  // Drops selected apps that reached the library since they were chosen.
  async function updateSelectedDownloads() {
    const ids = [...list.marked.value]
    if (!ids.length) return
    const result = await api.post<SelectionResult>('/catalog/selection', {
      ids,
      filter: null,
    } satisfies Selection)
    if (!alive()) return
    const eligible = new Set(result.ids)
    list.deselect(ids.filter((id) => !eligible.has(id)))
  }

  function reset() {
    list.clearPages()
    list.setSelection([])
    list.cursor.value = ''
    list.loaded.value = { total: 0, items: [], next: false, previous: false }
  }

  watch(
    () => state.value.catalog.status,
    (status) => {
      if (status === 'ready') {
        list.clearPages()
        void list.load(true)
      }
      if (status === 'failed' && context.active.value) error.value = failure()
    },
  )
  watch(
    () => state.value.catalog.cached,
    (cached, was) => {
      if (!cached && was) reset()
    },
  )
  // Finished downloads mark their apps as already in the library.
  watch(
    () => state.value.downloadsActive,
    (downloading) => {
      if (!downloading) void list.load(true)
    },
  )

  return { ...list, delisted: list.filter, canSelect, updateSelectedDownloads, reset }
}
