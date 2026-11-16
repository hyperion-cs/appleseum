import { test, type TestContext } from 'node:test'
import assert from 'node:assert/strict'
import { setImmediate } from 'node:timers/promises'
let uploadIpa: typeof import('../src/api/upload.ts').uploadIpa
let state: typeof import('../src/api/state.ts').fetchState

class UploadEvents extends EventTarget {
  listeners = new Set<EventListenerOrEventListenerObject>()

  addEventListener(
    type: string,
    listener: EventListenerOrEventListenerObject | null,
    options?: AddEventListenerOptions | boolean,
  ) {
    if (listener) this.listeners.add(listener)
    super.addEventListener(type, listener, options)
  }

  removeEventListener(
    type: string,
    listener: EventListenerOrEventListenerObject | null,
    options?: EventListenerOptions | boolean,
  ) {
    if (listener) this.listeners.delete(listener)
    super.removeEventListener(type, listener, options)
  }
}

class FakeXHR {
  static instances: FakeXHR[] = []
  upload = new UploadEvents()
  onloadend: ((event: Event) => void) | null = null
  onerror: (() => void) | null = null
  ontimeout: (() => void) | null = null
  onabort: (() => void) | null = null
  status = 0
  statusText = ''
  responseText = ''
  responseType = ''
  responseHeaders = ''
  headers: Record<string, string> = {}
  body: Blob | null = null
  method = ''
  url = ''
  aborted = false

  constructor() {
    FakeXHR.instances.push(this)
  }

  open(method: string, url: string) {
    this.method = method
    this.url = url
  }

  setRequestHeader(name: string, value: string) {
    this.headers[name] = value
  }

  getAllResponseHeaders() {
    return this.responseHeaders
  }

  send(body: Blob) {
    this.body = body
  }

  abort() {
    this.aborted = true
    this.onabort?.()
  }

  progress(loaded: number, total: number, lengthComputable = true) {
    this.upload.dispatchEvent(
      Object.assign(new Event('progress'), { loaded, total, lengthComputable }),
    )
  }

  complete(status: number, response: unknown) {
    this.status = status
    this.responseText = JSON.stringify(response)
    this.onloadend?.(new Event('loadend'))
  }
}

async function install(t: TestContext) {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'XMLHttpRequest')
  FakeXHR.instances = []
  Object.defineProperty(globalThis, 'XMLHttpRequest', { configurable: true, value: FakeXHR })
  await import('./httpEnvironment.ts')
  ;({ fetchState: state } = await import('../src/api/state.ts'))
  ;({ uploadIpa } = await import('../src/api/upload.ts'))
  t.after(() => {
    if (original) Object.defineProperty(globalThis, 'XMLHttpRequest', original)
    else Reflect.deleteProperty(globalThis, 'XMLHttpRequest')
  })
}

const id = '01900000-0000-7000-8000-000000000002'

const file = () => new File(['x'.repeat(1_000)], 'existing.ipa')

test('browser progress reports file fraction and speed before server acknowledgement', async (t) => {
  await install(t)
  t.mock.method(globalThis, 'fetch', async () =>
    Response.json({ csrf: 'upload-csrf', mode: { public: false } }),
  )
  await state()
  let clock = 1_000
  t.mock.method(Date, 'now', () => clock)
  const events: Array<{ received: number; total: number; speed: number; status: string }> = []
  const controller = new AbortController()
  let acknowledged = false
  const pending = uploadIpa(file(), id, (event) => events.push(event), controller.signal)
  pending.then(() => {
    acknowledged = true
  })
  await setImmediate()
  const xhr = FakeXHR.instances[0]!
  assert.equal(xhr.method, 'PUT')
  assert.equal(xhr.url, '/api/library/imports/' + id)
  assert.equal(xhr.headers['X-CSRF-Token'], 'upload-csrf')
  assert.equal(xhr.headers['Content-Disposition'], "attachment; filename*=UTF-8''existing.ipa")
  assert.equal((xhr.body as File).name, 'existing.ipa')
  clock = 1_000
  xhr.progress(250, 1_000)
  assert.deepEqual(events.at(-1), {
    received: 250,
    total: 1_000,
    speed: 0,
    status: 'downloading',
  })
  clock = 2_000
  xhr.progress(750, 1_000)
  assert.equal(events.at(-1)?.received, 750)
  assert.equal(events.at(-1)?.total, 1_000)
  assert.equal(events.at(-1)?.status, 'downloading')
  assert.ok(Number.isFinite(events.at(-1)!.speed) && events.at(-1)!.speed > 0)
  xhr.progress(1_000, 1_000)
  xhr.upload.dispatchEvent(new Event('loadend'))
  assert.deepEqual(events.at(-1), { received: 1_000, total: 1_000, speed: 0, status: 'preparing' })
  const count = events.length
  xhr.progress(500, 1_000)
  assert.equal(events.length, count)
  await Promise.resolve()
  assert.equal(acknowledged, false)
  xhr.complete(201, { id })
  await pending
  assert.equal(acknowledged, true)
  controller.abort()
  assert.equal(xhr.aborted, false)
})

