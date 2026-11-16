import { onScopeDispose, ref } from 'vue'
import { ApiError, apiErrorCode, isAbort, sessionEnded } from '../api/errors.ts'
import { api } from '../api/http.ts'
import type { LibraryItem } from '../api/types.ts'
import { uploadIpa } from '../api/upload.ts'
import { randomId } from '../platform/browser.ts'
import { useAlive } from './lifetime.ts'

const maxImportBytes = 4_294_967_296

// Uploads IPA files one by one; the active one is shown as a local library row.
export function useIpaImport(reconcile: (id: string, completed: boolean) => Promise<void>) {
  const alive = useAlive()
  const importing = ref(false)
  const failures = ref<{ name: string; code: string }[]>([])
  const transfer = ref<LibraryItem | null>(null)
  let active: { id: string; controller: AbortController } | null = null

  // Resolves to the failure code, or '' once the server has the file.
  async function importFile(file: File) {
    const id = randomId()
    let code = ''
    try {
      if (!/\.ipa$/i.test(file.name)) throw new ApiError('invalid_ipa')
      if (file.size > maxImportBytes) throw new ApiError('import_too_large')
      active = { id, controller: new AbortController() }
      transfer.value = {
        id,
        cursor: '',
        appId: '',
        name: file.name,
        status: 'downloading',
        received: 0,
        total: file.size,
        speed: 0,
        error: '',
        version: '',
        minimumOs: '',
        size: file.size,
        createdAt: new Date().toISOString(),
        canManage: true,
        icon: '',
      }
      await uploadIpa(
        file,
        id,
        (progress) => {
          if (transfer.value?.id === id) Object.assign(transfer.value, progress)
        },
        active.controller.signal,
      )
    } catch (e) {
      code = isAbort(e) ? 'canceled' : apiErrorCode(e, 'import_failed')
    } finally {
      if (active?.id === id) active = null
      if (alive() && transfer.value?.id === id) {
        await reconcile(id, !code)
        transfer.value = null
      }
    }
    return code
  }

  // Resolves to the session error that stopped the queue, if any.
  async function upload(files: File[]) {
    if (importing.value || !files.length) return
    failures.value = []
    importing.value = true
    try {
      for (const file of files) {
        if (!alive()) return
        const code = await importFile(file)
        if (!alive()) return
        if (code && code !== 'canceled') failures.value.push({ name: file.name, code })
        if (sessionEnded(code)) return code
      }
    } finally {
      importing.value = false
    }
  }

  async function cancel(id: string) {
    if (active?.id === id) active.controller.abort()
    try {
      await api.post('/downloads/' + id + '/cancel')
    } catch (e) {
      // A buffering proxy may not have forwarded the upload to the server yet.
      if (!['not_found', 'action_unavailable'].includes(apiErrorCode(e, ''))) throw e
    }
  }

  onScopeDispose(() => active?.controller.abort())
  return { importing, failures, transfer, upload, cancel }
}
