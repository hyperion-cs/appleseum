import './httpEnvironment.ts'
import { test, type TestContext } from 'node:test'
import assert from 'node:assert/strict'
import { setImmediate } from 'node:timers/promises'
import { effectScope, ref } from 'vue'
import { useCatalog } from '../src/composables/useCatalog.ts'
import { useLibrary } from '../src/composables/useLibrary.ts'
import type { State, LibraryItem } from '../src/api/types.ts'

function environment(t: TestContext, overrides: Partial<State>) {
  t.mock.method(globalThis, 'fetch', (path: string, input?: RequestInit) => {
    const entry = {
      path,
      input,

      finish: (_body: unknown) => {},
    }
    requests.push(entry)
    return new Promise<Response>((resolve) => {
      entry.finish = (body) => resolve(Response.json(body))
    })
  })
  const priorWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
  const priorStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  Object.defineProperty(globalThis, 'window', {
    configurable: true,
    value: { matchMedia: () => ({ matches: false }) },
  })
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: { getItem: () => null, setItem: () => {} },
  })
  t.after(() => {
    for (const [key, prior] of [
      ['window', priorWindow],
      ['localStorage', priorStorage],
    ] as const) {
      if (prior) Object.defineProperty(globalThis, key, prior)
      else Reflect.deleteProperty(globalThis, key)
    }
  })
  const requests: Array<{ path: string; input?: RequestInit; finish: (body: unknown) => void }> = []
  const state = ref({
    tables: {},
    downloadsActive: false,
    mode: { public: false, sharedLibrary: false, service: false },
    catalog: { status: 'ready' },
    ...overrides,
  } as State)
  const context = { state, error: ref(''), active: ref(true) }
  const scope = effectScope()
  t.after(() => scope.stop())
  return { context, scope, requests, state, active: context.active }
}

function catalog(t: TestContext, overrides: Partial<State> = {}) {
  const env = environment(t, overrides)
  return { ...env, list: env.scope.run(() => useCatalog(env.context))! }
}

function library(t: TestContext, overrides: Partial<State> = {}) {
  const env = environment(t, overrides)
  const localTransfer = ref<LibraryItem | null>(null)
  const list = env.scope.run(() => useLibrary(env.context, undefined, localTransfer))!
  return { ...env, list, localTransfer }
}

const page = (id: string, extra = {}) => ({
  total: 1,
  items: [{ id, name: id, cursor: id, ...extra }],
  next: false,
  previous: false,
})

test('catalog starts with newest purchases first when no settings are saved', (t) => {
  const { list, requests } = catalog(t)
  assert.equal(list.sort.value, 'purchasedAt')
  assert.equal(list.direction.value, 'desc')
  const params = new URL(requests[0]!.path, 'http://test').searchParams
  assert.equal(params.get('sort'), 'purchasedAt')
  assert.equal(params.get('direction'), 'desc')
})

test('server settings saved while the tab is hidden apply when it returns, from the first page', async (t) => {
  const { list, active, state, requests } = catalog(t, {
    tables: {
      catalog: {
        revision: 2,
        value: { sort: 'name', direction: 'asc', delisted: true },
      },
    },
  })
  requests[0]!.finish({ ...page('old', { cursor: 'old-cursor' }), next: true })
  await setImmediate()
  list.navigate('next')
  active.value = false
  const requested = requests.length
  state.value = {
    ...state.value,
    tables: {
      catalog: {
        revision: 3,
        value: { ...state.value.tables.catalog.value, sort: 'purchasedAt', direction: 'desc' },
      },
    },
  }
  await setImmediate()
  assert.equal(list.sort.value, 'purchasedAt')
  assert.equal(requests.length, requested)
  active.value = true
  assert.equal(list.direction.value, 'desc')
  assert.equal(list.delisted.value, true)
  const params = new URL(requests.at(-1)!.path, 'http://test').searchParams
  assert.equal(params.get('sort'), 'purchasedAt')
  assert.equal(params.get('direction'), 'desc')
  assert.equal(params.has('cursor'), false)
})

test('search ignores late answers during debounce; leaving the tab cancels its pending timer', async (t) => {
  const { list, active, requests } = catalog(t)
  t.mock.timers.enable({ apis: ['setTimeout'] })
  list.query.value = 'new search'
  requests[0]!.finish(page('old', { inLibrary: false }))
  await setImmediate()
  assert.equal(list.page.value, null)
  assert.equal(list.loading.value, true)
  active.value = false
  t.mock.timers.tick(350)
  assert.equal(requests.length, 1)
  assert.equal(list.loading.value, false)
  active.value = true
  assert.equal(list.query.value, 'new search')
  assert.match(requests[1]!.path, /q=new\+search/)
})

