import { defaultPageSize, defaultVersionPageSize } from './pagination.ts'
import fixtures from '../assets/demo.json' with { type: 'json' }
import { maxPageSize, maxSelectionSize, minSearchLength } from './types.ts'
import { base64Url, demoStorage, randomId } from '../platform/browser.ts'
import deviceModels from '../../../applekit/assets/device-models.json' with { type: 'json' }
import macOsVersions from '../../../applekit/assets/macos-versions.json' with { type: 'json' }
import type { UploadProgress } from './upload.ts'
import type {
  Share,
  CatalogItem,
  DownloadRequest,
  Selection,
  DeviceSettings,
  DeviceProfile,
  InstallationProgress,
  LibraryItem,
  PreferencesInput,
  ProfileSummary,
  State,
  VersionPageRequest,
} from './types.ts'

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function failure(code: string, status: number) {
  return json({ code }, status)
}

function empty(status = 204) {
  return new Response(null, { status })
}

const { apps, files, colors, symbols } = fixtures

export function createDemo(now: () => number = Date.now) {
  const downloadSpeed = 6_250_000 // 50 Mbit/s.
  const preparationMs = 1_800
  const catalog: CatalogItem[] = apps.map((app, index) => ({
    cursor: '',
    id: app.id,
    name: app.name,
    delisted: index < 12,
    inLibrary: false,
    updateAvailable: false,
    size: app.size,
    purchasedAt: app.purchasedAt,
    icon:
      'data:image/svg+xml,' +
      encodeURIComponent(
        `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="14" fill="${colors[index % colors.length]}"/><path d="${symbols[index % symbols.length]}" fill="none" stroke="white" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/></svg>`,
      ),
  }))

  let library: LibraryItem[] = files.map((file) => {
    const app = apps.find((app) => app.id === file.appId)!
    const size = file.size ?? app.size
    return {
      cursor: '',
      id: file.id,
      appId: app.id,
      name: app.name,
      icon: catalog.find((item) => item.id === app.id)!.icon,
      version: file.version,
      minimumOs: app.versions.find(([version]) => version === file.version)![1],
      size,
      createdAt: file.createdAt,
      status: 'done',
      received: size,
      total: size,
      speed: 0,
      error: '',
      canManage: true,
    }
  })
  const state: State = {
    csrf: '',
    mode: {
      public: false,
      sharedLibrary: false,
      service: false,
      admin: true,
      debug: false,
      ish: false,
    },
    account: {
      email: 'alex.morgan@example.com',
      name: 'Alex Morgan',
      id: '9461832075',
      country: 'US',
      storefront: '143441',
      signedInAt: '2026-11-09T18:42:16Z',
    },
    auth: { state: 'ready', busy: false, error: '' },
    catalog: { status: 'ready', progress: 100, error: '', cached: true },
    storageError: '',
    downloadsActive: library.some((file) => file.status === 'downloading'),
    shareCount: 0,
    tables: {
      shares: { value: { sort: 'createdAt', direction: 'desc' }, revision: 1 },
      catalog: { value: { sort: 'purchasedAt', direction: 'desc', delisted: true }, revision: 1 },
      library: { value: { sort: 'createdAt', direction: 'desc', mine: true }, revision: 1 },
    },
    preferences: { language: 'en', theme: 'dark', revision: 1 },
  }
  const profile: ProfileSummary = {
    catalogApps: catalog.length,
    delistedApps: catalog.filter((app) => app.delisted).length,
    signedInAt: state.account!.signedInAt,
    apple: {
      nickname: 'alexmorgan',
      familyRole: 'organizer',
      balance: '$8.47',
      subscriptions: 3,
    },
    appleError: '',
  }
  const settings = {
    model: 'MacBookPro18,1',
    osVersion: '26.5',
    locale: 'en-US',
    timeZone: 'America/New_York',
  }
  const device: DeviceProfile = {
    readOnly: false,
    models: deviceModels.map(({ id, name, minOS, maxOS }) => ({
      id,
      name,
      minOs: minOS,
      maxOs: maxOS,
    })),
    osVersions: macOsVersions.map(({ version }) => ({ version })),
    settings,
    defaults: { ...settings },
    id: '7fc8d2ea-6b49-45c3-91df-8b351e92a407',
    guid: '7FC8D2EA6B49',
    clientVersion: 'Configurator 2.21',
  }

  const transfers = new Map<string, { file: LibraryItem; startedAt: number }>()
  let shares: Share[] = []
  const shareFiles = new Map<string, string[]>()
  const account = state.account
  const appleProfile = profile.apple
  let refreshingAt: number | null = null

  const timestamp = () => new Date(now()).toISOString()

  function readSaved(key: string): unknown {
    try {
      return JSON.parse(demoStorage.getItem('appleseum.' + key) ?? 'null')
    } catch {
      return null
    }
  }

  function save(key: string, value: unknown) {
    try {
      demoStorage.setItem('appleseum.' + key, JSON.stringify(value))
    } catch {}
  }

  const saved = readSaved('preferences') as PreferencesInput | null
  if (saved && ['en', 'ru'].includes(saved.language) && ['light', 'dark'].includes(saved.theme))
    Object.assign(state.preferences, saved)
  for (const resource of ['catalog', 'library', 'shares'] as const) {
    const value = readSaved('table-settings.' + resource) as
      State['tables'][typeof resource]['value'] | null
    if (value && typeof value.sort === 'string' && ['asc', 'desc'].includes(value.direction))
      Object.assign(state.tables[resource].value, value)
  }

  function duration(file: LibraryItem) {
    return (file.total / downloadSpeed) * 1_000 + preparationMs
  }

  function update() {
    const at = now()
    for (const [key, transfer] of transfers) {
      const elapsed = at - transfer.startedAt
      const downloadingMs = (transfer.file.total / downloadSpeed) * 1_000
      Object.assign(transfer.file, {
        received: Math.min(
          transfer.file.total,
          Math.max(0, Math.floor((elapsed / 1_000) * downloadSpeed)),
        ),
        speed: elapsed >= 0 && elapsed < downloadingMs ? downloadSpeed : 0,
        status:
          elapsed < 0
            ? 'queued'
            : elapsed < downloadingMs
              ? 'downloading'
              : elapsed < downloadingMs + preparationMs
                ? 'preparing'
                : 'done',
      })
      if (transfer.file.status === 'done') transfers.delete(key)
    }
    if (refreshingAt !== null) {
      state.catalog.progress = Math.min(100, Math.floor((at - refreshingAt) / 25))
      if (state.catalog.progress === 100) {
        state.catalog.cached = true
        state.catalog.status = 'ready'
        refreshingAt = null
      }
    }
    state.downloadsActive = transfers.size > 0
    for (const app of catalog) {
      const file = library.find((file) => file.appId === app.id && file.status === 'done')
      app.inLibrary = Boolean(file)
      app.size = file?.size ?? apps.find((item) => item.id === app.id)!.size
    }
    profile.catalogApps = state.catalog.cached ? catalog.length : 0
    profile.delistedApps = state.catalog.cached ? catalog.filter((app) => app.delisted).length : 0
  }

  function enqueue(file: LibraryItem) {
    let startedAt = now()
    for (const transfer of transfers.values())
      startedAt = Math.max(startedAt, transfer.startedAt + duration(transfer.file))
    file.status = startedAt > now() ? 'queued' : 'downloading'
    library.unshift(file)
    transfers.set(file.id, { file, startedAt })
    update()
  }

  function reschedule() {
    let at = now()
    for (const transfer of transfers.values()) {
      if (transfer.startedAt > now()) transfer.startedAt = at
      at = Math.max(at, transfer.startedAt + duration(transfer.file))
    }
    update()
  }

  function selectedLibrary(input: Selection) {
    return matching(library, input.filter?.query ?? '').filter(
      (file) => input.filter || input.ids.includes(file.id),
    )
  }

  function matchingShares(query = '') {
    const text = query.toLocaleLowerCase()
    return shares.filter(
      (share) =>
        !text ||
        share.owner.toLocaleLowerCase().includes(text) ||
        (shareFiles.get(share.id) ?? []).some((id) =>
          library
            .find((file) => file.id === id)
            ?.name.toLocaleLowerCase()
            .includes(text),
        ),
    )
  }

  function newIdentity() {
    device.id = randomId()
    device.guid = device.id.replaceAll('-', '').slice(0, 12).toUpperCase()
    state.account = null
  }

  function applyDevice(value: DeviceSettings) {
    if (device.settings.model !== value.model) newIdentity()
    device.settings = { ...value }
  }

  function versionsFor(app: (typeof apps)[number]) {
    return app.versions.map(([version, minimumOs], index) => {
      let hash = 2_166_136_261
      for (const char of app.id + '/' + version)
        hash = Math.imul(hash ^ char.charCodeAt(0), 16_777_619) >>> 0
      return {
        id: String(820_000_000 + (hash % 140_000_000)),
        version,
        minimumOs: minimumOs,
        latest: index === 0,
        error: '',
      }
    })
  }

  function matching<T extends CatalogItem | LibraryItem>(
    items: T[],
    query: string,
    delisted = false,
  ) {
    const q = query.trim().toLocaleLowerCase()
    return items.filter(
      (app) =>
        (!q || app.name.toLocaleLowerCase().includes(q)) &&
        (!delisted || ('delisted' in app && app.delisted)),
    )
  }

  function page<T extends { id: string }>(
    items: T[],
    params: URLSearchParams,
    value: (item: T, index: number) => unknown = (_, i) => i,
  ) {
    const limit = Math.min(maxPageSize, Math.max(1, Number(params.get('limit')) || defaultPageSize))
    const desc = params.get('direction') !== 'asc'

    const compareValue = (a: unknown, b: unknown) =>
      typeof a === 'number' && typeof b === 'number'
        ? a - b
        : String(a ?? '').localeCompare(String(b ?? ''), undefined, { numeric: true })

    const compare = (a: [unknown, string], b: [unknown, string]) =>
      (compareValue(a[0], b[0]) || compareValue(a[1], b[1])) * (desc ? -1 : 1)

    const ordered = items
      .map((item, i) => ({ item, key: [value(item, i), item.id] as [unknown, string] }))
      .sort((a, b) => compare(a.key, b.key))
    const raw = params.get('cursor')
    let boundary: [unknown, string] | null = null
    if (raw)
      boundary = JSON.parse(
        new TextDecoder().decode(
          Uint8Array.from(atob(raw.replaceAll('-', '+').replaceAll('_', '/')), (c) =>
            c.charCodeAt(0),
          ),
        ),
      )
    const back = params.get('move') === 'prev'
    const matching = boundary
      ? ordered.filter((row) =>
          back ? compare(row.key, boundary!) < 0 : compare(row.key, boundary!) > 0,
        )
      : ordered
    const selected = back ? matching.slice(-limit) : matching.slice(0, limit)

    const encode = (key: [unknown, string]) =>
      base64Url(new TextEncoder().encode(JSON.stringify(key)))

    return {
      total: items.length,
      items: selected.map((row) => ({ ...row.item, cursor: encode(row.key) })),
      next: !!selected.length && ordered.indexOf(selected.at(-1)!) < ordered.length - 1,
      previous: !!selected.length && ordered.indexOf(selected[0]!) > 0,
    }
  }

  function request(path: string, method = 'GET', body?: unknown): Response {
    update()
    const [route, query = ''] = path.split('?')
    const params = new URLSearchParams(query)
    // A selection is the body itself or, for downloads, its selection field.
    const selection =
      (body as DownloadRequest | undefined)?.selection ?? (body as Selection | undefined)
    const search = params.get('q') ?? selection?.filter?.query ?? ''
    if (
      /^\/(catalog|library|shares|downloads)(\/|$)/.test(route!) &&
      (typeof search !== 'string' || (search && [...search].length < minSearchLength))
    )
      return failure('invalid_request', 400)
    if (Array.isArray(selection?.ids) && selection.ids.length > maxSelectionSize)
      return failure('selection_too_large', 400)
    switch (method + ' ' + route) {
      case 'GET /state': {
        state.shareCount = shares.length
        return json(state)
      }
      case 'GET /shares': {
        const items = matchingShares(params.get('q') ?? '')
        const key = (params.get('sort') || 'createdAt') as keyof Share
        return json(page(items, params, (item) => item[key]))
      }
      case 'GET /profile': {
        return json(profile)
      }
      case 'GET /admin/device': {
        return json(device)
      }
      case 'GET /catalog':
      case 'GET /library': {
        const items = matching<CatalogItem | LibraryItem>(
          route === '/catalog' ? (state.catalog.cached ? catalog : []) : library,
          params.get('q') ?? '',
          route === '/catalog' && params.get('delisted') === 'true',
        )
        const key = params.get('sort') || (route === '/catalog' ? 'purchasedAt' : 'createdAt')

        const field = (item: CatalogItem | LibraryItem) =>
          key === 'status' && 'delisted' in item
            ? Number(item.delisted)
            : item[key as keyof typeof item]

        return json(page(items, params, field))
      }
      case 'PUT /preferences': {
        const value = body as PreferencesInput
        if (
          !value ||
          !['ru', 'en'].includes(value.language) ||
          !['light', 'dark'].includes(value.theme)
        )
          return failure('invalid_request', 400)
        state.preferences = {
          language: value.language,
          theme: value.theme,
          revision: state.preferences.revision + 1,
        }
        save('preferences', state.preferences)
        return json(state.preferences)
      }
      case 'PUT /table-settings/catalog':
      case 'PUT /table-settings/library':
      case 'PUT /table-settings/shares': {
        const resource = route.split('/').at(-1) as keyof State['tables']
        const value = body as State['tables'][typeof resource]['value']
        const tables = state.tables as unknown as Record<
          string,
          { value: unknown; revision: number }
        >
        tables[resource] = { value: { ...value }, revision: state.tables[resource].revision + 1 }
        save('table-settings.' + resource, value)
        return json(state.tables[resource])
      }
      case 'POST /catalog/selection':
      case 'POST /library/selection': {
        const value = body as Selection
        const isLibrary = route === '/library/selection'
        const items = matching<CatalogItem | LibraryItem>(
          isLibrary ? library : catalog,
          value.filter?.query ?? '',
          !isLibrary && Boolean(value.filter?.delisted),
        ).filter(
          (item) =>
            (value.filter || value.ids.includes(item.id)) &&
            (isLibrary ||
              !(item as CatalogItem).inLibrary ||
              (item as CatalogItem).updateAvailable),
        )
        if (items.length > maxSelectionSize) return failure('selection_too_large', 400)
        if (isLibrary)
          return json({
            ids: items.map((item) => item.id),
            readyIds: (items as LibraryItem[])
              .filter((item) => item.status === 'done')
              .map((item) => item.id),
            manageIds: items.map((item) => item.id),
          })
        return json({
          ids: (items as CatalogItem[])
            .filter((item) => !item.inLibrary || item.updateAvailable)
            .map((item) => item.id),
        })
      }
      case 'POST /downloads': {
        if (!state.account) return failure('sign_in_required', 401)
        const value = body as DownloadRequest
        const { ids, filter } = value.selection
        const selected = matching(catalog, filter?.query ?? '', Boolean(filter?.delisted)).filter(
          (app) => filter || ids.includes(app.id),
        )
        const added: LibraryItem[] = []
        for (const app of selected) {
          if (
            library.some(
              (file) =>
                file.appId === app.id &&
                ['done', 'queued', 'downloading', 'preparing'].includes(file.status),
            )
          )
            continue
          const versions = versionsFor(apps.find((item) => item.id === app.id)!)
          const choice = value.versions?.find((choice) => choice.appId === app.id)
          const version = choice
            ? versions.find((item) => item.id === choice.versionId)
            : versions[0]
          if (!version) return failure('invalid_request', 400)
          added.push({
            cursor: '',
            id: randomId(),
            appId: app.id,
            name: app.name,
            icon: app.icon,
            canManage: true,
            status: 'queued',
            received: 0,
            total: app.size!,
            size: app.size!,
            speed: 0,
            error: '',
            version: version.version,
            minimumOs: version.minimumOs,
            createdAt: timestamp(),
          })
        }
        for (const file of added) enqueue(file)
        return empty(202)
      }
      case 'POST /library/delete': {
        const removed = new Set(selectedLibrary(body as Selection).map((file) => file.id))
        if (shares.some((share) => shareFiles.get(share.id)?.some((key) => removed.has(key))))
          return failure('shared_files', 409)
        library = library.filter((file) => !removed.has(file.id))
        for (const key of removed) transfers.delete(key)
        reschedule()
        return empty()
      }
      case 'POST /shares/selection': {
        const ids = matchingShares((body as Selection).filter?.query).map((share) => share.id)
        return ids.length > maxSelectionSize ? failure('selection_too_large', 400) : json({ ids })
      }
      case 'POST /shares/stop': {
        const ids = new Set((body as Selection).ids)
        shares = shares.filter((share) => !ids.has(share.id))
        for (const id of ids) shareFiles.delete(id)
        return empty()
      }
      case 'POST /shares': {
        const selected = selectedLibrary(body as Selection)
        if (!selected.length || selected.some((file) => file.status !== 'done'))
          return failure('action_unavailable', 409)
        const ids = selected.map((file) => file.id).sort()
        if ([...shareFiles.values()].some((other) => JSON.stringify(other) === JSON.stringify(ids)))
          return failure('sharing_duplicate', 409)
        if (shares.length >= 8) return failure('sharing_limit', 409)
        const key = randomId()
        const share = {
          cursor: '',
          owner: state.account?.email ?? '',
          createdAt: timestamp(),
          id: key,
          name: selected[0].name,
          count: selected.length,
          icon: selected[0].icon,
          url: 'about:blank#' + key,
        }
        shareFiles.set(key, ids)
        shares.push(share)
        return json(share, 201)
      }
      case 'POST /catalog/refresh': {
        refreshingAt = now()
        state.catalog.status = 'loading'
        state.catalog.progress = 0
        return empty(202)
      }
      case 'DELETE /catalog': {
        refreshingAt = null
        state.catalog.cached = false
        state.catalog.status = 'idle'
        update()
        return empty()
      }
      case 'POST /profile/refresh': {
        profile.apple = appleProfile
        return json(profile)
      }
      case 'DELETE /profile': {
        profile.apple = null
        return empty()
      }
      case 'POST /logout': {
        state.account = null
        return empty()
      }
      case 'POST /login': {
        const value = body as { email?: string; password?: string }
        if (!value?.email || !value.password) return failure('invalid_credentials', 400)
        state.account = account
        account!.signedInAt = timestamp()
        return json(account)
      }
      case 'PUT /admin/device': {
        const value = body as DeviceSettings
        if (
          !value ||
          !deviceModels.some((model) => model.id === value.model) ||
          !macOsVersions.some((os) => os.version === value.osVersion)
        )
          return failure('invalid_device_settings', 400)
        applyDevice(value)
        return json(device)
      }
      case 'POST /admin/device/defaults': {
        applyDevice(device.defaults)
        return json(device)
      }
      case 'POST /admin/device/reset': {
        newIdentity()
        return json(device)
      }
    }
    if (method === 'POST' && /^\/catalog\/[^/]+\/versions$/.test(route)) {
      const app = apps.find((item) => item.id === route.split('/')[2])
      if (!app) return failure('invalid_request', 400)
      const value = body as VersionPageRequest
      const versions = versionsFor(app)
      return json({
        name: app.name,
        revision: 'demo',
        ...page(
          versions,
          new URLSearchParams({
            cursor: value?.cursor ?? '',
            move: value?.move ?? '',
            direction: 'asc',
            limit: String(value?.limit ?? defaultVersionPageSize),
          }),
        ),
      })
    }
    if (method === 'POST' && /^\/downloads\/[^/]+\/cancel$/.test(route)) {
      const transfer = transfers.get(route.split('/')[2])
      if (!transfer) return failure('not_found', 404)
      Object.assign(transfer.file, { status: 'canceled', speed: 0 })
      transfers.delete(transfer.file.id)
      reschedule()
      return empty()
    }
    if (method === 'GET' && route.startsWith('/shares/')) {
      const share = shares.find((share) => share.id === route.split('/')[2])
      if (share && /^\/shares\/[^/]+\/progress\/[a-z0-9]{7}$/.test(route))
        return json({ stage: 'waiting', percent: 0, skipped: 0 } satisfies InstallationProgress)
      return share ? json(share) : failure('not_found', 404)
    }
    return failure('operation_unavailable', 403)
  }

  async function upload(
    file: File,
    key: string,
    progress: (value: UploadProgress) => void,
    signal: AbortSignal,
  ) {
    if (signal.aborted) throw new DOMException('Upload canceled', 'AbortError')
    const name = file.name.replace(/\.ipa$/i, '')
    const row: LibraryItem = {
      cursor: '',
      id: key,
      appId: String(9_000_000_000 + file.size),
      name,
      icon: catalog[0].icon,
      canManage: true,
      status: 'downloading',
      received: 0,
      total: file.size,
      size: file.size,
      speed: downloadSpeed,
      error: '',
      version: '1.0',
      minimumOs: '15.0',
      createdAt: timestamp(),
    }
    enqueue(row)
    while (transfers.has(key)) {
      if (signal.aborted) {
        transfers.delete(key)
        row.status = 'canceled'
        row.speed = 0
        reschedule()
        throw new DOMException('Upload canceled', 'AbortError')
      }
      update()
      progress({
        received: row.received,
        total: row.total,
        speed: row.speed,
        status: row.status === 'preparing' || row.status === 'done' ? 'preparing' : 'downloading',
      })
      if (transfers.has(key)) await new Promise((resolve) => setTimeout(resolve, 100))
    }
    if (signal.aborted || row.status === 'canceled')
      throw new DOMException('Upload canceled', 'AbortError')
  }

  update()
  return { request, upload }
}
