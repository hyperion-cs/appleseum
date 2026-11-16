import { onScopeDispose, watch, type Ref } from 'vue'
import { apiErrorCode } from '../api/errors.ts'
import { api } from '../api/http.ts'
import type { Tables } from '../api/types.ts'
import { useAlive } from './lifetime.ts'

type Table = keyof Tables
type View = Tables[Table]['value']

// Saves each table's latest settings in turn and reports newer ones saved elsewhere.
export function useTableSettings(
  source: () => Partial<Tables> | undefined,
  changed: (table: Table, value: View) => void,
  error: Ref<string>,
) {
  const alive = useAlive()
  const controller = new AbortController()
  const pending = new Map<Table, View>()
  const revisions = new Map<Table, number>()
  let writing: Table | null = null

  const saved = () => Object.entries(source() ?? {}) as [Table, Tables[Table]][]

  for (const [table, { revision }] of saved()) revisions.set(table, revision)

  async function flush() {
    if (writing) return
    while (pending.size && alive()) {
      const [table, value] = pending.entries().next().value!
      pending.delete(table)
      writing = table
      try {
        const { revision } = await api.put<Tables[Table]>(
          '/table-settings/' + table,
          value,
          controller.signal,
        )
        if (alive()) revisions.set(table, Math.max(revisions.get(table) ?? 0, revision))
      } catch (failure) {
        if (alive()) {
          // The next save retries this value unless a newer one is already queued.
          if (!pending.has(table)) pending.set(table, value)
          error.value = apiErrorCode(failure)
        }
        break
      } finally {
        writing = null
      }
    }
  }

  watch(
    () => ({ ...source() }),
    () => {
      for (const [table, { revision, value }] of saved()) {
        if (pending.has(table) || writing === table || revision <= (revisions.get(table) ?? 0))
          continue
        revisions.set(table, revision)
        changed(table, value)
      }
    },
  )
  onScopeDispose(() => {
    pending.clear()
    controller.abort()
  })
  return {
    save(table: Table, value: View) {
      if (!alive()) return
      pending.set(table, value)
      void flush()
    },
  }
}
