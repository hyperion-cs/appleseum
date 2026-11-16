import { computed, shallowRef, watch, type Ref } from 'vue'
import { maxSelectionSize } from '../api/types.ts'

export function useSelectionSummary<T>(selected: Ref<Set<T>>, scope: () => string) {
  const matching = shallowRef<Set<T> | null>(null)
  watch(
    scope,
    () => {
      matching.value = null
    },
    { flush: 'sync' },
  )
  const allSelected = computed(
    () => !!matching.value?.size && [...matching.value].every((id) => selected.value.has(id)),
  )
  const someSelected = computed(() => selected.value.size > 0 && !allSelected.value)

  function rememberAll() {
    matching.value = new Set(selected.value)
  }

  // The selection after a click on `id`, or null when the limit forbids one more.
  function toggled(id: T) {
    if (!selected.value.has(id) && selected.value.size >= maxSelectionSize) return null
    const next = new Set(selected.value)
    if (!next.delete(id)) next.add(id)
    return next
  }

  return { allSelected, someSelected, rememberAll, toggled }
}
