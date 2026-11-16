import { computed, onScopeDispose, ref, shallowRef } from 'vue'
import { useTimeoutFn } from '@vueuse/core'
import { api } from '../api/http.ts'
import type { InstallationProgress } from '../api/types.ts'
import { installationLink, installationTrace } from '../platform/installation.ts'
import { useAlive } from './lifetime.ts'

const pollIntervalMs = 1_000
const warningDelayMs = 10_000
// Consecutive failed reads before the status counts as unavailable.
const maxPollFailures = 3

// The dialog observes only the attempt started here. Copying a reusable link
// never calls start, and closing the scope leaves the actual transfer alone.
export function useInstallation(shareId: () => string) {
  const alive = useAlive()
  const requested = ref(false)
  const delayed = ref(false)
  const failures = ref(0)
  const expired = ref(false)
  const progress = shallowRef<InstallationProgress | null>(null)
  let trace = ''
  let controller: AbortController | undefined
  const warningTimer = useTimeoutFn(() => (delayed.value = true), warningDelayMs, {
    immediate: false,
  })
  const poll = useTimeoutFn(() => void refresh(), pollIntervalMs, { immediate: false })

  const stage = computed(() => progress.value?.stage ?? 'waiting')
  // An error ends observation and remains visible in the installation status block.
  const error = computed(() => {
    if (stage.value === 'complete') return ''
    if (stage.value === 'failed') return 'installation_manifest_failed'
    if (expired.value) return 'installation_status_expired'
    if (failures.value >= maxPollFailures) return 'installation_status_unavailable'
    return ''
  })
  // A warning is a delay the device may still recover from; observation goes on.
  const warning = computed(() => {
    if (error.value) return ''
    if (stage.value === 'interrupted') return 'installation_interrupted'
    if (!delayed.value) return ''
    if (stage.value === 'waiting') return 'installation_no_manifest'
    if (stage.value === 'manifest' || stage.value === 'waitingIpa') return 'installation_no_ipa'
    return ''
  })

  async function refresh() {
    const attempt = trace
    const pending = new AbortController()
    controller = pending
    try {
      const next = await api.get<InstallationProgress>(
        `/shares/${shareId()}/progress/${attempt}`,
        pending.signal,
      )
      if (!alive() || trace !== attempt) return
      failures.value = 0
      // The server forgets an idle attempt; that must not erase what was seen here.
      if (next.stage === 'waiting' && progress.value && progress.value.stage !== 'waiting')
        expired.value = true
      else progress.value = next
    } catch {
      if (!alive() || trace !== attempt) return
      failures.value++
    } finally {
      if (alive() && trace === attempt) {
        if (stage.value === 'complete' || error.value) stop()
        else poll.start()
      }
    }
  }

  function start(source: string) {
    const next = installationTrace()
    const link = installationLink(source, next)
    controller?.abort()
    poll.stop()
    trace = next
    progress.value = null
    requested.value = true
    delayed.value = expired.value = false
    failures.value = 0
    warningTimer.start()
    void refresh()
    return link
  }

  function stop() {
    trace = ''
    controller?.abort()
    poll.stop()
    warningTimer.stop()
  }

  onScopeDispose(stop)
  return { start, stop, requested, progress, stage, warning, error }
}
