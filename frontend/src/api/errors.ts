export class ApiError extends Error {
  code: string

  constructor(code: string) {
    super(code)
    this.name = 'ApiError'
    this.code = code
  }

  static from(body: unknown, fallback = 'error') {
    const value = body as { code?: unknown } | null
    return new ApiError(typeof value?.code === 'string' ? value.code : fallback)
  }
}

export function apiErrorCode(error: unknown, fallback = 'error') {
  return error instanceof ApiError ? error.code : fallback
}

export function isAbort(error: unknown) {
  return error instanceof DOMException && error.name === 'AbortError'
}

export function sessionEnded(code: string) {
  return code === 'sign_in_required' || code === 'session_expired'
}
