import { computed, onScopeDispose, ref, watch } from 'vue'
import { apiErrorCode, sessionEnded } from '../api/errors.ts'
import { createCache } from '../api/pageCache.ts'
import { pageCursor, pageMove, type PageDirection } from '../api/pagination.ts'
import type { DownloadVersion, VersionPage } from '../api/types.ts'
import { loadVersionPage } from '../api/versions.ts'
import { deviceOs, osCompatibility } from '../platform/device.ts'
import { useLatest } from './lifetime.ts'
import { usePageSizing } from './usePageSizing.ts'

// Optional version choices for apps about to download; apps without a choice get the latest.
export function useVersionPicker(
  ids: readonly string[],
  initialNames: Record<string, string>,
  expired: () => void,
) {
  const latest = useLatest()
  const { limit, choosePageSize } = usePageSizing(() => 'versions')
  const currentOs = deviceOs(navigator.userAgent, navigator.maxTouchPoints)
  const specific = ref(false)
  const appId = ref(ids[0] ?? '')
  const choices = ref<Record<string, { id: string; label: string }>>({})
  const names = ref({ ...initialNames })
  const page = ref<VersionPage | null>(null)
  const cursor = ref('')
  const move = ref<PageDirection | ''>('')
  // Cursors are valid only within the revision of the list they came from.
  const revision = ref('')
  const loading = ref(false)
  const progress = ref(0)
  const navigating = ref<PageDirection | ''>('')
  const errorCode = ref('')
  const pages = createCache<VersionPage>()
  let controller: AbortController | undefined

  const versions = computed(() =>
    (page.value?.items ?? []).map((item) => ({
      ...item,
      compatibility: osCompatibility(item.minimumOs, currentOs),
    })),
  )
  const failed = computed(() => !!errorCode.value || !!page.value?.items.some((item) => item.error))

  function stop() {
    latest.cancel()
    controller?.abort()
    loading.value = false
    navigating.value = ''
  }

  function reset() {
    page.value = null
    cursor.value = revision.value = move.value = ''
  }

  async function load(token = '', direction: PageDirection | '' = '', fresh = false) {
    stop()
    if (!specific.value || !appId.value) return
    const current = latest.start()
    const id = appId.value
    // Retrying a page keeps the direction it was reached by.
    const navigation = direction || (token === cursor.value ? move.value : '')
    const key = JSON.stringify([id, limit.value, token, navigation, revision.value])
    move.value = navigation
    cursor.value = token
    errorCode.value = ''
    loading.value = true
    progress.value = 0
    navigating.value = direction
    controller = new AbortController()
    try {
      const result =
        (!fresh && pages.get(key)) ||
        (await loadVersionPage(
          `/catalog/${id}/versions`,
          {
            cursor: token,
            limit: limit.value,
            move: token ? pageMove(navigation || 'next') : '',
            revision: token ? revision.value : '',
          },
          (value) => {
            if (current()) progress.value = value
          },
          controller.signal,
        ))
      if (!current()) return
      if (!result.items.some((item) => item.error)) pages.set(key, result)
      names.value[id] = result.name
      page.value = result
      revision.value = result.revision
    } catch (e) {
      if (!current()) return
      errorCode.value = apiErrorCode(e)
      if (sessionEnded(errorCode.value)) expired()
      if (errorCode.value === 'versions_changed') {
        pages.clear()
        reset()
      }
    } finally {
      if (current()) {
        loading.value = false
        navigating.value = ''
      }
    }
  }

  const navigate = (to: PageDirection) => load(pageCursor(page.value!, to), to)

  const retry = () => load(cursor.value, '', true)

  // Stops loading and returns the chosen versions.
  function finish(): DownloadVersion[] {
    stop()
    return specific.value
      ? Object.entries(choices.value).map(([appId, choice]) => ({ appId, versionId: choice.id }))
      : []
  }

  watch([specific, appId, limit], () => {
    reset()
    void load()
  })
  onScopeDispose(stop)

  return {
    specific,
    appId,
    choices,
    names,
    page,
    versions,
    limit,
    loading,
    progress,
    navigating,
    errorCode,
    failed,
    choosePageSize,
    navigate,
    retry,
    finish,
  }
}
