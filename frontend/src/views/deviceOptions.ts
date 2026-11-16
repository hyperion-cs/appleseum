import data from '../assets/device-options.json' with { type: 'json' }
import type { DeviceSettings } from '../api/types.ts'

export function deviceOptions(current: Partial<DeviceSettings> = {}) {
  function options(values: string[], selected: string | undefined) {
    return [...new Set([...values, ...(selected ? [selected] : [])])]
      .sort()
      .map((value) => ({ value, label: value }))
  }

  return {
    locale: options(data.locales, current.locale),
    timeZone: options(data.timeZones, current.timeZone),
  }
}
