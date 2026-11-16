import assert from 'node:assert/strict'
import test from 'node:test'
import { createDemo } from '../src/api/demo.ts'
import type {
  CatalogPage,
  DeviceProfile,
  LibraryPage,
  ProfileSummary,
  State,
  VersionPage,
} from '../src/api/types.ts'

test('prepared data supports filtering, sorting, pagination and read-only queries', async () => {
  const { request } = createDemo()
  const state: State = await request('/state').json()
  assert.equal(state.account?.name, 'Alex Morgan')
  assert.equal(state.auth.state, 'ready')
  const catalog: CatalogPage = await request('/catalog?sort=name&direction=asc&limit=5').json()
  assert.equal(catalog.items.length, 5)
  assert.ok(catalog.total > catalog.items.length)
  assert.ok(catalog.next)
  const next: CatalogPage = await request(
    '/catalog?sort=name&direction=asc&limit=5&cursor=' +
      catalog.items.at(-1)!.cursor +
      '&move=next',
  ).json()
  assert.equal(next.items.length, 5)
  assert.ok(next.items.every((row) => !catalog.items.some((first) => first.id === row.id)))
  const selected: CatalogPage = await request('/catalog?q=Atlas&delisted=true').json()
  assert.deepEqual(
    selected.items.map((app) => app.name),
    ['Atlas Maps'],
  )
  assert.ok(selected.items[0].icon.startsWith('data:image/svg+xml,'))
  const profile: ProfileSummary = await request('/profile').json()
  assert.equal(profile.catalogApps, catalog.total)
  const delisted: CatalogPage = await request('/catalog?delisted=true').json()
  assert.equal(profile.delistedApps, delisted.total)
  const device: DeviceProfile = await request('/admin/device').json()
  assert.equal(device.readOnly, false)
  const library: LibraryPage = await request('/library').json()
  assert.equal(library.total, 8)
  assert.ok(library.items.every((app) => app.status === 'done'))
  const selection = await request('/library/selection', 'POST', {
    ids: [],
    filter: { query: '', delisted: false, mine: false },
  }).json()
  assert.deepEqual(
    selection.readyIds,
    library.items.filter((app) => app.status === 'done').map((app) => app.id),
  )
  const versions: VersionPage = await request(`/catalog/${catalog.items[0].id}/versions`, 'POST', {
    limit: 5,
  }).json()
  assert.equal(versions.items.length, 5)
  assert.equal(versions.items[0].latest, true)
  assert.ok(versions.next)
})

test('virtual downloads run at 50 Mbit/s and pass through preparation before becoming ready', async () => {
  let time = Date.UTC(2_026, 10, 10, 12)
  const { request } = createDemo(() => time)
  const catalog: CatalogPage = await request('/catalog?q=Wave').json()
  const app = catalog.items[0]
  const started = time
  assert.equal(
    request('/downloads', 'POST', { selection: { ids: [app.id], filter: null } }).status,
    202,
  )
  const result: LibraryPage = await request('/library').json()
  const file = result.items.find((file) => file.appId === app.id)!
  assert.equal(result.total, 9)
  assert.equal(file.status, 'downloading')
  assert.equal(file.speed, 6_250_000)
  time += 4_000
  const progress: LibraryPage = await request('/library').json()
  assert.equal(progress.items.find((item) => item.id === file.id)?.received, 25_000_000)
  assert.equal((await request('/state').json()).downloadsActive, true)
  assert.equal((await request('/catalog?q=Wave').json()).items[0].inLibrary, false)
  time = started + Math.ceil((file.total / 6_250_000) * 1_000)
  assert.equal(
    (await request('/library').json()).items.find((item: { id: string }) => item.id === file.id)
      .status,
    'preparing',
  )
  time += 1_900
  assert.equal(
    (await request('/library').json()).items.find((item: { id: string }) => item.id === file.id)
      .status,
    'done',
  )
  assert.equal((await request('/catalog?q=Wave').json()).items[0].inLibrary, true)
  assert.equal((await request('/state').json()).downloadsActive, false)
})

