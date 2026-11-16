import './httpEnvironment.ts'
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { setImmediate } from 'node:timers/promises'
import { effectScope, ref } from 'vue'
import { language } from '../src/i18n/index.ts'
import { theme, usePreferences } from '../src/composables/usePreferences.ts'
import type { Preferences, PreferencesInput, State } from '../src/api/types.ts'

test('rapid preference changes keep the latest choice and ignore stale polling while saving', async (t) => {
  const prior = Object.getOwnPropertyDescriptor(globalThis, 'document')
  const element = { dataset: {} as Record<string, string>, lang: '' }
  Object.defineProperty(globalThis, 'document', {
    configurable: true,
    value: { documentElement: element },
  })
  const scope = effectScope()
  t.after(() => {
    scope.stop()
    if (prior) Object.defineProperty(globalThis, 'document', prior)
    else Reflect.deleteProperty(globalThis, 'document')
  })
  const requests: { input: PreferencesInput; finish: (value: Preferences) => void }[] = []
  t.mock.method(
    globalThis,
    'fetch',
    (_path: string, init: RequestInit) =>
      new Promise<Response>((resolve) => {
        requests.push({
          input: JSON.parse(init.body as string),

          finish: (value) => resolve(Response.json(value)),
        })
      }),
  )
  const current = ref({ preferences: {} } as State)
  const errors: string[] = []
  const preferences = scope.run(() => usePreferences(current, (code) => errors.push(code)))!
  preferences.applyPreferences({ language: 'en', theme: 'dark', revision: 1 })
  preferences.toggleLanguage()
  preferences.toggleTheme()
  preferences.toggleLanguage()
  preferences.applyPreferences({ language: 'ru', theme: 'dark', revision: 2 })
  assert.equal(language.value, 'en')
  assert.equal(theme.value, 'light')
  assert.equal(element.dataset.theme, 'light')
  assert.equal(element.lang, 'en')
  assert.equal(requests.length, 1)
  requests[0]!.finish({ ...requests[0]!.input, revision: 2 })
  await setImmediate()
  assert.equal(requests.length, 2)
  assert.deepEqual(requests[1]!.input, { language: 'en', theme: 'light' })
  requests[1]!.finish({ ...requests[1]!.input, revision: 3 })
  await setImmediate()
  assert.deepEqual(current.value.preferences, { language: 'en', theme: 'light', revision: 3 })
  assert.deepEqual(errors, [])
})

test('changing sessions cancels old preferences and ignores their late reply', async (t) => {
  const prior = Object.getOwnPropertyDescriptor(globalThis, 'document')
  const element = { dataset: {} as Record<string, string>, lang: '' }
  Object.defineProperty(globalThis, 'document', {
    configurable: true,
    value: { documentElement: element },
  })
  const scope = effectScope()
  t.after(() => {
    scope.stop()
    if (prior) Object.defineProperty(globalThis, 'document', prior)
    else Reflect.deleteProperty(globalThis, 'document')
  })
  const requests: { signal: AbortSignal; finish: (value: Preferences) => void }[] = []
  t.mock.method(
    globalThis,
    'fetch',
    (_path: string, init: RequestInit) =>
      new Promise<Response>((resolve) => {
        requests.push({ signal: init.signal!, finish: (value) => resolve(Response.json(value)) })
      }),
  )
  const initial: Preferences = { language: 'en', theme: 'dark', revision: 1 }
  const current = ref({ preferences: initial } as State)
  const errors: string[] = []
  const preferences = scope.run(() => usePreferences(current, (code) => errors.push(code)))!
  preferences.applyPreferences(initial)
  preferences.toggleLanguage()
  preferences.toggleTheme()
  preferences.resetPreferences()
  assert.equal(requests[0]!.signal.aborted, true)
  current.value = { preferences: initial } as State
  preferences.applyPreferences(initial)
  preferences.toggleTheme()
  assert.equal(requests.length, 2)
  requests[0]!.finish({ language: 'ru', theme: 'dark', revision: 100 })
  await setImmediate()
  assert.deepEqual(current.value.preferences, initial)
  assert.equal(requests.length, 2)
  preferences.applyPreferences({ language: 'ru', theme: 'dark', revision: 200 })
  assert.equal(element.lang, 'en')
  assert.equal(element.dataset.theme, 'light')
  requests[1]!.finish({ language: 'en', theme: 'light', revision: 2 })
  await setImmediate()
  assert.deepEqual(current.value.preferences, { language: 'en', theme: 'light', revision: 2 })
  preferences.toggleLanguage()
  scope.stop()
  assert.equal(requests[2]!.signal.aborted, true)
  requests[2]!.finish({ language: 'ru', theme: 'dark', revision: 3 })
  await setImmediate()
  language.value = 'en'
  assert.equal(element.lang, 'ru')
  assert.deepEqual(current.value.preferences, { language: 'en', theme: 'light', revision: 2 })
  assert.deepEqual(errors, [])
})
