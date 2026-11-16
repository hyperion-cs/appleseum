import { computed, ref, watch, type Ref } from 'vue'
import { apiErrorCode } from '../api/errors.ts'
import { useAlive } from './lifetime.ts'

// Runs one user action at a time; a failure leaves its code in `error`.
export function useAction(error: Ref<string>, failed?: (code: string) => void) {
  const alive = useAlive()
  const running = ref('')
  const busy = computed(() => running.value !== '')

  async function run(action: () => Promise<unknown>, name = 'action') {
    if (busy.value) return
    running.value = name
    error.value = ''
    try {
      await action()
    } catch (e) {
      if (!alive()) return
      error.value = apiErrorCode(e)
      failed?.(error.value)
    } finally {
      running.value = ''
    }
  }

  return { running, busy, run }
}

// Background reads report each new failure once; clearing one keeps the shown error.
export function useReadError(error: Ref<string>) {
  const code = ref('')
  watch(code, (value) => {
    if (value) error.value = value
  })
  return code
}
