import assert from 'node:assert/strict'
import { test, type TestContext } from 'node:test'
import { setImmediate } from 'node:timers/promises'
import { usePolling } from '../src/composables/usePolling.ts'
import { createTestRenderer } from './sfc.ts'

function mount(t: TestContext, read: () => Promise<void>) {
  const renderer = createTestRenderer()
  const app = renderer.createApp({
    setup() {
      usePolling(read, 100)
      return () => null
    },
  })
  app.mount({})
  let mounted = true
  function unmount() {
    if (mounted) app.unmount()
    mounted = false
  }
  t.after(unmount)
  return unmount
}

test('polling waits for each request, including a slow initial read', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const complete: Array<() => void> = []
  const unmount = mount(t, () => new Promise<void>((resolve) => complete.push(resolve)))
  assert.equal(complete.length, 1)
  t.mock.timers.tick(1_000)
  assert.equal(complete.length, 1)
  complete[0]!()
  await setImmediate()
  t.mock.timers.tick(100)
  assert.equal(complete.length, 2)
  t.mock.timers.tick(1_000)
  assert.equal(complete.length, 2)
  unmount()
  complete[1]!()
  await setImmediate()
  t.mock.timers.tick(1_000)
  assert.equal(complete.length, 2)
})

test('a late initial response cannot restart polling after unmount', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  let finish!: () => void
  let calls = 0
  const unmount = mount(t, () => {
    calls++
    return new Promise<void>((resolve) => {
      finish = resolve
    })
  })
  unmount()
  finish()
  await setImmediate()
  t.mock.timers.tick(1_000)
  assert.equal(calls, 1)
})