test('readiness on the visible page needs no selection request; completed selections remain shareable', async (t) => {
  const { list, requests } = library(t)
  requests[0]!.finish(page('file', { status: 'done' }))
  await setImmediate()
  list.setSelection(['file'])
  assert.equal(list.canShare.value, true)
  const refresh = list.load(true)
  requests[1]!.finish(page('file', { status: 'done' }))
  await refresh
  assert.equal(requests.length, 2)
  assert.equal(list.canShare.value, true)
})

test('clearing pages ignores late results', async (t) => {
  const { list, requests } = library(t)
  requests[0]!.finish(page('file', { status: 'done' }))
  await setImmediate()
  assert.equal(list.rows.value[0]?.id, 'file')
  const refreshing = list.load()
  list.clearPages()
  assert.equal(requests[1]!.input?.signal?.aborted, true)
  requests[1]!.finish(page('stale', { status: 'done' }))
  await refreshing
  assert.equal(list.rows.value[0]?.id, 'file')
})

test('completed imports reset a previous-page cursor and ignore older polling responses', async (t) => {
  const { list, requests } = library(t)
  requests[0]!.finish({ ...page('existing', { status: 'done', cursor: 'older' }), next: true })
  await setImmediate()
  list.navigate('next')
  requests[1]!.finish({
    ...page('old', { status: 'done', cursor: 'back-to-first' }),
    previous: true,
  })
  await setImmediate()
  list.navigate('previous')
  requests[2]!.finish({ ...page('existing', { status: 'done', cursor: 'older' }), next: true })
  await setImmediate()
  const polling = list.load(true)
  assert.match(requests[3]!.path, /cursor=back-to-first/)
  const refreshing = list.reloadFromStart()
  assert.equal(requests[3]!.input?.signal?.aborted, true)
  assert.equal(new URL(requests[4]!.path, 'http://test').searchParams.has('cursor'), false)
  requests[4]!.finish(page('imported', { status: 'done' }))
  await refreshing
  requests[3]!.finish(page('stale', { status: 'done' }))
  await polling
  assert.equal(list.rows.value[0]?.id, 'imported')
  list.setSelection(['imported'])
  assert.equal(list.canShare.value, true)
})

test('an import completed while the library is hidden resets its paging without loading off screen', async (t) => {
  const { list, active, requests } = library(t)
  requests[0]!.finish({ ...page('existing', { status: 'done', cursor: 'older' }), next: true })
  await setImmediate()
  list.navigate('next')
  requests[1]!.finish(page('old', { status: 'done' }))
  await setImmediate()
  active.value = false
  await list.reloadFromStart()
  assert.equal(requests.length, 2)
  active.value = true
  const params = new URL(requests[2]!.path, 'http://test').searchParams
  assert.equal(params.has('cursor'), false)
  assert.equal(params.get('sort'), 'createdAt')
  assert.equal(params.get('direction'), 'desc')
})

test('sorting saves the table settings', async (t) => {
  const { list, requests } = catalog(t)
  list.changeSort('purchasedAt')
  assert.equal(list.sort.value, 'purchasedAt')
  assert.equal(list.direction.value, 'asc')
  const save = requests.find((r) => r.path === '/api/table-settings/catalog')!
  assert.deepEqual(JSON.parse(save.input!.body as string), {
    sort: 'purchasedAt',
    direction: 'asc',
    delisted: true,
  })
  save.finish({ revision: 1 })
  await setImmediate()
  assert.equal(list.sort.value, 'purchasedAt')
})

test('background polling cannot interrupt a foreground navigation or bypass a search debounce', async (t) => {
  const { list, requests } = library(t)
  await list.load(true)
  assert.equal(requests.length, 1)
  assert.equal(list.loading.value, true)
  requests[0]!.finish(page('file', { status: 'done' }))
  await setImmediate()
  list.query.value = 'draft'
  await list.load(true)
  assert.equal(requests.length, 1)
  assert.equal(list.loading.value, true)
})

test('a newer saved sort never erases this browser search', async (t) => {
  const { list, state, requests } = catalog(t)
  list.query.value = 'local search'
  state.value = {
    ...state.value,
    tables: {
      catalog: {
        revision: 10,
        value: { sort: 'purchasedAt', direction: 'desc', delisted: false },
      },
      library: {
        revision: 10,
        value: { sort: 'createdAt', direction: 'desc', mine: true },
      },
    },
  }
  await setImmediate()
  assert.equal(list.query.value, 'local search')
  assert.equal(list.sort.value, 'purchasedAt')
  assert.equal(list.delisted.value, false)
  assert.match(requests.at(-1)!.path, /q=local\+search/)
})

