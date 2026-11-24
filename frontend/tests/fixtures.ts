import { readFileSync } from 'node:fs'
import { test as base, expect, type Page } from '@playwright/test'
import type {
  CatalogItem,
  InstallationProgress,
  LibraryItem,
  LibrarySelectionResult,
  Selection,
  State,
} from '../src/api/types'

// A test changes only the parts of the state it is about.
export type StateOverrides = Partial<Omit<State, 'mode' | 'auth' | 'catalog'>> & {
  mode?: Partial<State['mode']>
  auth?: Partial<State['auth']>
  catalog?: Partial<State['catalog']>
}

// Every page must finish its test without uncaught errors.
export const test = base.extend({
  page: async ({ page }, use) => {
    await use(page)
    expect(await page.pageErrors()).toEqual([])
  },
})
export { expect }

export const devicePlatform = { model: 'MacBookPro18,1', osVersion: '26.5' }

// The API sends its own projection of applekit's platform lists.
export const deviceModels = JSON.parse(
  readFileSync(new URL('../../applekit/assets/device-models.json', import.meta.url), 'utf8'),
).map(({ id, name, minOS, maxOS }: { id: string; name: string; minOS: number; maxOS: number }) => ({
  id,
  name,
  minOs: minOS,
  maxOs: maxOS,
}))

export const deviceVersions = JSON.parse(
  readFileSync(new URL('../../applekit/assets/macos-versions.json', import.meta.url), 'utf8'),
).map(({ version }: { version: string }) => ({ version }))

export const account = {
  email: 'example@example.test',
  name: 'Example',
  id: '12345',
  country: 'RU',
  storefront: '143469',
  signedInAt: '',
}

export const catalog: CatalogItem[] = [
  {
    id: '1001',
    name: 'Example App',
    delisted: false,
    inLibrary: false,
    updateAvailable: false,
    size: 123_000_000,
    icon: '',
    purchasedAt: '',
  },
  {
    id: '1002',
    name: 'Delisted App',
    delisted: true,
    inLibrary: false,
    updateAvailable: false,
    size: null,
    icon: '',
    purchasedAt: '',
  },
]

