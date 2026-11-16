import assert from 'node:assert/strict'
import { test, type TestContext } from 'node:test'
import {
  readPageSize,
  writePageSize,
  clearPageSize,
  hasPageSize,
} from '../src/composables/pageSizeStorage.ts'

function browser(t: TestContext, mobile = false) {
  const values = new Map<string, string>()
  const overrides = {
    window: { matchMedia: () => ({ matches: mobile }) },
    localStorage: {
      getItem: (key: string) => values.get(key) ?? null,

      setItem: (key: string, value: string) => values.set(key, value),

      removeItem: (key: string) => values.delete(key),
    },
  }
  for (const [name, value] of Object.entries(overrides)) {
    const previous = Object.getOwnPropertyDescriptor(globalThis, name)
    Object.defineProperty(globalThis, name, { value, configurable: true })
    t.after(() => {
      if (previous) Object.defineProperty(globalThis, name, previous)
      else Reflect.deleteProperty(globalThis, name)
    })
  }
  return { values, storage: overrides.localStorage }
}

test('renamed page preferences retain old choices and write under the new name', (t) => {
  const { values } = browser(t)
  values.set('illbeback.pageSize.catalog', '25')
  values.set('illbeback.pageSize.library', '50')
  assert.equal(readPageSize('catalog'), 25)
  assert.equal(readPageSize('library'), 50)
  writePageSize('catalog', 10)
  assert.equal(values.get('appleseum.pageSize.catalog'), '10')
  assert.equal(readPageSize('catalog'), 10)
  assert.equal(readPageSize('library'), 50)
})

test('invalid preferences use the initial ten-row limit', (t) => {
  const { values } = browser(t, true)
  for (const invalid of ['', '0', '129', '1.5', 'not a number']) {
    values.set('appleseum.pageSize.debug', invalid)
    assert.equal(readPageSize('debug'), 10)
  }
})

test('version pages default to five and retain manual limits after migrating the old default', (t) => {
  const { values } = browser(t)
  assert.equal(readPageSize('versions'), 5)
  assert.equal(readPageSize('catalog'), 10)
  values.set('appleseum.pageSize.versions', '10')
  assert.equal(readPageSize('versions'), 5)
  values.set('appleseum.pageSize.versions', '25')
  assert.equal(readPageSize('versions'), 25)
  writePageSize('versions', 10)
  assert.equal(readPageSize('versions'), 10)
  assert.equal(values.get('appleseum.pageSize.versions.v2'), '10')
})

test('blocked browser storage does not break the table', (t) => {
  const { storage } = browser(t)
  t.mock.method(storage, 'getItem', () => {
    throw new Error('blocked')
  })
  t.mock.method(storage, 'setItem', () => {
    throw new Error('blocked')
  })
  assert.equal(readPageSize('catalog'), 10)
  assert.doesNotThrow(() => writePageSize('catalog', 25))
})

for (const mobile of [false, true]) {
  test(`automatic lists start at ten rows (${mobile ? 'mobile' : 'desktop'}) and retain manual choices`, (t) => {
    browser(t, mobile)
    for (const resource of ['catalog', 'library', 'debug', 'shares'] as const)
      assert.equal(readPageSize(resource), 10)
    assert.equal(readPageSize('versions'), 5)
    writePageSize('shares', 8)
    assert.equal(readPageSize('shares'), 8)
  })
}

test('the maximum manual preference is retained without rewriting storage', (t) => {
  const { values } = browser(t)
  values.set('appleseum.pageSize.catalog', '128')
  assert.equal(readPageSize('catalog'), 128)
  assert.equal(values.get('appleseum.pageSize.catalog'), '128')
})

test('reset removes modern and legacy sizes only for the chosen table', (t) => {
  const { values } = browser(t)
  values.set('appleseum.pageSize.catalog', '20')
  values.set('illbeback.pageSize.catalog', '50')
  values.set('appleseum.pageSize.library', '25')
  clearPageSize('catalog')
  assert.equal(hasPageSize('catalog'), false)
  assert.equal(readPageSize('catalog'), 10)
  assert.deepEqual([...values], [['appleseum.pageSize.library', '25']])
})
