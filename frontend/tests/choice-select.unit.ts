import { test, type TestContext } from 'node:test'
import assert from 'node:assert/strict'
import { h, nextTick, ref } from 'vue'
import { createTestRenderer, loadSetup } from './sfc.ts'

function mount(t: TestContext, file: string, initial: string | string[]) {
  const renderer = createTestRenderer()
  const component = loadSetup(`components/${file}.vue`)
  const value = ref(initial)
  const events = { choices: [] as string[], closes: 0 }
  const options = [
    { value: '', label: 'All' },
    { value: 'Apple', label: 'Apple' },
    { value: 'API', label: 'API' },
  ]
  const app = renderer.createApp({
    render: () =>
      h(component, {
        modelValue: value.value,
        label: 'Sources',
        clearValue: '',
        mobile: false,
        options,

        'onUpdate:modelValue': (next: string | string[]) => {
          value.value = next
        },

        onChoose: (value: string) => events.choices.push(value),

        onClose: () => {
          events.closes++
        },
      }),
  })
  const instance = app.mount({})
  t.after(() => app.unmount())
  const child = instance.$.subTree.component!
  return { state: child.setupState, value, events }
}

test('multiple choice toggles independently, stays open and resets to all', async (t) => {
  const { state, value } = mount(t, 'ChoiceSelect', [])
  let closes = 0
  for (const [choice, expected] of [
    ['Apple', ['Apple']],
    ['API', ['Apple', 'API']],
    ['Apple', ['API']],
    ['', []],
    ['API', ['API']],
    ['API', []],
  ] as const) {
    state.choose(choice, () => closes++)
    await nextTick()
    assert.deepEqual(value.value, [...expected])
    assert.equal(closes, 0)
  }
})

test('single choice keeps its previous select-and-close behavior', async (t) => {
  const { state, value } = mount(t, 'ChoiceSelect', '')
  let closes = 0
  state.choose('Apple', () => closes++)
  await nextTick()
  assert.equal(value.value, 'Apple')
  assert.equal(state.selected, 'Apple')
  assert.equal(closes, 1)
})

test('multiple choices keep keyboard position when their selection changes', async (t) => {
  const { state, value, events } = mount(t, 'ChoiceOptions', [])
  assert.equal(state.selected(''), true)
  state.active = 2
  value.value = ['Apple', 'API']
  await nextTick()
  assert.equal(state.active, 2)
  assert.equal(state.selected(''), false)
  assert.equal(state.selected('Apple'), true)
  assert.equal(state.selected('API'), true)

  const key = (key: string) => ({ key, target: {}, preventDefault() {}, stopPropagation() {} })

  state.key(key(' '))
  assert.deepEqual(events.choices, ['API'])
  state.key(key('ArrowUp'))
  assert.equal(state.active, 1)
  state.key(key('Enter'))
  assert.deepEqual(events.choices, ['API', 'Apple'])
  state.key(key('Escape'))
  assert.equal(events.closes, 1)
  value.value = []
  await nextTick()
  assert.equal(state.selected(''), true)
})
