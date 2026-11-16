import test from 'node:test'
import assert from 'node:assert/strict'
import { effectScope, ref } from 'vue'
import { maxSelectionSize } from '../src/api/types.ts'
import { useSelectionSummary } from '../src/composables/useSelectionSummary.ts'

test('the header tracks the full matching selection, not the visible page', () => {
  const scope = effectScope()
  try {
    const selected = ref(new Set(['page-one'])),
      query = ref('')
    const summary = scope.run(() => useSelectionSummary(selected, () => query.value))!
    assert.equal(summary.allSelected.value, false)
    assert.equal(summary.someSelected.value, true)
    selected.value = new Set(['page-one', 'page-two', 'page-three'])
    summary.rememberAll()
    assert.equal(summary.allSelected.value, true)
    selected.value.delete('page-two')
    assert.equal(summary.allSelected.value, false)
    assert.equal(summary.someSelected.value, true)
    selected.value.add('page-two')
    assert.equal(summary.allSelected.value, true)
    query.value = 'another filter'
    assert.equal(summary.allSelected.value, false)
    selected.value = new Set(Array.from({ length: maxSelectionSize }, (_, i) => String(i)))
    assert.equal(summary.toggled('0')?.size, maxSelectionSize - 1)
    assert.equal(summary.toggled('off-page'), null)
    selected.value.clear()
    assert.equal(summary.someSelected.value, false)
  } finally {
    scope.stop()
  }
})
