import { getCurrentScope, onMounted } from 'vue'
import { useTimeoutPoll } from '@vueuse/core'

// Finish the initial read before starting the interval, even on a slow connection.
export function usePolling(read: () => Promise<void>, interval: number) {
  const scope = getCurrentScope()
  const { resume } = useTimeoutPoll(read, interval, { immediate: false })
  onMounted(async () => {
    await read()
    if (scope?.active) resume()
  })
}
