import './httpEnvironment.ts'
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { setImmediate } from 'node:timers/promises'
import { loadVersionPage } from '../src/api/versions.ts'
import { ApiError } from '../src/api/errors.ts'
import type { VersionPage } from '../src/api/types.ts'

const page: VersionPage = { name: 'Приложение', total: 0, items: [], next: false, previous: false }

const frame = (progress: number, result: VersionPage | null = null, error = '') =>
  JSON.stringify({ progress, page: result, error }) + '\n'

test('version progress arrives before the page and handles split UTF-8 frames', async (t) => {
  let source!: ReadableStreamDefaultController<Uint8Array>
  t.mock.method(globalThis, 'fetch', async (_path: string, input: RequestInit) => {
    assert.equal(new Headers(input.headers).get('Accept'), 'application/x-ndjson')
    assert.equal(input.credentials, 'same-origin')
    assert.equal(input.method, 'POST')
    return new Response(
      new ReadableStream<Uint8Array>({ start: (controller) => (source = controller) }),
      { headers: { 'Content-Type': 'application/x-ndjson; charset=utf-8' } },
    )
  })
  const values: number[] = []
  const request = loadVersionPage(
    '/catalog/1/versions',
    { cursor: '', limit: 5 },
    (value) => values.push(value),
    new AbortController().signal,
  )
  await setImmediate()
  const encoder = new TextEncoder()
  source.enqueue(encoder.encode(frame(0) + frame(7)))
  await setImmediate()
  assert.deepEqual(values, [0, 7])
  // A completed line can share a chunk with the beginning of the next one.
  const bytes = encoder.encode(frame(66) + frame(100, page))
  for (let i = 0; i < bytes.length; i += 3) source.enqueue(bytes.slice(i, i + 3))
  source.close()
  assert.deepEqual(await request, page)
  assert.deepEqual(values, [0, 7, 66, 100])
})

test('version streams retain server errors and reject truncated or malformed results', async (t) => {
  for (const [body, code] of [
    [frame(0) + frame(33, null, 'session_expired'), 'session_expired'],
    [frame(0), 'error'],
    ['not JSON\n', 'error'],
    ['null\n', 'error'],
  ]) {
    t.mock.method(
      globalThis,
      'fetch',
      async () => new Response(body, { headers: { 'Content-Type': 'application/x-ndjson' } }),
    )
    await assert.rejects(
      loadVersionPage(
        '/catalog/1/versions',
        { cursor: '', limit: 5 },
        () => {},
        new AbortController().signal,
      ),
      (error: unknown) => error instanceof ApiError && error.code === code,
    )
  }
})

test('version progress never moves backwards or completes before the page', async (t) => {
  t.mock.method(
    globalThis,
    'fetch',
    async () =>
      new Response(frame(50) + frame(30) + frame(100) + frame(100, page), {
        headers: { 'Content-Type': 'application/x-ndjson' },
      }),
  )
  const values: number[] = []
  await loadVersionPage(
    '/catalog/1/versions',
    { cursor: '', limit: 5 },
    (value) => values.push(value),
    new AbortController().signal,
  )
  assert.deepEqual(values, [50, 50, 99, 100])
})

test('version loading preserves JSON responses and cancellation', async (t) => {
  t.mock.method(globalThis, 'fetch', async () => Response.json(page))
  assert.deepEqual(
    await loadVersionPage(
      '/catalog/1/versions',
      { cursor: '', limit: 5 },
      () => {},
      new AbortController().signal,
    ),
    page,
  )
  let source!: ReadableStreamDefaultController<Uint8Array>
  t.mock.method(globalThis, 'fetch', async (_path: string, input: RequestInit) => {
    const body = new ReadableStream<Uint8Array>({ start: (controller) => (source = controller) })
    input.signal!.addEventListener('abort', () =>
      source.error(new DOMException('Canceled', 'AbortError')),
    )
    return new Response(body, { headers: { 'Content-Type': 'application/x-ndjson' } })
  })
  const controller = new AbortController()
  const pending = loadVersionPage(
    '/catalog/1/versions',
    { cursor: '', limit: 5 },
    () => {},
    controller.signal,
  )
  await setImmediate()
  controller.abort()
  await assert.rejects(pending, { name: 'AbortError' })
})