test('public selections share ready files but only manage owned downloads', async (t) => {
  const { list, state, requests } = library(t)
  state.value = { ...state.value, mode: { ...state.value.mode, public: true } }
  requests[0]!.finish({
    total: 2,
    items: [
      { id: 'own', name: 'Own', status: 'done', canManage: true },
      { id: 'foreign', name: 'Foreign', status: 'done', canManage: false },
    ],
    next: false,
    previous: false,
  })
  await setImmediate()
  list.setSelection(['own'])
  assert.equal(list.canManage.value, true)
  list.setSelection(['own', 'foreign'])
  assert.equal(list.canShare.value, true)
  assert.equal(list.canManage.value, false)
  list.setSelection(['foreign'])
  assert.equal(list.canManage.value, false)
})

test('browser transfers appear before the server and keep their progress across polling', async (t) => {
  const { list, state, requests, localTransfer } = library(t)
  state.value = { ...state.value, mode: { ...state.value.mode, public: true } }
  const local: LibraryItem = {
    id: '01900000-0000-7000-8000-000000000002',
    appId: '',
    name: 'existing.ipa',
    status: 'downloading',
    received: 750,
    total: 1_000,
    speed: 500,
    error: '',
    version: '',
    minimumOs: '',
    size: 1_000,
    createdAt: '2026-11-07T12:00:00Z',
    canManage: true,
    icon: '',
  }
  localTransfer.value = local
  assert.equal(list.rows.value.length, 1)
  requests[0]!.finish({ total: 0, items: [], next: false, previous: false })
  await setImmediate()
  list.setSelection([local.id])
  assert.equal(list.canManage.value, true)
  assert.equal(list.canShare.value, false)
  const polling = list.load(true)
  requests.at(-1)!.finish({
    total: 1,
    items: [{ ...local, received: 0, speed: 0 }],
    next: false,
    previous: false,
  })
  await polling
  assert.equal(list.rows.value.length, 1)
  assert.equal((list.rows.value[0] as LibraryItem).received, 750)
  assert.equal((list.rows.value[0] as LibraryItem).speed, 500)
  localTransfer.value!.status = 'preparing'
  const completing = list.load(true)
  requests.at(-1)!.finish({
    total: 1,
    items: [{ ...local, name: 'Imported App', status: 'done', received: 1_000, speed: 0 }],
    next: false,
    previous: false,
  })
  await completing
  assert.equal((list.rows.value[0] as LibraryItem).status, 'preparing')
  assert.equal(list.canShare.value, false)
  localTransfer.value = null
  await setImmediate()
  assert.equal(list.rows.value[0]?.name, 'Imported App')
  assert.equal(list.canShare.value, true)
  assert.equal(
    requests.some((request) => request.path.includes('/selection')),
    false,
  )
})

test('shared library defaults to mine and saves changes for paging and select-all', async (t) => {
  const { list, active, requests } = library(t, {
    mode: { public: true, sharedLibrary: true, service: false },
  } as Partial<State>)
  assert.equal(list.mine.value, true)
  assert.equal(new URL(requests[0]!.path, 'http://test').searchParams.get('mine'), 'true')
  requests[0]!.finish({ ...page('own', { status: 'done', cursor: 'own-cursor' }), next: true })
  await setImmediate()
  list.navigate('next')
  assert.match(requests.at(-1)!.path, /cursor=own-cursor/)

  list.mine.value = false
  const save = requests.find((r) => r.path === '/api/table-settings/library')!
  assert.equal(JSON.parse(save.input!.body as string).mine, false)
  save.finish({ revision: 2 })
  const allPage = requests.at(-1)!
  const params = new URL(allPage.path, 'http://test').searchParams
  assert.equal(params.get('mine'), 'false')
  assert.equal(params.has('cursor'), false)
  allPage.finish(page('foreign', { status: 'done', canManage: false }))
  await setImmediate()

  const selecting = list.selectAll()
  assert.deepEqual(JSON.parse(requests.at(-1)!.input!.body as string), {
    ids: [],
    filter: { query: '', delisted: false, mine: false },
  })
  requests.at(-1)!.finish({ ids: ['foreign'], readyIds: ['foreign'], manageIds: [] })
  await selecting
  assert.deepEqual([...list.marked.value], ['foreign'])

  active.value = false
  active.value = true
  assert.equal(list.mine.value, false)
  assert.equal(new URL(requests.at(-1)!.path, 'http://test').searchParams.get('mine'), 'false')

  list.mine.value = true
  const ownSelecting = list.selectAll()
  assert.equal(JSON.parse(requests.at(-1)!.input!.body as string).filter.mine, true)
  requests.at(-1)!.finish({ ids: ['own'], readyIds: ['own'], manageIds: ['own'] })
  await ownSelecting
  assert.deepEqual([...list.marked.value], ['own'])
})

