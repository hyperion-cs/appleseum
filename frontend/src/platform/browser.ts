import { productName } from '../api/types.ts'

export const demoEnabled =
  typeof location !== 'undefined' && new URLSearchParams(location.search).has('demo')

export function createBrowserStorage(demo: boolean) {
  const prefix = productName + '.'

  function key(name: string) {
    return demo
      ? prefix + 'demo.' + (name.startsWith(prefix) ? name.slice(prefix.length) : name)
      : name
  }

  return {
    getItem: (name: string) => localStorage.getItem(key(name)),

    setItem: (name: string, value: string) => localStorage.setItem(key(name), value),

    removeItem: (name: string) => localStorage.removeItem(key(name)),
  }
}

export const demoStorage = createBrowserStorage(true)
export const browserStorage = demoEnabled ? demoStorage : createBrowserStorage(false)

export function base64Url(bytes: Uint8Array) {
  return btoa(String.fromCharCode(...bytes))
    .replaceAll('+', '-')
    .replaceAll('/', '_')
    .replace(/=+$/, '')
}

export function randomId() {
  if (typeof crypto.randomUUID === 'function') return crypto.randomUUID()
  // Local HTTP origins lack randomUUID, but still expose getRandomValues.
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  bytes[6] = (bytes[6]! & 15) | 64
  bytes[8] = (bytes[8]! & 63) | 128
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

export function downloadFile(file: string | Blob, name = '') {
  const url = typeof file === 'string' ? file : URL.createObjectURL(file)
  const link = document.createElement('a')
  link.href = url
  link.download = name
  document.body.append(link)
  try {
    link.click()
  } finally {
    link.remove()
    if (typeof file !== 'string') setTimeout(() => URL.revokeObjectURL(url), 1_000)
  }
}

export async function copyText(value: string) {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(value)
      return
    } catch {
      // Local HTTP and denied clipboard access need the selection-based fallback.
    }
  }
  const prior = document.activeElement as HTMLElement | null
  const input = document.createElement('textarea')
  input.value = value
  input.readOnly = true
  input.tabIndex = -1
  input.className = 'clipboard-input'
  input.setAttribute('aria-hidden', 'true')
  document.body.append(input)
  try {
    input.focus({ preventScroll: true })
    input.select()
    input.setSelectionRange(0, value.length)
    if (!document.execCommand('copy')) throw new Error('copy')
  } finally {
    input.remove()
    prior?.focus({ preventScroll: true })
  }
}
