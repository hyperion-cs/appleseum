import { getCurrentScope, onActivated, onDeactivated, onScopeDispose, ref } from 'vue'

// Reports whether the owning component or effect scope is still in use.
export function useAlive() {
  const scope = getCurrentScope()
  return () => scope?.active === true
}

// Only the newest run of a live scope may apply its result.
export function useLatest() {
  let serial = 0
  onScopeDispose(() => ++serial)
  return {
    start() {
      const run = ++serial
      return () => run === serial
    },

    cancel() {
      ++serial
    },
  }
}

// Whether a component kept alive by <KeepAlive> is on screen.
export function useActive() {
  const active = ref(true)
  onActivated(() => (active.value = true))
  onDeactivated(() => (active.value = false))
  return active
}