test('a saved shared library filter is restored and accepts newer server settings', async (t) => {
  const { list, state, requests } = library(t, {
    mode: { public: true, sharedLibrary: true, service: false },
    tables: {
      catalog: {
        revision: 1,
        value: { sort: 'name', direction: 'asc', delisted: true },
      },
      library: {
        revision: 2,
        value: { sort: 'createdAt', direction: 'desc', mine: false },
      },
    },
  })
  assert.equal(list.mine.value, false)
  assert.match(requests[0]!.path, /mine=false/)
  state.value = {
    ...state.value,
    tables: {
      ...state.value.tables,
      library: {
        revision: 3,
        value: { ...state.value.tables.library.value, mine: true },
      },
    },
  }
  await setImmediate()
  assert.equal(list.mine.value, true)
  assert.match(requests.at(-1)!.path, /mine=true/)
})

test('fitting updates visible rows, limit and continuation without a request; navigation uses the visible boundary', async (t) => {
  const { list, requests } = catalog(t)
  const items = Array.from({ length: 10 }, (_, i) => ({
    id: String(i),
    name: String(i),
    cursor: 'after-' + i,
  }))
  requests[0]!.finish({
    total: 10,
    items,
    next: false,
    previous: false,
  })
  await setImmediate()
  list.fitPageSize(3)
  assert.equal(requests.length, 1)
  assert.equal(list.limit.value, 3)
  assert.deepEqual(
    list.rows.value.map((row) => row.id),
    ['0', '1', '2'],
  )
  assert.equal(list.page.value?.next, true)
  list.navigate('next')
  const params = new URL(requests[1]!.path, 'http://test').searchParams
  assert.equal(params.get('limit'), '3')
  assert.equal(params.get('cursor'), 'after-2')
})

test('polling a shortened backward page keeps the original request size and start boundary', async (t) => {
  const { list, requests } = catalog(t)
  const items = Array.from({ length: 10 }, (_, i) => ({
    id: String(i),
    name: String(i),
    cursor: 'after-' + i,
  }))
  const result = {
    total: 30,
    items,
    next: true,
    previous: true,
  }
  requests[0]!.finish(result)
  await setImmediate()
  list.navigate('previous')
  requests[1]!.finish(result)
  await setImmediate()
  list.fitPageSize(3)
  const poll = list.load(true)
  const params = new URL(requests[2]!.path, 'http://test').searchParams
  assert.equal(params.get('limit'), '10')
  assert.equal(params.get('cursor'), 'after-0')
  assert.equal(params.get('move'), 'prev')
  requests[2]!.finish(result)
  await poll
  assert.deepEqual(
    list.rows.value.map((row) => row.id),
    ['0', '1', '2'],
  )
  list.choosePageSize(3)
  assert.equal(list.automatic.value, false)
  list.fitPageSize(1)
  assert.equal(list.limit.value, 3)
})

test('select-all ignores a response for an earlier search without loading any pages', async (t) => {
  const { list, requests } = catalog(t)
  requests[0]!.finish(page('one'))
  await setImmediate()
  const selecting = list.selectAll()
  const request = requests.at(-1)!
  assert.equal(request.path, '/api/catalog/selection')
  const reads = requests.filter((r) => r.path.startsWith('/api/catalog?')).length
  list.query.value = 'another search'
  request.finish({ ids: ['one', 'off-page'] })
  assert.equal(await selecting, false)
  assert.equal(list.marked.value.size, 0)
  assert.equal(requests.filter((r) => r.path.startsWith('/api/catalog?')).length, reads)
})

test('deselected file metadata cannot authorize a later off-page selection', async (t) => {
  const { list, requests } = library(t, {
    mode: { public: true, sharedLibrary: false, service: false },
  } as Partial<State>)
  requests[0]!.finish(page('owned', { status: 'done', canManage: true }))
  await setImmediate()
  list.setSelection(['owned'])
  assert.equal(list.canShare.value, true)
  assert.equal(list.canManage.value, true)
  list.setSelection([])
  const reading = list.load()
  requests[1]!.finish(page('other', { status: 'done', canManage: false }))
  await reading
  list.setSelection(['owned'])
  assert.equal(list.canShare.value, false)
  assert.equal(list.canManage.value, false)
  const selecting = list.selectAll()
  requests[2]!.finish({ ids: ['owned'], readyIds: ['owned'], manageIds: ['owned'] })
  await selecting
  assert.equal(list.canShare.value, true)
  assert.equal(list.canManage.value, true)
})

test('disposing a list discards queued table settings writes', async (t) => {
  const { list, requests, scope } = catalog(t)
  list.changeSort('name')
  const save = requests.find((request) => request.path === '/api/table-settings/catalog')!
  list.changeSort('purchasedAt')
  scope.stop()
  save.finish({ revision: 1 })
  await setImmediate()
  assert.equal(
    requests.filter((request) => request.path.startsWith('/api/table-settings/')).length,
    1,
  )
})
