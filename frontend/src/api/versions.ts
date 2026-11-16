import type { VersionLoadEvent, VersionPage, VersionPageRequest } from './types.ts'
import { ApiError } from './errors.ts'
import { request } from './http.ts'

const maxVersionEventLength = 1_048_576

export async function loadVersionPage(
  path: string,
  input: VersionPageRequest,
  progress: (value: number) => void,
  signal: AbortSignal,
): Promise<VersionPage> {
  const response = await request<ReadableStream<Uint8Array>>('POST', path, {
    body: input,
    signal,
    responseType: 'stream',
    headers: { Accept: 'application/x-ndjson' },
  })
  if (!String(response.headers['content-type']).startsWith('application/x-ndjson'))
    return new Response(response.data).json() as Promise<VersionPage>
  if (!response.data) throw new ApiError('error')
  const reader = response.data.getReader()
  const decoder = new TextDecoder()
  let pending = ''
  let page: VersionPage | null = null
  let last = 0

  function event(line: string) {
    if (!line.trim()) return
    if (line.length > maxVersionEventLength) throw new ApiError('error')
    let value: VersionLoadEvent
    try {
      value = JSON.parse(line) as VersionLoadEvent
    } catch {
      throw new ApiError('error')
    }
    if (!value || typeof value !== 'object') throw new ApiError('error')
    if (value.error) throw new ApiError(value.error)
    if (!Number.isFinite(value.progress)) throw new ApiError('error')
    last = Math.max(last, Math.min(value.page ? 100 : 99, Math.max(0, Math.floor(value.progress))))
    progress(last)
    if (value.page) page = value.page
  }

  try {
    while (true) {
      const { value, done } = await reader.read()
      pending += decoder.decode(value, { stream: !done })
      let newline: number
      while ((newline = pending.indexOf('\n')) >= 0) {
        event(pending.slice(0, newline))
        pending = pending.slice(newline + 1)
      }
      if (pending.length > maxVersionEventLength) throw new ApiError('error')
      if (done) break
    }
    event(pending)
    if (!page) throw new ApiError('error')
    return page
  } finally {
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}
