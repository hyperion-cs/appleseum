import { computed } from 'vue'
import { language, languages, t } from './index.ts'

const formats = computed(() => {
  const locale = languages[language.value].locale

  const unit = (name: string) =>
    new Intl.NumberFormat(locale, { style: 'unit', unit: name, maximumFractionDigits: 1 })

  return {
    megabits: unit('megabit'),
    megabytes: unit('megabyte'),
    gigabytes: unit('gigabyte'),
    decimal: new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }),
    percent: new Intl.NumberFormat(locale, {
      style: 'percent',
      minimumFractionDigits: 1,
      maximumFractionDigits: 1,
    }),
    date: new Intl.DateTimeFormat(locale, { dateStyle: 'short', timeStyle: 'short' }),
    day: new Intl.DateTimeFormat(locale, { dateStyle: 'short' }),
    clock: new Intl.DateTimeFormat(locale, {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
    }),
    regions: new Intl.DisplayNames([language.value], { type: 'region' }),
  }
})

export function bytes(value: number) {
  return value >= 1e9
    ? formats.value.gigabytes.format(value / 1e9)
    : formats.value.megabytes.format(value / 1e6)
}

export function bitrate(bytesPerSecond: number) {
  return formats.value.megabits.format((bytesPerSecond * 8) / 1e6) + t.value.perSecond
}

export function decimal(value: number) {
  return formats.value.decimal.format(value)
}

export function percent(value: number) {
  return formats.value.percent.format(value / 100).replace(/\s/gu, '')
}

export function date(value: string) {
  const parsed = new Date(value)
  return Number.isFinite(parsed.getTime()) ? formats.value.date.format(parsed) : t.value.noValue
}

export function clock(value: string) {
  return formats.value.clock.format(new Date(value))
}

export function purchaseDate(value: string) {
  if (!value) return t.value.noValue
  const parsed = new Date(/^\d{4}-\d{2}-\d{2}$/.test(value) ? value + 'T00:00:00' : value)
  return Number.isFinite(parsed.getTime()) ? formats.value.day.format(parsed) : value
}

export function region(code: string) {
  try {
    return formats.value.regions.of(code.toUpperCase()) ?? code
  } catch {
    return code
  }
}