export async function fakeApi(
  page: Page,
  signedIn = true,
  debug = false,
  downloaded = false,
  admin = false,
  purchaseRejected = false,
) {
  const apps = catalog.map((app) => ({ ...app, inLibrary: downloaded && app.id === '1001' }))
  let preferences = { language: 'ru', theme: 'dark', revision: 1 }
  let catalogCached = true
  let signed = signedIn,
    logins = 0,
    signedInAt = ''
  let activeShare: { id: string; url: string; count: number; icon: string } | null = null
  const overrides: StateOverrides = {}
  const tables: State['tables'] = {
    shares: { value: { sort: 'createdAt', direction: 'desc' }, revision: 1 },
    catalog: { value: { sort: 'name', direction: 'asc', delisted: true }, revision: 1 },
    library: { value: { sort: 'name', direction: 'asc', mine: true }, revision: 1 },
  }
  await page.route('**/api/**', async (route) => {
    const request = route.request(),
      url = new URL(request.url()),
      path = url.pathname
    if (path === '/api/state') {
      const { mode, auth, catalog: catalogState, ...rest } = overrides
      await route.fulfill({
        json: {
          csrf: 'test-token',
          mode: {
            public: false,
            sharedLibrary: false,
            service: false,
            admin,
            debug,
            ish: false,
            ...mode,
          },
          account: signed ? { ...account, signedInAt } : null,
          auth: { state: 'ready', busy: false, error: '', ...auth },
          catalog: {
            status: signed ? (purchaseRejected ? 'failed' : 'ready') : 'idle',
            progress: 0,
            error: purchaseRejected ? 'purchase_history_authentication_required' : '',
            cached: catalogCached,
            ...catalogState,
          },
          downloadsActive: false,
          shareCount: activeShare ? 1 : 0,
          tables,
          preferences,
          ...rest,
        } satisfies State,
      })
      return
    }
    if (path === '/api/debug/info') {
      await route.fulfill({
        json: {
          executable: '/fixture/appleseum',
          arguments: [],
          startedAt: '2026-11-05T00:00:00Z',
          uptimeSeconds: 60,
          version: 'fixture',
          runtime: 'fixture',
          goroutines: 5,
          heapBytes: 1_234,
          capacity: 1_000,
        },
      })
      return
    }
    if (path === '/api/profile') {
      await route.fulfill({
        json: {
          catalogApps: 2,
          delistedApps: 1,
          signedInAt: '',
          apple: null,
          appleError: '',
        },
      })
      return
    }
    if (path === '/api/admin/device') {
      const settings = { locale: 'en-US', timeZone: 'UTC' }
      await route.fulfill({
        json: {
          readOnly: false,
          settings: { ...devicePlatform, ...settings },
          defaults: { ...devicePlatform, ...settings },
          models: deviceModels,
          osVersions: deviceVersions,
          id: '00000000-0000-4000-8000-000000000001',
          guid: '000000000000',
          clientVersion: 'fixture',
        },
      })
      return
    }
    if (path === '/api/preferences') {
      preferences = { ...request.postDataJSON(), revision: preferences.revision + 1 }
      await route.fulfill({ json: preferences })
      return
    }
    if (path.startsWith('/api/table-settings/')) {
      const resource = path.split('/').at(-1) as keyof typeof tables
      const saved = tables as unknown as Record<string, { value: unknown; revision: number }>
      saved[resource] = { value: request.postDataJSON(), revision: tables[resource].revision + 1 }
      await route.fulfill({ json: tables[resource] })
      return
    }
    if (path === '/api/logout') {
      signed = false
      await route.fulfill({ status: 204 })
      return
    }
    if (path === '/api/login') {
      logins++
      await new Promise((resolve) => setTimeout(resolve, 350))
      if (logins === 1) {
        await route.fulfill({ status: 401, json: { code: 'two_factor_required' } })
      } else {
        signed = true
        await route.fulfill({ json: account })
      }
      return
    }
    if (path === '/api/catalog') {
      if (route.request().method() === 'DELETE') {
        catalogCached = false
        await route.fulfill({ status: 204 })
        return
      }
      await route.fulfill({
        json: {
          items: catalogCached ? apps : [],
          total: catalogCached ? apps.length : 0,
          next: false,
          previous: false,
        },
      })
      return
    }
    if (path === '/api/library') {
      await route.fulfill({
        json: {
          items: [
            {
              id: '01900000-0000-7000-8000-000000000001',
              appId: '1001',
              name: 'Example App',
              status: 'done',
              received: 1_234_567,
              total: 1_234_567,
              speed: 0,
              error: '',
              version: '1.0',
              minimumOs: '',
              size: 1_234_567,
              createdAt: '2026-10-25T10:00:00Z',
              canManage: true,
              icon: '',
            } satisfies LibraryItem,
          ],
          total: 1,
          next: false,
          previous: false,
        },
      })
      return
    }
    if (path === '/api/shares' && request.method() === 'GET') {
      await route.fulfill({
        json: {
          items: activeShare
            ? [{ ...activeShare, name: 'Test app', owner: '', createdAt: '2026-11-09T12:00:00Z' }]
            : [],
          total: activeShare ? 1 : 0,
          next: false,
          previous: false,
        },
      })
      return
    }
    if (path === '/api/shares/stop') {
      activeShare = null
      await route.fulfill({ status: 204 })
      return
    }
    if (/^\/api\/shares\/test-share\/progress\/[a-z0-9]{7}$/.test(path)) {
      await route.fulfill(
        activeShare
          ? {
              json: { stage: 'waiting', percent: 0, skipped: 0 } satisfies InstallationProgress,
            }
          : { status: 404, json: { code: 'not_found' } },
      )
      return
    }
    if (path === '/api/shares/test-share' && request.method() === 'GET') {
      await route.fulfill(
        activeShare ? { json: activeShare } : { status: 404, json: { code: 'not_found' } },
      )
      return
    }
    if (path === '/api/shares') {
      activeShare = {
        id: 'test-share',
        url: 'itms-services://localhost/?action=download-manifest&url=https%3A%2F%2F192.168.50.10%3A8443%2Fmanifests%2Ftest.plist',
        count: 1,
        icon: '',
      }
      await route.fulfill({ status: 201, json: activeShare })
      return
    }
    if (path === '/api/catalog/selection' || path === '/api/library/selection') {
      const input: Selection = request.postDataJSON()
      const library = path === '/api/library/selection'
      let ids = library
        ? ['01900000-0000-7000-8000-000000000001']
        : apps.filter((app) => !app.inLibrary).map((app) => app.id)
      if (input.ids?.length) ids = ids.filter((id) => input.ids.includes(id))
      await route.fulfill({
        json: library
          ? ({ ids, readyIds: ids, manageIds: ids } satisfies LibrarySelectionResult)
          : { ids },
      })
      return
    }
    if (path === '/api/catalog/refresh') {
      await route.fulfill({ status: 202 })
      return
    }
    await route.fulfill({ status: 204 })
  })
  return {
    setSignedInAt: (value: string) => (signedInAt = value),

    setState: ({ mode, auth, catalog, ...rest }: StateOverrides) => {
      Object.assign(overrides, rest)
      overrides.mode = { ...overrides.mode, ...mode }
      overrides.auth = { ...overrides.auth, ...auth }
      overrides.catalog = { ...overrides.catalog, ...catalog }
    },

    setShare: (value: typeof activeShare) => {
      activeShare = value
    },
  }
}

export async function chooseOption(page: Page, label: string, value: string) {
  await page.getByRole('button', { name: label, exact: true }).click()
  const dialog = page.getByRole('dialog', { name: label, exact: true })
  if (await dialog.getByRole('combobox').count()) {
    await dialog.getByRole('combobox').fill(value)
    await dialog.getByRole('option').click()
  } else await dialog.getByRole('option').filter({ hasText: value }).click()
  await expect(dialog).toHaveCount(0)
  await page.waitForFunction(() => !history.state?.modal)
}

export async function publicPreview(page: Page) {
  const preview = test.info().project.use.baseURL!
  await page.route('https://appleseum.test/**', async (route) => {
    const url = new URL(route.request().url())
    const response = await route.fetch({ url: new URL(url.pathname + url.search, preview).href })
    // The preview serves extensionless route pages untyped; WebKit downloads a fulfilled untyped page.
    const page = route.request().resourceType() === 'document'
    await route.fulfill({ response, ...(page && { contentType: 'text/html' }) })
  })
  await page.addInitScript(() => localStorage.setItem('appleseum.sessionKey', 'a'.repeat(43)))
}
