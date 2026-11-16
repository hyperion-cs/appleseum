import { ref, type Ref } from 'vue'
import { sessionEnded } from '../api/errors.ts'
import { t } from '../i18n/index.ts'
import { useAction } from './useAction.ts'

interface SelectableList<Row> {
  readonly marked: Set<string>
  readonly allSelected: boolean
  canSelect(row: Row): boolean
  toggle(id: string): boolean
  selectAll(): Promise<boolean>
  setSelection(ids: Iterable<string>): void
}

// Messages, dialogs, the running action and selection gestures of an app list screen.
export function useListScreen<Row extends { id: string }, Dialog extends string>(
  list: SelectableList<Row>,
  errorCode: Ref<string>,
  expired: () => void,
) {
  const notice = ref('')
  const { running, busy, run } = useAction(errorCode, (code) => {
    if (sessionEnded(code)) expired()
  })
  const dialog = ref<Dialog | 'menu' | null>(null)

  function openDialog(value: Dialog | 'menu') {
    errorCode.value = ''
    dialog.value = value
  }

  function closeDialog() {
    dialog.value = null
    errorCode.value = ''
  }

  function toggle(row: Row) {
    if (busy.value || !list.canSelect(row)) return
    if (!list.toggle(row.id)) errorCode.value = 'selection_too_large'
  }

  function selectAll() {
    return run(async () => {
      if (await list.selectAll()) dialog.value = null
    }, 'select')
  }

  function clearSelection() {
    list.setSelection([])
    dialog.value = null
  }

  function toggleAll() {
    if (busy.value) return
    if (list.allSelected) clearSelection()
    else void selectAll()
  }

  // Actions on a selection explain themselves when there is nothing to act on.
  function chosen(count = list.marked.size) {
    if (!count) notice.value = t.value.choose
    return count > 0
  }

  return {
    notice,
    running,
    busy,
    run,
    dialog,
    openDialog,
    closeDialog,
    toggle,
    selectAll,
    clearSelection,
    toggleAll,
    chosen,
  }
}
