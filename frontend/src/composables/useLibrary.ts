import { computed, reactive, watch, type Ref } from 'vue'
import { useTimeoutFn } from '@vueuse/core'
import { api } from '../api/http.ts'
import type { LibraryItem, LibraryPage, LibrarySelectionResult, Selection } from '../api/types.ts'
import { useAlive } from './lifetime.ts'
import { useAppList, type ListContext } from './useAppList.ts'

const pollIntervalMs = 1_500
const transferStatuses = ['queued', 'downloading', 'preparing', 'canceling']

function flag(ids: Set<string>, id: string, on: boolean) {
  if (on) ids.add(id)
  else ids.delete(id)
}

// Downloaded and imported files, polled while transfers run. Selected files remember
// whether they are ready and owned, also when they are on another page.
export function useLibrary(
  context: ListContext,
  importing?: Readonly<Ref<boolean>>,
  localTransfer?: Readonly<Ref<LibraryItem | null>>,
) {
  const { state, active } = context
  const alive = useAlive()
  const ready = reactive(new Set<string>())
  const manageable = reactive(new Set<string>())
  const canFilterMine = computed(
    () => state.value.mode.public && state.value.mode.sharedLibrary && !state.value.mode.service,
  )

  // The browser's own upload leads the downloads until the server lists it.
  function withLocalTransfer(items: LibraryItem[]) {
    const local = localTransfer?.value
    if (!local) return items
    return items.some((row) => row.id === local.id)
      ? items.map((row) => (row.id === local.id ? local : row))
      : [local, ...items]
  }

  function track(items: LibraryItem[], selected = list.marked.value) {
    for (const row of items) {
      if (!selected.has(row.id)) continue
      flag(manageable, row.id, row.canManage)
      flag(ready, row.id, row.status === 'done')
    }
  }

  function trust(result: LibrarySelectionResult) {
    for (const id of result.readyIds) if (list.marked.value.has(id)) ready.add(id)
    for (const id of result.manageIds) if (list.marked.value.has(id)) manageable.add(id)
  }

  const list = useAppList<LibraryPage>('library', context, {
    rows: withLocalTransfer,
    params(params, mine) {
      if (canFilterMine.value) params.set('mine', String(mine))
    },
    select(ids, rows) {
      for (const flags of [ready, manageable])
        for (const id of flags) if (!ids.has(id)) flags.delete(id)
      track(rows, ids)
    },
    matched: (result) => trust(result as LibrarySelectionResult),
    async loaded(items, current) {
      track(items)
      // Check unfinished selections outside this page; visible rows already carry status.
      const visible = new Set(items.map((row) => row.id))
      const ids = [...list.marked.value].filter((id) => !visible.has(id) && !ready.has(id))
      if (!ids.length) return
      const result = await api.post<LibrarySelectionResult>('/library/selection', {
        ids,
        filter: null,
      } satisfies Selection)
      if (current()) trust(result)
    },
  })

  const selectedIds = () => [...list.marked.value]
  const local = (id: string) => id === localTransfer?.value?.id
  const canShare = computed(
    () => list.marked.value.size > 0 && selectedIds().every((id) => !local(id) && ready.has(id)),
  )
  const canManage = computed(
    () =>
      list.marked.value.size > 0 &&
      (!state.value.mode.public || selectedIds().every((id) => local(id) || manageable.has(id))),
  )

  const transfersActive = () => state.value.downloadsActive || importing?.value === true

  const { start: startPoll, stop: stopPoll } = useTimeoutFn(
    async () => {
      if (!alive() || !active.value) return
      await list.load(true)
      if (
        alive() &&
        active.value &&
        (transfersActive() || list.rows.value.some((row) => transferStatuses.includes(row.status)))
      )
        startPoll()
    },
    pollIntervalMs,
    { immediate: false },
  )

  async function reloadFromStart() {
    list.clearPages()
    // A previous-page cursor can exclude new imports even on the first page.
    list.cursor.value = ''
    await list.load()
  }

  watch(active, (on) => (on ? startPoll() : stopPoll()), { flush: 'sync' })
  watch(transfersActive, (on) => (on ? startPoll() : void list.load(true)))
  watch(
    () => localTransfer?.value,
    () => {
      if (list.loaded.value) track(withLocalTransfer(list.loaded.value.items))
    },
  )
  // The first poll finds out whether rows on screen are still transferring.
  if (active.value) startPoll()

  return {
    ...list,
    mine: list.filter,
    canFilterMine,
    canShare,
    canManage,
    canSelect: (_: LibraryItem) => true,
    selectedIds,
    reloadFromStart,
  }
}
