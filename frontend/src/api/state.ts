import type { State } from './types.ts'
import { credentials } from './credentials.ts'
import { ApiError } from './errors.ts'
import { api } from './http.ts'

export async function logout() {
  await api.post('/logout')
  credentials.reset()
}

// A public session without its browser key cannot act for Apple: end it once and retry.
export async function fetchState(): Promise<State> {
  for (let attempt = 0; attempt < 2; attempt++) {
    const value = await api.get<State>('/state')
    if (credentials.accept(value)) return value
    if (attempt === 0) await logout()
  }
  throw new ApiError('session_key_required')
}
