import './httpEnvironment.ts'
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { setImmediate } from 'node:timers/promises'
import { ref } from 'vue'
import { useShares } from '../src/composables/useShares.ts'
import type { State } from '../src/api/types.ts'
import { createTestRenderer } from './sfc.ts'

test('unmount discards queued hosting table writes', async (t) => {
  const prior = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: { getItem: () => null },
  })
  t.after(() => {
    if (prior) Object.defineProperty(globalThis, 'localStorage', prior)
    else Reflect.deleteProperty(globalThis, 'localStorage')
  })
  const requests: Array<{ path: string; finish: (value: unknown) => void }> = []
  t.mock.method(
    globalThis,
    'fetch',
    (path: string) =>
      new Promise<Response>((resolve) => {
        requests.push({ path, finish: (value) => resolve(Response.json(value)) })
      }),
  )
  const renderer = createTestRenderer()
  let shares!: ReturnType<typeof useShares>
  const app = renderer.createApp({
    setup() {
      shares = useShares(
        { state: { tables: {}, shareCount: 0 } as State, active: false },
        { changed() {}, stopped() {} },
        ref(''),
      )
      return () => null
    },
  })
  app.mount({})
  let mounted = true
  t.after(() => {
    if (mounted) app.unmount()
  })
  shares.changeSort('name')
  await setImmediate()
  assert.equal(requests[0]?.path, '/api/table-settings/shares')
  shares.changeSort('createdAt')
  app.unmount()
  mounted = false
  requests[0]!.finish({ revision: 1 })
  await setImmediate()
  assert.equal(requests.length, 1)
})
