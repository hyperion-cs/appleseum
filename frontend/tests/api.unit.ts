import './httpEnvironment.ts'
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { ApiError } from '../src/api/errors.ts'
import { api, request } from '../src/api/http.ts'
import { fetchState as state, logout } from '../src/api/state.ts'

test('JSON and file downloads share CSRF, credentials and server error handling', async (t) => {
  const requests: RequestInit[] = []
  t.mock.method(globalThis, 'fetch', async (path: string, input: RequestInit) => {
    requests.push(input)
    if (path === '/api/state')
      return Response.json({ csrf: 'synthetic-token', mode: { public: false } })
    if (path === '/api/action') return new Response(null, { status: 202 })
    if (path === '/api/download') return new Response('file')
    return Response.json({ code: 'storage_failed' }, { status: 500 })
  })
  await state()
  assert.equal(await api.post('/action', { ids: ['synthetic'] }), undefined)
  assert.equal(new Headers(requests[1]!.headers).get('X-CSRF-Token'), 'synthetic-token')
  assert.equal(new Headers(requests[1]!.headers).get('Content-Type'), 'application/json')
  assert.equal(requests[1]!.credentials, 'same-origin')
  assert.equal((await request('GET', '/download', { responseType: 'text' })).data, 'file')
  assert.equal(new Headers(requests[2]!.headers).get('X-CSRF-Token'), 'synthetic-token')
  await assert.rejects(
    request('GET', '/failure'),
    (error: unknown) => error instanceof ApiError && error.code === 'storage_failed',
  )
})

test('malformed error bodies have a stable fallback; aborted reads keep their cancellation', async (t) => {
  for (const body of ['null', '{}', '{"code":17}', 'not json']) {
    t.mock.method(globalThis, 'fetch', async () => new Response(body, { status: 502 }))
    await assert.rejects(
      api.get('/broken'),
      (error: unknown) => error instanceof ApiError && error.code === 'error',
    )
    t.mock.restoreAll()
  }
  const controller = new AbortController()
  controller.abort()
  t.mock.method(globalThis, 'fetch', async (_path: string, input: RequestInit) => {
    input.signal!.throwIfAborted()
    return new Response()
  })
  await assert.rejects(api.get('/canceled', controller.signal), {
    name: 'AbortError',
  })
})

test('session keys follow routes and server-reported service status', async (t) => {
  const saved = new Map<string, string>()
  const previousStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const previousLocation = Object.getOwnPropertyDescriptor(globalThis, 'location')
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => saved.get(key) ?? null,

      setItem: (key: string, value: string) => saved.set(key, value),

      removeItem: (key: string) => saved.delete(key),
    },
  })
  Object.defineProperty(globalThis, 'location', {
    configurable: true,
    value: { protocol: 'https:' },
  })
  t.after(() => {
    if (previousStorage) Object.defineProperty(globalThis, 'localStorage', previousStorage)
    else Reflect.deleteProperty(globalThis, 'localStorage')
    if (previousLocation) Object.defineProperty(globalThis, 'location', previousLocation)
    else Reflect.deleteProperty(globalThis, 'location')
  })
  let root = false
  let rejectKey = false
  const requests: RequestInit[] = []
  t.mock.method(globalThis, 'fetch', async (path: string, input: RequestInit) => {
    requests.push(input)
    if (path === '/api/state')
      return Response.json({
        mode: { public: true, service: root },
        account: null,
        csrf: 'public-csrf',
      })
    if (rejectKey) return Response.json({ code: 'session_key_required' }, { status: 401 })
    return new Response(null, { status: 204 })
  })
  await state()
  const key = saved.get('appleseum.sessionKey')!
  assert.equal(Buffer.from(key, 'base64url').length, 32)
  for (const [path, method] of [
    ['/state', 'GET'],
    ['/catalog?q=test', 'GET'],
    ['/profile', 'GET'],
    ['/library', 'GET'],
    ['/catalog/selection', 'POST'],
    ['/shares', 'POST'],
    ['/shares/stop', 'POST'],
    ['/library/export', 'POST'],
    ['/library/delete', 'POST'],
    ['/downloads/fixture/cancel', 'POST'],
    ['/preferences', 'PUT'],
    ['/admin/device', 'GET'],
    ['/admin/device', 'PUT'],
    ['/admin/device/defaults', 'POST'],
    ['/admin/device/reset', 'POST'],
  ] as const) {
    await request(method, path)
    assert.equal(new Headers(requests.at(-1)!.headers).has('X-Session-Key'), false, path)
  }
  for (const path of [
    '/login',
    '/catalog/refresh',
    '/profile/refresh',
    '/downloads?test=1',
    '/catalog/123/versions',
  ]) {
    await api.post(path, { email: 'person@example.test' })
    assert.equal(new Headers(requests.at(-1)!.headers).get('X-Session-Key'), key, path)
  }
  const login = { email: '  +7 (000) 000-00-00  ', password: 'unchanged' }
  await api.post('/login', login)
  assert.equal(new Headers(requests.at(-1)!.headers).get('X-Session-Key'), key)
  assert.deepEqual(JSON.parse(requests.at(-1)!.body as string), login)
  rejectKey = true
  const count = requests.length
  await assert.rejects(api.post('/catalog/refresh'), { code: 'session_key_required' })
  assert.equal(requests.length, count + 1)
  assert.equal(saved.size, 0)
  rejectKey = false
  await state()
  assert.notEqual(saved.get('appleseum.sessionKey'), key)
  root = true
  await state()
  assert.equal(saved.size, 0)
  await request('GET', '/library')
  assert.equal(new Headers(requests.at(-1)!.headers).has('X-Session-Key'), false)
  await logout()
  assert.equal(saved.size, 0)
})

