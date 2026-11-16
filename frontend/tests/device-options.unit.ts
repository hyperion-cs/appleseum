import { test } from 'node:test'
import assert from 'node:assert/strict'
import { deviceOptions } from '../src/views/deviceOptions.ts'

test('device choices include defaults and preserve stored values outside the lists', () => {
  const stored = {
    locale: 'en-EU',
    timeZone: 'Etc/GMT+3',
  }
  const choices = deviceOptions(stored)
  for (const key of ['locale', 'timeZone'] as const) {
    assert.equal(choices[key].filter((option) => option.value === stored[key]).length, 1)
    assert.equal(new Set(choices[key].map((option) => option.value)).size, choices[key].length)
  }
  assert(choices.locale.some((option) => option.value === 'en-US'))
  assert(choices.locale.some((option) => option.value === 'ru-KZ'))
  assert(choices.timeZone.some((option) => option.value === 'UTC'))
  assert(choices.timeZone.some((option) => option.value === 'Asia/Almaty'))
})
