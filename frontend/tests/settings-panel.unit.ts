import './httpEnvironment.ts'
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { setImmediate } from 'node:timers/promises'
import { effectScope, ref } from 'vue'
import type { State } from '../src/api/types.ts'
import { useDeviceSettings } from '../src/composables/useDeviceSettings.ts'

test('local settings stay editable after sign-in and confirm identity changes', async (t) => {
  const settings = { model: 'fixture', osVersion: 'fixture', locale: 'en-US', timeZone: 'UTC' }
  const calls: string[] = []
  let changes = 0
  t.mock.method(globalThis, 'fetch', async (_path: string, input?: RequestInit) => {
    const method = input?.method ?? 'GET'
    calls.push(method)
    const body = method === 'PUT' ? JSON.parse(input!.body as string) : settings
    return Response.json({
      settings: { ...body },
      defaults: { ...settings },
      models: [],
      osVersions: [],
    })
  })
  const state = ref({
    mode: { public: false },
    account: null,
    auth: { state: 'code', busy: false, error: '' },
    downloadsActive: false,
  } as unknown as State)
  const scope = effectScope()
  t.after(() => scope.stop())
  const device = scope.run(() => useDeviceSettings(state, ref(''), () => changes++))!
  await device.load()
  assert.equal(device.locked.value, false)
  device.form.value!.locale = 'en-GB'
  assert.equal(device.locked.value, false)
  await device.update('save')
  assert.deepEqual(calls, ['GET', 'PUT'])
  assert.equal(changes, 1)
  state.value.auth.busy = true
  assert.equal(device.locked.value, true)
  state.value.auth.busy = false
  state.value.downloadsActive = true
  assert.equal(device.locked.value, true)
  state.value.downloadsActive = false
  state.value.account = { id: 'synthetic-owner' } as State['account']
  assert.equal(device.locked.value, true)
  state.value.auth.state = 'ready'
  assert.equal(device.locked.value, false)
  device.form.value!.timeZone = 'Europe/London'
  device.requestUpdate('save')
  await setImmediate()
  assert.deepEqual(calls, ['GET', 'PUT', 'PUT'])
  assert.equal(changes, 2)
  device.form.value!.model = 'another-fixture'
  device.requestUpdate('save')
  assert.equal(device.confirming.value, 'save')
  assert.deepEqual(calls, ['GET', 'PUT', 'PUT'])
  device.confirming.value = ''
  assert.deepEqual(calls, ['GET', 'PUT', 'PUT'])
  await device.update('save')
  assert.equal(changes, 3)
})
