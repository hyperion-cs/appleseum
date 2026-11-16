import { test } from 'node:test'
import assert from 'node:assert/strict'
import { h, nextTick, reactive } from 'vue'
import { minSearchLength } from '../src/api/types.ts'
import { createTestRenderer, loadSetup } from './sfc.ts'

// Exercise the actual component's model updates without browser rendering.
function setup(t: import('node:test').TestContext, minimum?: number) {
  const renderer = createTestRenderer()
  const component = loadSetup('components/TableToolbar.vue')
  const props = reactive({ query: '', minSearchLength: minimum })
  const emitted: string[] = []
  const app = renderer.createApp({
    render: () =>
      h(component, {
        ...props,
        searchLabel: 'Search',

        'onUpdate:query': (value: string) => {
          emitted.push(value)
          props.query = value
        },
      }),
  })
  const instance = app.mount({})
  t.after(() => app.unmount())
  return { state: instance.$.subTree.component!.setupState, props, emitted }
}

test('table search emits only empty or sufficiently long Unicode queries', async (t) => {
  const { state, props, emitted } = setup(t, minSearchLength)
  for (const value of ['a', 'ab', 'я', 'ёж', '🙂', '🙂🙂']) {
    state.draft = value
    await nextTick()
  }
  assert.deepEqual(emitted, [])
  state.draft = '🙂🙂я'
  await nextTick()
  assert.deepEqual(emitted, ['🙂🙂я'])
  state.draft = 'ab'
  await nextTick()
  assert.deepEqual(emitted, ['🙂🙂я', ''])
  assert.equal(state.draft, 'ab')
  state.draft = 'a'
  await nextTick()
  assert.deepEqual(emitted, ['🙂🙂я', ''])
  props.query = 'restored'
  await nextTick()
  assert.equal(state.draft, 'restored')
  state.draft = ''
  await nextTick()
  assert.equal(emitted.at(-1), '')
})

test('tables without a search minimum retain their existing input behavior', async (t) => {
  const { state, emitted } = setup(t)
  state.draft = 'a'
  await nextTick()
  assert.deepEqual(emitted, ['a'])
})
