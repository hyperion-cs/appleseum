import { computed, ref, watch, type Ref } from 'vue'
import { apiErrorCode } from '../api/errors.ts'
import { api } from '../api/http.ts'
import type { DeviceProfile, DeviceSettings, MacOsVersion, State } from '../api/types.ts'
import { t } from '../i18n/index.ts'
import { useAlive } from './lifetime.ts'
import { useAction } from './useAction.ts'

export type DeviceUpdate = 'save' | 'defaults' | 'reset'

const osLabel = (os: MacOsVersion) => `macOS ${os.version}`

// The virtual device Apple sees: an edited copy of its settings and the confirmed updates.
export function useDeviceSettings(
  state: Readonly<Ref<State>>,
  errorCode: Ref<string>,
  changed: () => void,
) {
  const alive = useAlive()
  const profile = ref<DeviceProfile | null>(null)
  const form = ref<DeviceSettings | null>(null)
  const loading = ref(false)
  const loadFailed = ref(false)
  const confirming = ref<DeviceUpdate | ''>('')
  const { running: savingAction, busy: saving, run } = useAction(errorCode)
  const locked = computed(
    () =>
      state.value.auth.busy ||
      Boolean(profile.value?.readOnly) ||
      (Boolean(state.value.account) && state.value.auth.state === 'code') ||
      state.value.downloadsActive,
  )
  const fields = computed<{ key: keyof DeviceSettings; label: string; help: string }[]>(() => [
    { key: 'model', label: t.value.deviceModel, help: t.value.deviceModelHelp },
    { key: 'osVersion', label: t.value.deviceOs, help: t.value.deviceOsHelp },
    { key: 'locale', label: t.value.deviceLocale, help: t.value.deviceLocaleHelp },
    { key: 'timeZone', label: t.value.deviceTimeZone, help: t.value.deviceTimeZoneHelp },
  ])

  function differs(target: DeviceSettings | undefined) {
    const current = form.value
    return Boolean(
      current && target && fields.value.some(({ key }) => current[key] !== target[key]),
    )
  }

  const dirty = computed(() => differs(profile.value?.settings))
  const defaultsChanged = computed(() => differs(profile.value?.defaults))
  // Each model runs a range of macOS versions.
  const versions = computed(() => {
    const model = profile.value?.models.find((model) => model.id === form.value?.model)
    return (profile.value?.osVersions ?? []).filter((os) => {
      const major = Number(os.version.split('.')[0])
      return !model || (major >= model.minOs && major <= model.maxOs)
    })
  })
  const models = computed(() =>
    (profile.value?.models ?? []).map((model) => ({ value: model.id, label: model.name })),
  )
  const osVersions = computed(() =>
    versions.value.map((os) => ({ value: os.version, label: osLabel(os) })),
  )
  // Settings that restoring the defaults would change, with readable values.
  const defaultChanges = computed(() => {
    const current = profile.value
    if (!current) return []
    const names: Partial<Record<keyof DeviceSettings, Map<string, string>>> = {
      model: new Map(current.models.map((model) => [model.id, model.name])),
      osVersion: new Map(current.osVersions.map((os) => [os.version, osLabel(os)])),
    }
    return fields.value
      .filter(({ key }) => current.settings[key] !== current.defaults[key])
      .map(({ key, label }) => ({
        key,
        label,
        value: names[key]?.get(current.defaults[key]) ?? current.defaults[key],
      }))
  })
  const defaultsReplaceIdentity = computed(
    () => profile.value?.settings.model !== profile.value?.defaults.model,
  )
  watch(
    () => form.value?.model,
    () => {
      const supported = versions.value.map((os) => os.version)
      if (form.value && supported.length && !supported.includes(form.value.osVersion))
        form.value.osVersion = supported[0]!
    },
  )

  function apply(value: DeviceProfile) {
    profile.value = value
    form.value = { ...value.settings }
  }

  async function load() {
    if (loading.value) return
    loading.value = true
    errorCode.value = ''
    try {
      apply(await api.get<DeviceProfile>('/admin/device'))
      loadFailed.value = false
    } catch (e) {
      loadFailed.value = true
      errorCode.value = apiErrorCode(e)
    } finally {
      loading.value = false
    }
  }

  function confirm(action: DeviceUpdate) {
    errorCode.value = ''
    confirming.value = action
  }

  // A new model means a new device identity, so that change is confirmed first.
  function requestUpdate(action: 'save' | 'defaults') {
    if (saving.value || locked.value || !profile.value || !form.value) return
    const target = action === 'save' ? form.value : profile.value.defaults
    if (action === 'defaults' || target.model !== profile.value.settings.model) confirm(action)
    else void update(action)
  }

  function update(action: DeviceUpdate) {
    if (locked.value || (action === 'save' && !dirty.value)) return
    return run(async () => {
      const value = await (action === 'save'
        ? api.put<DeviceProfile>('/admin/device', form.value)
        : api.post<DeviceProfile>('/admin/device/' + action))
      if (!alive()) return
      apply(value)
      confirming.value = ''
      changed()
    }, action)
  }

  return {
    profile,
    form,
    loading,
    loadFailed,
    confirming,
    savingAction,
    saving,
    locked,
    fields,
    models,
    osVersions,
    dirty,
    defaultsChanged,
    defaultChanges,
    defaultsReplaceIdentity,
    load,
    confirm,
    requestUpdate,
    update,
  }
}