test('a rejected public key is cleared and the next state poll requires a new sign-in', async (t) => {
  const saved = new Map([['appleseum.sessionKey', Buffer.alloc(32, 7).toString('base64url')]])
  const previousStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const previousLocation = Object.getOwnPropertyDescriptor(globalThis, 'location')
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: {
      getItem: (key: string) => saved.get(key) ?? null,

      setItem: (key: string, value: string) => saved.set(key, value),

      removeItem: (key: string) => saved.delete(key),
    },
  })
  Object.defineProperty(globalThis, 'location', {
    configurable: true,
    value: { protocol: 'https:' },
  })
  t.after(() => {
    if (previousStorage) Object.defineProperty(globalThis, 'localStorage', previousStorage)
    else Reflect.deleteProperty(globalThis, 'localStorage')
    if (previousLocation) Object.defineProperty(globalThis, 'location', previousLocation)
    else Reflect.deleteProperty(globalThis, 'location')
  })
  let signedIn = true
  let logouts = 0
  t.mock.method(globalThis, 'fetch', async (path: string) => {
    if (path === '/api/catalog/refresh')
      return Response.json({ code: 'session_key_required' }, { status: 401 })
    if (path === '/api/logout') {
      signedIn = false
      ++logouts
      return new Response(null, { status: 204 })
    }
    return Response.json({
      mode: { public: true, service: false },
      account: signedIn ? { id: 'synthetic-owner' } : null,
      csrf: 'public-csrf',
    })
  })
  assert.notEqual((await state()).account, null)
  await assert.rejects(api.post('/catalog/refresh'), { code: 'session_key_required' })
  assert.equal(saved.size, 0)
  const restored = await state()
  assert.equal(restored.account, null)
  assert.equal(logouts, 1)
  assert.equal(saved.size, 1)
})

test('a public session that cannot be recovered stops after one logout', async (t) => {
  const previousStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const previousLocation = Object.getOwnPropertyDescriptor(globalThis, 'location')
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: { getItem: () => null, removeItem() {} },
  })
  Object.defineProperty(globalThis, 'location', {
    configurable: true,
    value: { protocol: 'https:' },
  })
  t.after(() => {
    if (previousStorage) Object.defineProperty(globalThis, 'localStorage', previousStorage)
    else Reflect.deleteProperty(globalThis, 'localStorage')
    if (previousLocation) Object.defineProperty(globalThis, 'location', previousLocation)
    else Reflect.deleteProperty(globalThis, 'location')
  })
  const requests: string[] = []
  t.mock.method(globalThis, 'fetch', async (path: string) => {
    requests.push(path)
    if (path === '/api/logout') return new Response(null, { status: 204 })
    return Response.json({ mode: { public: true }, account: { id: 'owner' }, csrf: 'test' })
  })
  await assert.rejects(state(), { code: 'session_key_required' })
  assert.deepEqual(requests, ['/api/state', '/api/logout', '/api/state'])
})

test('malformed success JSON is rejected and reset headers survive parsing errors', async (t) => {
  const { credentials } = await import('../src/api/credentials.ts')
  credentials.key = 'synthetic-key'
  t.mock.method(
    globalThis,
    'fetch',
    async () =>
      new Response('not json', {
        headers: { 'Content-Type': 'application/json', 'X-Appleseum-Session-Reset': '1' },
      }),
  )
  await assert.rejects(api.get('/malformed'), { code: 'error' })
  assert.equal(credentials.key, '')
})
