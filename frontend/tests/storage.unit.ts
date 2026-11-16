import assert from 'node:assert/strict'
import test from 'node:test'
import { createBrowserStorage } from '../src/platform/browser.ts'
import { createDemo } from '../src/api/demo.ts'

test('promo storage cannot read, overwrite or remove production and legacy keys', (t) => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const values = new Map<string, string>([
    ['appleseum.sessionKey', 'production-session'],
    ['illbeback.pageSize.catalog', '50'],
  ])
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => values.get(key) ?? null,

      setItem: (key: string, value: string) => values.set(key, value),

      removeItem: (key: string) => values.delete(key),
    },
  })
  t.after(() => {
    if (previous) Object.defineProperty(globalThis, 'localStorage', previous)
    else Reflect.deleteProperty(globalThis, 'localStorage')
  })
  const production = createBrowserStorage(false)
  const promo = createBrowserStorage(true)
  assert.equal(promo.getItem('appleseum.sessionKey'), null)
  assert.equal(promo.getItem('illbeback.pageSize.catalog'), null)
  promo.setItem('appleseum.sessionKey', 'promo-session')
  assert.equal(production.getItem('appleseum.sessionKey'), 'production-session')
  promo.removeItem('appleseum.sessionKey')
  assert.equal(production.getItem('appleseum.sessionKey'), 'production-session')

  const { request: mock } = createDemo()
  mock('/preferences', 'PUT', { language: 'ru', theme: 'light' })
  const table = { sort: 'name', direction: 'asc', delisted: false }
  mock('/table-settings/catalog', 'PUT', table)
  mock('/library/delete', 'POST', {
    ids: [],
    filter: { query: '', delisted: false, mine: false },
  })
  const { request: fresh } = createDemo()
  return Promise.all([fresh('/state').json(), fresh('/library').json()]).then(
    ([state, library]) => {
      assert.equal(state.preferences.language, 'ru')
      assert.equal(state.preferences.theme, 'light')
      assert.deepEqual(state.tables.catalog.value, table)
      assert.equal(library.total, 8)
      assert.ok(library.items.every((item: { status: string }) => item.status === 'done'))
      assert.equal(values.get('appleseum.sessionKey'), 'production-session')
      assert.equal(values.get('illbeback.pageSize.catalog'), '50')
      for (const key of values.keys())
        assert.ok(
          key.startsWith('appleseum.demo.') ||
            ['appleseum.sessionKey', 'illbeback.pageSize.catalog'].includes(key),
        )
    },
  )
})
