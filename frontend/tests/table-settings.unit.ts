import './httpEnvironment.ts'
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { setImmediate } from 'node:timers/promises'
import { effectScope, ref } from 'vue'
import { useTableSettings } from '../src/composables/useTableSettings.ts'
import type { TableSettingsInput, Tables } from '../src/api/types.ts'

const initial: TableSettingsInput = { sort: 'name', direction: 'asc', delisted: false, mine: false }

test('table saves coalesce to the latest value and preserve newer revisions', async (t) => {
  const requests: Array<{ value: TableSettingsInput; finish: (value: unknown) => void }> = []
  t.mock.method(
    globalThis,
    'fetch',
    (_path: string, input: RequestInit) =>
      new Promise<Response>((resolve) => {
        requests.push({
          value: JSON.parse(input.body as string),
          finish: (value) => resolve(Response.json(value)),
        })
      }),
  )
  const tables = ref<Partial<Tables>>({ catalog: { revision: 1, value: initial } })
  const changes: TableSettingsInput[] = []
  const error = ref('')
  const scope = effectScope()
  const { save } = scope.run(() =>
    useTableSettings(
      () => tables.value,
      (_, value) => changes.push(value),
      error,
    ),
  )!
  t.after(() => scope.stop())
  save('catalog', initial)
  save('catalog', { ...initial, sort: 'status' })
  save('catalog', { ...initial, sort: 'purchasedAt' })
  tables.value = { catalog: { revision: 2, value: initial } }
  await setImmediate()
  assert.deepEqual(changes, [])
  requests[0]!.finish({ revision: 3 })
  await setImmediate()
  assert.equal(requests.length, 2)
  assert.equal(requests[1]!.value.sort, 'purchasedAt')
  requests[1]!.finish({ revision: 4 })
  await setImmediate()
  tables.value = { catalog: { revision: 3, value: initial } }
  await setImmediate()
  assert.deepEqual(changes, [])
  tables.value.catalog = { revision: 5, value: { ...initial, direction: 'desc' } }
  await setImmediate()
  assert.deepEqual(changes, [{ ...initial, direction: 'desc' }])
  assert.equal(error.value, '')
})
