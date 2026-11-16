import { onScopeDispose, ref, watchEffect, type Ref } from 'vue'
import { apiErrorCode } from '../api/errors.ts'
import { api } from '../api/http.ts'
import type { Preferences, PreferencesInput, State } from '../api/types.ts'
import { language, nextLanguage, type Language } from '../i18n/index.ts'
import { useLatest } from './lifetime.ts'

export type Theme = 'light' | 'dark'
export const theme = ref<Theme>('dark')

// Saves the latest choice one request at a time; polled state never overrides a pending one.
export function usePreferences(state: Ref<State | null>, reportError: (code: string) => void) {
  const session = useLatest()
  let revision = 0
  let pending: PreferencesInput | null = null
  let saving = false
  let controller: AbortController | undefined

  watchEffect(
    () => {
      document.documentElement.dataset.theme = theme.value
      document.documentElement.lang = language.value
    },
    { flush: 'sync' },
  )

  // Another session has its own preferences: drop this one's queue and late replies.
  function resetPreferences() {
    session.cancel()
    controller?.abort()
    pending = null
    revision = 0
    saving = false
  }

  function applyPreferences(saved: Preferences) {
    if (saving || pending || saved.revision <= revision) return
    revision = saved.revision
    language.value = saved.language as Language
    theme.value = saved.theme as Theme
  }

  async function save() {
    pending = { language: language.value, theme: theme.value }
    if (saving) return
    saving = true
    const current = session.start()
    while (pending && current()) {
      const input = pending
      pending = null
      controller = new AbortController()
      try {
        const saved = await api.put<Preferences>('/preferences', input, controller.signal)
        if (!current()) return
        revision = Math.max(revision, saved.revision)
        if (state.value) state.value.preferences = saved
      } catch (error) {
        if (current()) reportError(apiErrorCode(error))
      }
    }
    if (current()) saving = false
  }

  function toggleLanguage() {
    language.value = nextLanguage.value
    void save()
  }

  function toggleTheme() {
    theme.value = theme.value === 'dark' ? 'light' : 'dark'
    void save()
  }

  onScopeDispose(resetPreferences)
  return { applyPreferences, resetPreferences, toggleLanguage, toggleTheme }
}
