import assert from 'node:assert/strict'
import test from 'node:test'

test('the Axios demo adapter handles JSON, streams and blobs without a network request', async (t) => {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'location')
  Object.defineProperty(globalThis, 'location', {
    configurable: true,
    value: { search: '?demo', protocol: 'https:' },
  })
  t.after(() => {
    if (previous) Object.defineProperty(globalThis, 'location', previous)
    else Reflect.deleteProperty(globalThis, 'location')
  })
  t.mock.method(globalThis, 'fetch', () => {
    throw new Error('Unexpected demo network request')
  })
  const { api, request } = await import('../src/api/http.ts')
  const { fetchState: state, logout } = await import('../src/api/state.ts')
  const { loadVersionPage } = await import('../src/api/versions.ts')
  const account = (await state()).account
  assert.ok(account)
  await logout()
  assert.equal((await state()).account, null)
  await api.post('/login', { email: 'person@example.test', password: 'synthetic-password' })
  assert.equal((await state()).account?.id, account.id)
  const catalog = await api.get<{ items: { id: string }[] }>('/catalog?limit=5')
  const page = await loadVersionPage(
    '/catalog/' + catalog.items[0]!.id + '/versions',
    { limit: 5 },
    () => {},
    new AbortController().signal,
  )
  assert.ok(page.items.length > 0)
  const blob = await request<Blob>('GET', '/profile', { responseType: 'blob' })
  assert.ok(blob.data instanceof Blob)
  assert.ok(blob.data.size > 0)
  await assert.rejects(request('GET', '/unavailable', { responseType: 'blob' }), {
    code: 'operation_unavailable',
  })
  const canceled = new AbortController()
  canceled.abort()
  await assert.rejects(api.get('/state', canceled.signal), { name: 'AbortError' })
})
