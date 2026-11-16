import type { State } from './types.ts'
import { ApiError } from './errors.ts'
import { base64Url, browserStorage } from '../platform/browser.ts'

const storageKey = 'appleseum.sessionKey'
const appleActions = new Set(['/login', '/catalog/refresh', '/profile/refresh', '/downloads'])

function stored<T>(access: () => T) {
  try {
    return access()
  } catch {
    throw new ApiError('browser_storage_unavailable')
  }
}

function readKey() {
  const saved = stored(() => browserStorage.getItem(storageKey)) ?? ''
  return /^[A-Za-z0-9_-]{43}$/.test(saved) ? saved : ''
}

function createKey() {
  const key = base64Url(crypto.getRandomValues(new Uint8Array(32)))
  stored(() => browserStorage.setItem(storageKey, key))
  return key
}

export const credentials = {
  csrf: '',
  public: false,
  service: false,
  key: '',

  reset() {
    this.key = ''
    try {
      browserStorage.removeItem(storageKey)
    } catch {}
  },

  accept(value: State) {
    this.csrf = value.csrf
    this.public = value.mode.public
    this.service = value.mode.service
    if (!value.mode.public) return true
    if (location.protocol !== 'https:') throw new ApiError('https_required')
    if (value.mode.service) {
      this.reset()
      return true
    }
    this.key = readKey()
    if (value.account && !this.key) return false
    this.key ||= createKey()
    return true
  },

  headers(path: string, method: string): Record<string, string> {
    const route = path.split('?')[0]!
    const apple =
      method === 'POST' && (appleActions.has(route) || /^\/catalog\/[^/]+\/versions$/.test(route))
    return {
      'X-CSRF-Token': this.csrf,
      ...(this.public && !this.service && apple && this.key ? { 'X-Session-Key': this.key } : {}),
    }
  },
}