test('upload progress without a computable total stays bounded and monotonic', async (t) => {
  await install(t)
  let clock = 1_000
  t.mock.method(Date, 'now', () => clock)
  const values: number[] = []
  const pending = uploadIpa(
    file(),
    id,
    (value) => {
      values.push(value.received)
      assert.ok(Number.isFinite(value.speed))
    },
    new AbortController().signal,
  )
  await setImmediate()
  const xhr = FakeXHR.instances[0]!
  xhr.progress(700, 0, false)
  clock += 500
  xhr.progress(100, 0, false)
  clock += 500
  xhr.progress(2_000, 0, false)
  assert.deepEqual(values, [700, 700, 1_000])
  xhr.complete(201, { id })
  await pending
})

test('upload completion without a computable total enters preparation before acknowledgement', async (t) => {
  await install(t)
  const stages: string[] = []
  const pending = uploadIpa(
    file(),
    id,
    (value) => stages.push(value.status),
    new AbortController().signal,
  )
  await setImmediate()
  const xhr = FakeXHR.instances[0]!
  xhr.progress(700, 0, false)
  xhr.upload.dispatchEvent(new Event('load'))
  assert.equal(stages.at(-1), 'preparing')
  xhr.complete(201, { id })
  await pending
  assert.equal(xhr.upload.listeners.size, 0)
})

test('upload errors preserve API codes and cancellation releases listeners', async (t) => {
  await install(t)
  for (const [status, response, code] of [
    [400, { code: 'invalid_ipa' }, 'invalid_ipa'],
    [502, null, 'import_failed'],
    [413, { code: 'import_too_large' }, 'import_too_large'],
  ] as const) {
    const pending = uploadIpa(file(), id, () => {}, new AbortController().signal)
    await setImmediate()
    FakeXHR.instances.at(-1)!.complete(status, response)
    await assert.rejects(pending, { code })
  }
  for (const kind of ['onerror', 'ontimeout'] as const) {
    const pending = uploadIpa(file(), id, () => {}, new AbortController().signal)
    await setImmediate()
    FakeXHR.instances.at(-1)![kind]?.()
    await assert.rejects(pending, { code: 'import_failed' })
  }
  const controller = new AbortController()
  const pending = uploadIpa(file(), id, () => {}, controller.signal)
  await setImmediate()
  const xhr = FakeXHR.instances.at(-1)!
  controller.abort()
  await assert.rejects(pending, { name: 'AbortError' })
  assert.equal(xhr.aborted, true)
  assert.equal(xhr.upload.listeners.size, 0)
  const count = FakeXHR.instances.length
  await assert.rejects(
    uploadIpa(file(), id, () => {}, controller.signal),
    { name: 'AbortError' },
  )
  assert.equal(FakeXHR.instances.length, count)
})

test('import responses clear a reset session key on success and failure', async (t) => {
  await install(t)
  const { credentials } = await import('../src/api/credentials.ts')
  for (const status of [201, 401]) {
    credentials.key = 'synthetic-key'
    const pending = uploadIpa(file(), id, () => {}, new AbortController().signal)
    await setImmediate()
    const xhr = FakeXHR.instances.at(-1)!
    xhr.responseHeaders = 'X-Appleseum-Session-Reset: 1'
    xhr.complete(status, { code: 'sign_in_required' })
    if (status === 201) await pending
    else await assert.rejects(pending, { code: 'sign_in_required' })
    assert.equal(credentials.key, '')
  }
})
