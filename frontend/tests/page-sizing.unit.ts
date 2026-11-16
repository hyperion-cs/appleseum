import { test, type TestContext } from 'node:test'
import assert from 'node:assert/strict'
import { ref } from 'vue'
import { usePageSizing } from '../src/composables/usePageSizing.ts'
import { shortenPage } from '../src/api/pagination.ts'
import type { PageSizeResource } from '../src/composables/pageSizeStorage.ts'

function browser(t: TestContext) {
  const values = new Map<string, string>()
  for (const [name, value] of Object.entries({
    window: { matchMedia: () => ({ matches: false }) },
    localStorage: {
      getItem: (key: string) => values.get(key) ?? null,

      setItem: (key: string, value: string) => values.set(key, value),

      removeItem: (key: string) => values.delete(key),
    },
  })) {
    const before = Object.getOwnPropertyDescriptor(globalThis, name)
    Object.defineProperty(globalThis, name, { value, configurable: true })
    t.after(() =>
      before
        ? Object.defineProperty(globalThis, name, before)
        : Reflect.deleteProperty(globalThis, name),
    )
  }
  return values
}

test('automatic reductions never grow or write storage; manual choices persist per resource', (t) => {
  const values = browser(t)
  const resource = ref<PageSizeResource>('catalog')
  const sizing = usePageSizing(() => resource.value)
  assert.equal(sizing.automatic.value, true)
  sizing.fitPageSize(6)
  sizing.fitPageSize(8)
  assert.equal(sizing.limit.value, 6)
  assert.equal(values.size, 0)
  sizing.choosePageSize(6) // Choosing the current number is still a manual choice.
  sizing.fitPageSize(2)
  assert.equal(sizing.limit.value, 6)
  assert.equal(sizing.automatic.value, false)
  assert.equal(values.get('appleseum.pageSize.catalog'), '6')
  resource.value = 'library'
  assert.equal(sizing.automatic.value, true)
  sizing.fitPageSize(3)
  resource.value = 'catalog'
  assert.equal(sizing.limit.value, 6)
  assert.equal(sizing.automatic.value, false)
  sizing.autoPageSize()
  assert.equal(values.get('appleseum.pageSize.catalog'), undefined)
  assert.equal(sizing.automatic.value, true)
})

test('shortening uses the last visible item cursor without mutating cached pages', () => {
  const items = ['a', 'b', 'c', 'd'].map((cursor) => ({ cursor }))
  const original = { total: 4, items, next: false, previous: true }
  const shortened = shortenPage(original, 2)
  assert.deepEqual(shortened, { total: 4, items: items.slice(0, 2), next: true, previous: true })
  assert.equal(original.items.length, 4)
  assert.equal(original.next, false)
  assert.equal(shortenPage(original, 4), original)
})
