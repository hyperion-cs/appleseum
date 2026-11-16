import { test } from 'node:test'
import assert from 'node:assert/strict'
import { scrollbarGeometry } from '../src/app/persistentScrollbars.ts'

test('scroll thumb represents viewport size and tracks both ends and the middle', () => {
  assert.deepEqual(scrollbarGeometry(100, 400, 0, 100), { length: 25, position: 0 })
  assert.deepEqual(scrollbarGeometry(100, 400, 150, 100), { length: 25, position: 37.5 })
  assert.deepEqual(scrollbarGeometry(100, 400, 300, 100), { length: 25, position: 75 })
})

test('thumb stays usable for long content and clamps Safari rubber-band overscroll', () => {
  assert.deepEqual(scrollbarGeometry(100, 10_000, -50, 100), { length: 24, position: 0 })
  assert.deepEqual(scrollbarGeometry(100, 10_000, 11_000, 100), { length: 24, position: 76 })
  assert.deepEqual(scrollbarGeometry(100, 100, 0, 100), { length: 100, position: 0 })
  assert.deepEqual(scrollbarGeometry(0, 100, 0, 0), { length: 0, position: 0 })
})
