import { test } from 'node:test'
import assert from 'node:assert/strict'
import { nextTick, reactive, h, markRaw } from 'vue'
import { createTestRenderer, loadSetup } from './sfc.ts'

// Run the real dialog setup in Node, without a browser or a DOM dependency.
function setup(t: import('node:test').TestContext) {
  const cleanups: (() => void)[] = []
  const restores: (() => void)[] = []
  let counter = 0
  t.after(() => {
    for (const cleanup of cleanups.reverse()) cleanup()
    for (const restore of restores) restore()
  })
  class Element {
    constructor() {
      markRaw(this)
    }
    isConnected = true
    children: Element[] = []
    focused = false

    focus() {
      document.activeElement = this
      this.focused = true
    }

    getClientRects() {
      return [1]
    }

    contains(value: unknown) {
      return value === this || this.children.includes(value as Element)
    }

    querySelectorAll() {
      return this.children
    }
  }
  const document = Object.assign(new EventTarget(), {
    activeElement: null as Element | null,
    body: { style: { overflow: 'auto' } },
  })
  const window = new EventTarget()
  const history = {
    state: {} as Record<string, unknown>,

    pushState(value: Record<string, unknown>) {
      this.state = value
    },

    back() {
      this.state = {}
    },
  }
  for (const [key, value] of Object.entries({ document, window, history, HTMLElement: Element })) {
    const previous = Object.getOwnPropertyDescriptor(globalThis, key)
    Object.defineProperty(globalThis, key, { configurable: true, value })
    restores.push(() =>
      previous
        ? Object.defineProperty(globalThis, key, previous)
        : Reflect.deleteProperty(globalThis, key),
    )
  }
  const renderer = createTestRenderer(() => new Element())
  const component = loadSetup('components/Modal.vue')

  function mount(dismissible = true) {
    const closes: number[] = []
    const props = reactive({ dismissible })
    const app = renderer.createApp({
      render: () => h(component, { title: 'Dialog', ...props, onClose: () => closes.push(1) }, {}),
    })
    app.config.idPrefix = String(++counter)
    const instance = app.mount(new Element())
    const state = instance.$.subTree.component!.setupState
    const root = new Element()
    state.root = root
    let mounted = true

    const unmount = () => {
      if (mounted) {
        mounted = false
        app.unmount()
      }
    }

    cleanups.push(unmount)
    return { unmount, props, state, root, closes }
  }

  return { mount, document, Element }
}

function key(value: string, shiftKey = false) {
  return Object.assign(new Event('keydown', { cancelable: true }), { key: value, shiftKey })
}

test('a rejected dismissal does not prevent closing later; Tab from the dialog root enters its controls', async (t) => {
  const { mount, document, Element } = setup(t)
  const dialog = mount(false)
  document.dispatchEvent(key('Escape'))
  assert.equal(dialog.closes.length, 0)
  dialog.state.close()
  assert.equal(dialog.closes.length, 0)
  dialog.props.dismissible = true
  await nextTick()
  document.dispatchEvent(key('Escape'))
  assert.equal(dialog.closes.length, 1)
  const first = new Element(),
    last = new Element()
  dialog.root.children = [first, last]
  dialog.root.focus()
  const tab = key('Tab')
  document.dispatchEvent(tab)
  assert.equal(tab.defaultPrevented, true)
  assert.equal(document.activeElement, first)
  first.focus()
  document.dispatchEvent(key('Tab', true))
  assert.equal(document.activeElement, last)
  await nextTick()
})

test('only the top dialog handles Escape and scroll locking survives nested dialogs', (t) => {
  const { mount, document } = setup(t)
  const parent = mount(),
    child = mount()
  assert.equal(document.body.style.overflow, 'hidden')
  document.dispatchEvent(key('Escape'))
  assert.equal(parent.closes.length, 0)
  assert.equal(child.closes.length, 1)
  child.unmount()
  assert.equal(document.body.style.overflow, 'hidden')
  document.dispatchEvent(key('Escape'))
  assert.equal(parent.closes.length, 1)
  parent.unmount()
  assert.equal(document.body.style.overflow, 'auto')
})
