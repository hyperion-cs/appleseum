import assert from 'node:assert/strict'
import { test, type TestContext } from 'node:test'
import { copyText, downloadFile, randomId } from '../src/platform/browser.ts'

function globals(t: TestContext, values: Record<string, unknown>) {
  for (const [name, value] of Object.entries(values)) {
    const prior = Object.getOwnPropertyDescriptor(globalThis, name)
    Object.defineProperty(globalThis, name, { value, configurable: true })
    t.after(() => {
      if (prior) Object.defineProperty(globalThis, name, prior)
      else Reflect.deleteProperty(globalThis, name)
    })
  }
}

test('failed browser downloads release temporary elements and blob URLs', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  let removed = false
  const link = {
    href: '',
    download: '',

    click() {
      throw new Error('blocked')
    },

    remove() {
      removed = true
    },
  }
  globals(t, { document: { body: { append() {} }, createElement: () => link } })
  t.mock.method(URL, 'createObjectURL', () => 'blob:download')
  const revoke = t.mock.method(URL, 'revokeObjectURL', () => {})
  assert.throws(() => downloadFile(new Blob(['data']), 'file.har'), /blocked/)
  assert.equal(link.href, 'blob:download')
  assert.equal(link.download, 'file.har')
  assert.equal(removed, true)
  t.mock.timers.tick(1_000)
  assert.equal(revoke.mock.calls[0]?.arguments[0], 'blob:download')
})

test('native clipboard success needs no fallback element', async (t) => {
  const copied: string[] = []
  globals(t, {
    window: { isSecureContext: true },
    navigator: { clipboard: { writeText: async (value: string) => copied.push(value) } },
    document: {},
  })
  await copyText('installation link')
  assert.deepEqual(copied, ['installation link'])
})

test('denied clipboard access falls back and restores focus even when copying fails', async (t) => {
  let removed = false
  let focused = false
  const input = {
    value: '',

    setAttribute() {},

    focus() {},

    select() {},

    setSelectionRange() {},

    remove() {
      removed = true
    },
  }
  globals(t, {
    window: { isSecureContext: true },
    navigator: {
      clipboard: {
        writeText: async () => {
          throw new Error('denied')
        },
      },
    },
    document: {
      activeElement: {
        focus: () => {
          focused = true
        },
      },
      body: { append() {} },

      createElement: () => input,

      execCommand: () => false,
    },
  })
  await assert.rejects(copyText('installation link'), /copy/)
  assert.equal(input.value, 'installation link')
  assert.equal(removed, true)
  assert.equal(focused, true)
})

test('ID generation uses the native crypto API when available', (t) => {
  const id = '01900000-0000-4000-8000-000000000001'
  t.mock.method(crypto, 'randomUUID', () => id)
  assert.equal(randomId(), id)
})

test('ID generation works on local HTTP without randomUUID', (t) => {
  globals(t, { crypto: { getRandomValues: (bytes: Uint8Array) => bytes.fill(255) } })
  assert.equal(randomId(), 'ffffffff-ffff-4fff-bfff-ffffffffffff')
})
