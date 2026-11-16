import { computed, reactive, ref, watch } from 'vue'
import { ApiError, apiErrorCode } from '../api/errors.ts'
import { api } from '../api/http.ts'
import { fetchState, logout as endSession } from '../api/state.ts'
import type { State, LoginRequest } from '../api/types.ts'
import { useErrorMessage } from '../i18n/index.ts'
import { useLatest } from './lifetime.ts'
import { loginFailure } from './login.ts'
import { usePolling } from './usePolling.ts'
import { usePreferences } from './usePreferences.ts'

const pollIntervalMs = 2_500

export type Session = ReturnType<typeof useSession>

// Server state, the sign-in flow and the errors they produce.
export function useSession() {
  // A user's action outdates any state request already in flight.
  const polled = useLatest()
  const current = ref<State | null>(null)
  const busy = ref(false)
  const twoFactor = ref(false)
  const connectionError = ref(false)
  const actionError = ref('')
  const form = reactive({
    email: '',
    password: '',
    code: '',
    remember: true,
  } satisfies LoginRequest)
  const { code: errorCode, message: error } = useErrorMessage()
  let lastLoginFailure = ''

  const signedIn = computed(() => Boolean(current.value?.account || current.value?.mode.service))
  const authenticated = computed(() => signedIn.value && current.value?.auth.state === 'ready')
  const savedVerification = computed(() => current.value?.auth.state === 'code')
  const canSubmit = computed(
    () =>
      (savedVerification.value || Boolean(form.email && form.password)) &&
      (!twoFactor.value || /^\d{6}$/.test(form.code)),
  )
  const preferences = usePreferences(current, (code) => {
    actionError.value = code
  })

  function clearSecrets() {
    form.password = ''
    form.code = ''
  }

  function applyState(next: State) {
    connectionError.value = false
    errorCode.value = ''
    if (current.value && next.csrf !== current.value.csrf) preferences.resetPreferences()
    // Apple asks for the password or the code again: restart the form when what it asks changes.
    if (next.auth.state !== 'ready') {
      if (next.auth.state !== current.value?.auth.state) {
        form.email = next.account?.email ?? ''
        clearSecrets()
        twoFactor.value = next.auth.state === 'code'
      }
      if (next.auth.state !== 'code' && next.auth.error && next.auth.error !== lastLoginFailure)
        errorCode.value = next.auth.error
    }
    if (!next.account && next.catalog.error === 'session_expired')
      errorCode.value = 'session_expired'
    current.value = next
    preferences.applyPreferences(next.preferences)
  }

  async function refresh() {
    const fresh = polled.start()
    try {
      const next = await fetchState()
      if (fresh() && !busy.value) applyState(next)
    } catch (e) {
      if (!fresh() || busy.value) return
      connectionError.value = !(e instanceof ApiError)
      if (e instanceof ApiError) errorCode.value = e.code
    }
  }

  function acceptState(next: State) {
    if (busy.value) return
    polled.cancel()
    applyState(next)
  }

  async function login() {
    if (busy.value) return
    if (!canSubmit.value) {
      actionError.value = 'invalid_credentials'
      return
    }
    busy.value = true
    polled.cancel()
    errorCode.value = ''
    try {
      await api.post('/login', form)
      const next = await fetchState()
      if (!next.account && !next.mode.service) throw new ApiError('login_failed')
      applyState(next)
      clearSecrets()
      twoFactor.value = false
    } catch (e) {
      const failure = loginFailure(apiErrorCode(e))
      // A failed read is left to the state poll.
      if (savedVerification.value && !failure.twoFactor)
        await fetchState().then(applyState, () => {})
      twoFactor.value = failure.twoFactor
      lastLoginFailure = failure.errorCode
      if (failure.errorCode) actionError.value = failure.errorCode
      form.code = ''
      if (!failure.keepPassword) form.password = ''
    } finally {
      busy.value = false
    }
  }

  async function logout() {
    if (busy.value || (!signedIn.value && !twoFactor.value)) return false
    polled.cancel()
    busy.value = true
    try {
      await endSession()
      applyState(await fetchState())
      clearSecrets()
      return true
    } catch (e) {
      actionError.value = apiErrorCode(e)
      return false
    } finally {
      busy.value = false
    }
  }

  function resetLogin() {
    polled.cancel()
    actionError.value = errorCode.value = ''
    twoFactor.value = false
    clearSecrets()
  }

  // Abandons a pending challenge on the server before offering the password form again.
  async function restartLogin() {
    if (busy.value) return
    const pending = current.value ? current.value.auth.state !== 'ready' : false
    if ((pending || twoFactor.value) && !(await logout())) return
    resetLogin()
  }

  function deviceChanged() {
    resetLogin()
    void refresh()
  }

  watch(signedIn, (value) => {
    if (!value) twoFactor.value = false
  })
  usePolling(refresh, pollIntervalMs)
  return reactive({
    current,
    busy,
    twoFactor,
    form,
    error,
    connectionError,
    actionError,
    signedIn,
    authenticated,
    savedVerification,
    canSubmit,
    refresh,
    acceptState,
    login,
    logout,
    restartLogin,
    deviceChanged,
    toggleLanguage: preferences.toggleLanguage,
    toggleTheme: preferences.toggleTheme,
  })
}
