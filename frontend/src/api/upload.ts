import { demoEnabled } from '../platform/browser.ts'
import { ApiError, isAbort } from './errors.ts'
import { loadDemo, request } from './http.ts'

export interface UploadProgress {
  received: number
  total: number
  speed: number
  status: 'downloading' | 'preparing'
}

// A header carries Latin-1 only; RFC 8187 spells the UTF-8 name in percent escapes.
function attachment(name: string) {
  const encoded = encodeURIComponent(name).replace(
    /['()*]/g,
    (c) => '%' + c.charCodeAt(0).toString(16).toUpperCase(),
  )
  return "attachment; filename*=UTF-8''" + encoded
}

export async function uploadIpa(
  file: File,
  id: string,
  progress: (value: UploadProgress) => void,
  signal: AbortSignal,
): Promise<void> {
  if (demoEnabled) return (await loadDemo()).upload(file, id, progress, signal)
  let received = 0
  let measured = Date.now()
  let speed = 0
  let preparing = false
  let target: EventTarget | undefined

  function prepared() {
    if (preparing || signal.aborted) return
    preparing = true
    progress({ received: file.size, total: file.size, speed: 0, status: 'preparing' })
  }

  await request('PUT', '/library/imports/' + id, {
    body: file,
    signal,
    adapter: 'xhr',
    headers: { 'Content-Disposition': attachment(file.name) },
    onUploadProgress: ({ loaded, progress: fraction, event }) => {
      if (preparing || signal.aborted) return
      if (!target && event?.target) {
        target = event.target
        target?.addEventListener('load', prepared, { once: true })
      }
      const bytes = Math.max(received, Math.min(file.size, loaded))
      // Measured from the request start, so the first event already has a speed.
      const now = Date.now()
      const elapsed = (now - measured) / 1_000
      if (elapsed > 0) {
        const instant = (bytes - received) / elapsed
        speed = speed ? 0.35 * instant + 0.65 * speed : instant
      }
      measured = now
      received = bytes
      preparing = fraction === 1
      progress({
        received,
        total: file.size,
        speed: preparing ? 0 : speed,
        status: preparing ? 'preparing' : 'downloading',
      })
    },
  })
    .catch((error: unknown) => {
      if ((error instanceof ApiError && error.code !== 'error') || isAbort(error)) throw error
      throw new ApiError('import_failed')
    })
    .finally(() => target?.removeEventListener('load', prepared))
}
