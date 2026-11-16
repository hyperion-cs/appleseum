import { computed, reactive } from 'vue'
import { maxPageSize } from '../api/types.ts'
import {
  hasPageSize,
  readPageSize,
  writePageSize,
  clearPageSize,
  type PageSizeResource,
} from './pageSizeStorage.ts'

// Automatic reductions live only in memory; only explicit choices reach storage.
export function usePageSizing(resource: () => PageSizeResource) {
  const states = new Map<PageSizeResource, { size: number; automatic: boolean }>()
  const current = computed(() => {
    const key = resource()
    if (!states.has(key))
      states.set(key, reactive({ size: readPageSize(key), automatic: !hasPageSize(key) }))
    return states.get(key)!
  })
  const limit = computed(() => current.value.size)
  const automatic = computed(() => current.value.automatic)

  function choosePageSize(value: number) {
    if (!Number.isInteger(value) || value < 1 || value > maxPageSize) return
    Object.assign(current.value, { size: value, automatic: false })
    writePageSize(resource(), value)
  }

  function fitPageSize(value: number) {
    if (automatic.value && Number.isInteger(value) && value >= 1 && value < limit.value)
      current.value.size = value
  }

  function autoPageSize() {
    clearPageSize(resource())
    Object.assign(current.value, { size: readPageSize(resource()), automatic: true })
  }

  return { limit, automatic, choosePageSize, fitPageSize, autoPageSize }
}