test('canceling and deleting queued downloads starts the next file without touching prepared apps', async () => {
  let time = Date.UTC(2_026, 10, 10, 12)
  const { request } = createDemo(() => time)
  const wave = (await request('/catalog?q=Wave').json()).items[0]
  const trail = (await request('/catalog?q=Trail').json()).items[0]
  request('/downloads', 'POST', { selection: { ids: [wave.id, trail.id], filter: null } })
  const rows: LibraryPage = await request('/library').json()
  const first = rows.items.find((row) => row.appId === wave.id)!
  const next = rows.items.find((row) => row.appId === trail.id)!
  assert.equal(next.status, 'queued')
  time += 1_000
  assert.equal(request(`/downloads/${first.id}/cancel`, 'POST').status, 204)
  const after: LibraryPage = await request('/library').json()
  assert.equal(after.items.find((row) => row.id === first.id)?.status, 'canceled')
  assert.equal(after.items.find((row) => row.id === next.id)?.status, 'downloading')
  request('/library/delete', 'POST', { ids: [first.id, next.id] })
  assert.equal((await request('/library').json()).total, 8)
  assert.equal((await request('/state').json()).downloadsActive, false)
})

test('virtual shares, profile, account and device operations stay inside the mock', async () => {
  const { request } = createDemo()
  const library: LibraryPage = await request('/library').json()
  const file = library.items[0]
  const shared = request('/shares', 'POST', { ids: [file.id] })
  assert.equal(shared.status, 201)
  const share = await shared.json()
  assert.match(share.url, /^about:blank#/)
  assert.equal((await request('/state').json()).shareCount, 1)
  assert.equal(request('/shares', 'POST', { ids: [file.id] }).status, 409)
  request('/shares/stop', 'POST', { ids: [share.id] })
  assert.equal((await request('/state').json()).shareCount, 0)
  const second = await request('/shares', 'POST', { ids: [file.id] }).json()
  const refused = request('/library/delete', 'POST', { ids: [file.id] })
  assert.equal(refused.status, 409)
  assert.deepEqual(await refused.json(), { code: 'shared_files' })
  assert.equal((await request('/library').json()).total, 8)
  assert.equal((await request('/state').json()).shareCount, 1)
  request('/shares/stop', 'POST', { ids: [second.id] })
  request('/library/delete', 'POST', { ids: [file.id] })
  assert.equal((await request('/library').json()).total, 7)
  assert.equal((await request('/state').json()).shareCount, 0)
  request('/profile', 'DELETE')
  assert.equal((await request('/profile').json()).apple, null)
  request('/profile/refresh', 'POST')
  assert.notEqual((await request('/profile').json()).apple, null)
  request('/logout', 'POST')
  assert.equal((await request('/state').json()).account, null)
  request('/login', 'POST', { email: 'synthetic@example.test', password: 'synthetic-only' })
  assert.equal((await request('/state').json()).account.name, 'Alex Morgan')
  const device: DeviceProfile = await request('/admin/device').json()
  const updated = await request('/admin/device', 'PUT', {
    ...device.settings,
    timeZone: 'Europe/Paris',
  }).json()
  assert.equal(updated.settings.timeZone, 'Europe/Paris')
  const reset = await request('/admin/device/reset', 'POST').json()
  assert.notEqual(reset.id, device.id)
  assert.equal(reset.guid, reset.id.replaceAll('-', '').slice(0, 12).toUpperCase())
  assert.equal((await request('/state').json()).account, null)
  for (const [method, path] of [
    ['GET', '/future-endpoint'],
    ['POST', '/library/export'],
  ]) {
    const response = request(path, method)
    assert.equal(response.status, 403)
    assert.deepEqual(await response.json(), { code: 'operation_unavailable' })
  }
})

test('catalog refresh restores cached data and exposes normal progress', async () => {
  let time = Date.UTC(2_026, 10, 10, 12)
  const { request } = createDemo(() => time)
  request('/catalog', 'DELETE')
  assert.equal((await request('/catalog').json()).total, 0)
  request('/catalog/refresh', 'POST')
  time += 1_000
  const loading = await request('/state').json()
  assert.equal(loading.catalog.status, 'loading')
  assert.equal(loading.catalog.progress, 40)
  time += 2_000
  assert.equal((await request('/state').json()).catalog.status, 'ready')
  assert.equal((await request('/catalog').json()).total, 18)
})

test('without browser storage a fresh session restores the initial data', async () => {
  const { request } = createDemo()
  const original: State = await request('/state').json()
  assert.equal(request('/preferences', 'PUT', { language: 'ru', theme: 'light' }).status, 200)
  const table = { sort: 'name', direction: 'asc', delisted: false }
  request('/table-settings/catalog', 'PUT', table)
  const changed: State = await request('/state').json()
  assert.equal(changed.preferences.language, 'ru')
  assert.equal(changed.preferences.theme, 'light')
  assert.deepEqual(changed.tables.catalog.value, table)
  assert.deepEqual(await createDemo().request('/state').json(), original)
})

test('mock IPA uploads report progress, become library entries and support cancellation', async (t) => {
  t.mock.timers.enable({ apis: ['Date', 'setTimeout'], now: Date.UTC(2_026, 10, 10, 12) })
  const { request, upload: uploadFile } = createDemo(() => Date.now())
  const progress: string[] = []
  const upload = uploadFile(
    new File(['synthetic'], 'Travel Notes.ipa'),
    'upload-one',
    (value) => progress.push(value.status),
    new AbortController().signal,
  )
  t.mock.timers.tick(100)
  await Promise.resolve()
  t.mock.timers.tick(2_000)
  await upload
  const library: LibraryPage = await request('/library').json()
  assert.equal(library.total, 9)
  assert.equal(library.items.find((row) => row.id === 'upload-one')?.status, 'done')
  assert.ok(progress.includes('downloading'))
  assert.ok(progress.includes('preparing'))
  const controller = new AbortController()
  const canceled = uploadFile(
    new File(['synthetic'], 'Other Notes.ipa'),
    'upload-two',
    () => {},
    controller.signal,
  )
  controller.abort()
  const rejection = assert.rejects(canceled, { name: 'AbortError' })
  t.mock.timers.tick(100)
  await rejection
  assert.equal(
    (await request('/library').json()).items.find((row: { id: string }) => row.id === 'upload-two')
      .status,
    'canceled',
  )
})

test('share pages, matching selection, settings and bulk stop follow the live API', async () => {
  const { request } = createDemo()
  const library: LibraryPage = await request('/library').json()
  for (const file of library.items)
    assert.equal(request('/shares', 'POST', { ids: [file.id] }).status, 201)
  assert.equal(
    request('/shares', 'POST', { ids: library.items.slice(0, 2).map((file) => file.id) }).status,
    409,
  )
  const first = await request('/shares?limit=3&sort=name&direction=asc').json()
  assert.equal(first.total, 8)
  assert.equal(first.items.length, 3)
  assert.ok(first.next)
  assert.equal((await request('/shares/' + first.items[0].id).json()).name, first.items[0].name)
  const selected = await request('/shares/selection', 'POST', {
    ids: [],
    filter: { query: first.items[0].name, delisted: false, mine: false },
  }).json()
  assert.deepEqual(selected.ids, [first.items[0].id])
  const settings = { sort: 'name', direction: 'asc' }
  const before: State = await request('/state').json()
  request('/table-settings/shares', 'PUT', settings)
  const after: State = await request('/state').json()
  assert.deepEqual(after.tables.shares.value, settings)
  assert.deepEqual(after.tables.library, before.tables.library)
  assert.equal(
    request('/shares/stop', 'POST', { ids: first.items.map((share: { id: string }) => share.id) })
      .status,
    204,
  )
  assert.equal((await request('/state').json()).shareCount, 5)
  assert.equal(request('/shares/' + first.items[0].id).status, 404)
})

test('bulk demo requests reject more than 512 IDs without changing the library', async () => {
  const { request } = createDemo()
  const ids = Array.from({ length: 513 }, (_, i) => String(i))
  const selection = { ids, filter: null }
  for (const [path, body] of [
    ['/library/selection', selection],
    ['/downloads', { selection }],
    ['/shares', selection],
    ['/library/delete', selection],
  ] as const) {
    const response = request(path, 'POST', body)
    assert.equal(response.status, 400)
    assert.deepEqual(await response.json(), { code: 'selection_too_large' })
  }
  assert.equal((await request('/library').json()).total, 8)
})

const filtered = (query: unknown) => ({
  ids: [],
  filter: { query, delisted: false, mine: false },
})

test('demo tables and bulk actions enforce the same Unicode search minimum', async () => {
  const { request } = createDemo()
  for (const query of ['a', 'ab', 'ёж', '🙂🙂']) {
    for (const path of ['/catalog', '/library', '/shares'])
      assert.equal(request(path + '?q=' + encodeURIComponent(query)).status, 400)
    assert.equal(request('/library/selection', 'POST', filtered(query)).status, 400)
  }
  assert.equal(request('/library/selection', 'POST', filtered(17)).status, 400)
  for (const query of ['', 'Atl', '🙂🙂🙂'])
    assert.equal(request('/catalog?q=' + encodeURIComponent(query)).status, 200)
})
