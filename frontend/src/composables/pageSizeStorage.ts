import { maxPageSize, productName } from '../api/types.ts'
import { defaultPageSize, defaultVersionPageSize } from '../api/pagination.ts'
import { browserStorage } from '../platform/browser.ts'

const legacyVersionPageSize = 10

export type PageSizeResource = 'catalog' | 'library' | 'debug' | 'versions' | 'shares'

// The current key first, then the names this preference had before.
function storageKeys(resource: PageSizeResource) {
  const key = productName + '.pageSize.' + resource
  return resource === 'versions'
    ? [key + '.v2', key, 'illbeback.pageSize.' + resource]
    : [key, 'illbeback.pageSize.' + resource]
}

function defaultSize(resource: PageSizeResource) {
  return resource === 'versions' ? defaultVersionPageSize : defaultPageSize
}

// A blocked storage only loses persistence: the open table keeps its size in memory.
function savedPageSize(resource: PageSizeResource): number | undefined {
  try {
    const [current, ...legacy] = storageKeys(resource).map((key) => browserStorage.getItem(key))
    const value = Number(current ?? legacy.find((saved) => saved !== null))
    if (!Number.isInteger(value) || value < 1 || value > maxPageSize) return undefined
    // The old default of the version list was never a choice.
    return resource === 'versions' && current === null && value === legacyVersionPageSize
      ? defaultVersionPageSize
      : value
  } catch {
    return undefined
  }
}

export function hasPageSize(resource: PageSizeResource) {
  return savedPageSize(resource) !== undefined
}

export function readPageSize(resource: PageSizeResource) {
  return savedPageSize(resource) ?? defaultSize(resource)
}

export function writePageSize(resource: PageSizeResource, value: number) {
  try {
    browserStorage.setItem(storageKeys(resource)[0]!, String(value))
  } catch {}
}

// Legacy names go too, so an earlier manual choice cannot reappear.
export function clearPageSize(resource: PageSizeResource) {
  try {
    for (const key of storageKeys(resource)) browserStorage.removeItem(key)
  } catch {}
}
