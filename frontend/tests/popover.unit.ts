import assert from 'node:assert/strict'
import { test, type TestContext } from 'node:test'
import { effectScope, markRaw, nextTick, shallowRef } from 'vue'

class Target extends EventTarget {
  listeners = new Map<string, Set<EventListenerOrEventListenerObject>>()

  override addEventListener(
    type: string,
    listener: EventListenerOrEventListenerObject | null,
    options?: AddEventListenerOptions | boolean,
  ) {
    if (listener) {
      if (!this.listeners.has(type)) this.listeners.set(type, new Set())
      this.listeners.get(type)!.add(listener)
    }
    super.addEventListener(type, listener, options)
  }

  override removeEventListener(
    type: string,
    listener: EventListenerOrEventListenerObject | null,
    options?: EventListenerOptions | boolean,
  ) {
    if (listener) this.listeners.get(type)?.delete(listener)
    super.removeEventListener(type, listener, options)
  }

  count() {
    return [...this.listeners.values()].reduce((total, values) => total + values.size, 0)
  }
}

const media = Object.assign(new Target(), { matches: false })
const windowTarget = Object.assign(new Target(), { matchMedia: () => media })

async function setup(t: TestContext) {
  const globals = { window: windowTarget, document: new Target(), Element: Target }
  for (const [name, value] of Object.entries(globals)) {
    const before = Object.getOwnPropertyDescriptor(globalThis, name)
    Object.defineProperty(globalThis, name, { configurable: true, value })
    t.after(() => {
      if (before) Object.defineProperty(globalThis, name, before)
      else Reflect.deleteProperty(globalThis, name)
    })
  }
  media.matches = false
  let focuses = 0
  const trigger = markRaw({
    focus: () => {
      focuses++
    },
    contains: (node: unknown) => node === trigger,
  })
  const { usePopover } = await import('../src/components/usePopover.ts')
  const scope = effectScope()
  const popover = scope.run(() =>
    usePopover(
      shallowRef(trigger as unknown as HTMLButtonElement),
      shallowRef<HTMLElement | null>(null),
      192,
    ),
  )!
  t.after(() => scope.stop())
  await nextTick()
  return { popover, scope, trigger, focuses: () => focuses }
}

test('popover closes with Escape and returns focus to its trigger', async (t) => {
  const { popover, focuses } = await setup(t)
  popover.open.value = true
  assert.equal(popover.position.value.position, 'fixed')
  assert.equal(popover.position.value.opacity, 0)
  const event = new Event('keydown', { cancelable: true })
  Object.defineProperty(event, 'key', { value: 'Escape' })
  popover.key(event as KeyboardEvent)
  assert.equal(event.defaultPrevented, true)
  assert.equal(popover.open.value, false)
  assert.equal(focuses(), 1)
})

test('focus leaving a popover does not steal focus back; changing device layout closes it', async (t) => {
  const { popover, trigger, focuses } = await setup(t)
  popover.open.value = true
  popover.blur({ relatedTarget: trigger } as unknown as FocusEvent)
  assert.equal(popover.open.value, true)
  popover.blur({ relatedTarget: {} } as FocusEvent)
  assert.equal(popover.open.value, false)
  assert.equal(focuses(), 0)
  popover.open.value = true
  media.matches = true
  const event = Object.assign(new Event('change'), { matches: true })
  media.dispatchEvent(event)
  await nextTick()
  assert.equal(popover.mobile.value, true)
  assert.equal(popover.open.value, false)
})

test('disposing a popover releases media and outside-click listeners', async (t) => {
  const { scope } = await setup(t)
  assert(media.count() > 0)
  assert(windowTarget.count() > 0)
  scope.stop()
  await nextTick()
  assert.equal(media.count(), 0)
  assert.equal(windowTarget.count(), 0)
})
