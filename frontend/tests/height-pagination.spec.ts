import type { Page } from '@playwright/test'
import { test, expect } from './fixtures.ts'

async function lists(page: Page, downloadsActive = false) {
  const requests: URL[] = []
  const items = Array.from({ length: 35 }, (_, index) => ({
    id: String(index + 1),
    name: `Application ${index + 1}`,
    delisted: true,
    inLibrary: false,
    updateAvailable: false,
    icon: '',
    purchasedAt: '',
    size: null,
  }))
  await page.route(
    (url) => url.pathname.startsWith('/api/'),
    async (route) => {
      const url = new URL(route.request().url())
      if (url.pathname === '/api/state')
        return route.fulfill({
          json: {
            account: {
              email: 'test@example.test',
              name: 'Test',
              id: '1',
              country: 'RU',
              storefront: '',
              signedInAt: '',
            },
            mode: {
              public: false,
              sharedLibrary: false,
              service: false,
              debug: false,
              admin: false,
              ish: false,
            },
            auth: { state: 'ready', busy: false, error: '' },
            catalog: { status: 'ready', progress: 100, error: '', cached: true },
            shareCount: 0,
            csrf: 'test',
            downloadsActive,
            tables: {
              catalog: { revision: 1, value: { sort: 'name', direction: 'asc', delisted: true } },
              library: { revision: 1, value: { sort: 'name', direction: 'asc', mine: true } },
              shares: { revision: 1, value: { sort: 'name', direction: 'asc' } },
            },
            preferences: { language: 'ru', theme: 'light', revision: 1 },
          },
        })
      if (url.pathname.endsWith('/selection'))
        return route.fulfill({
          json: { ids: route.request().postDataJSON().ids ?? [], readyIds: [], manageIds: [] },
        })
      if (url.pathname === '/api/catalog') {
        requests.push(url)
        const size = Number(url.searchParams.get('limit'))
        const cursor = url.searchParams.get('cursor') ?? ''
        const boundary = Number(cursor.split('-')[1]) || 0
        const start =
          url.searchParams.get('move') === 'prev' ? Math.max(0, boundary - 1 - size) : boundary
        const rows = items.slice(start, start + size)
        return route.fulfill({
          json: {
            total: items.length,
            items: rows.map((row, i) => ({ ...row, cursor: `after-${start + i + 1}` })),
            next: start + size < items.length,
            previous: start > 0,
          },
        })
      }
      return route.fulfill({ json: {} })
    },
  )
  return requests
}

for (const width of [390, 1_280]) {
  test(`automatic reduction fits real rows and resumes at the visible boundary (${width})`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 470 })
    const requests = await lists(page)
    await page.goto('/available')
    const rows = page.locator('tbody tr')
    const size = page.getByRole('button', { name: 'Строк', exact: true })
    await expect(size).toHaveText(/^\d+$/)
    await expect.poll(async () => rows.count()).toBeLessThan(10)
    const count = await rows.count()
    expect(count).toBeGreaterThan(0)
    await expect(size).toContainText(String(count))
    expect(requests).toHaveLength(1)
    expect(requests[0]!.searchParams.get('limit')).toBe('10')
    expect(await page.evaluate(() => localStorage.getItem('appleseum.pageSize.catalog'))).toBeNull()
    expect(
      await page.locator('.pagination').evaluate((el) => el.getBoundingClientRect().bottom),
    ).toBeLessThanOrEqual(471)
    await page.locator('.app-footer').evaluate((el) => {
      el.style.minHeight = '500px'
    })
    await page.waitForTimeout(200)
    expect(await rows.count()).toBe(count)
    expect(requests).toHaveLength(1)
    await page.getByRole('button', { name: 'Вперёд', exact: true }).click()
    await expect(rows.first()).toContainText(`Application ${count + 1}`)
    expect(requests[1]!.searchParams.get('cursor')).toBe(`after-${count}`)
    expect(requests[1]!.searchParams.get('limit')).toBe(String(count))
    await page.getByRole('button', { name: 'Назад', exact: true }).click()
    await expect(rows.first()).toContainText('Application 1')
    const before = requests.length
    const smaller = await rows.count()
    await page.setViewportSize({ width, height: 1_100 })
    await expect(size).toContainText(String(smaller))
    // Increasing available space never increases the count or fetches more rows.
    await page.waitForTimeout(200)
    expect(await rows.count()).toBe(smaller)
    expect(requests).toHaveLength(before)
  })
}

test('row growth reduces locally; shrinking it again does not refill the page', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1_280, height: 800 })
  const requests = await lists(page)
  await page.goto('/available')
  const rows = page.locator('tbody tr')
  await expect(rows).toHaveCount(10)
  const firstCount = await rows.count()
  await rows.first().evaluate((el) => {
    el.style.height = '410px'
  })
  await expect.poll(() => rows.count()).toBeLessThan(firstCount)
  const reduced = await rows.count()
  expect(requests).toHaveLength(1)
  await rows.first().evaluate((el) => {
    el.style.height = ''
  })
  await page.waitForTimeout(200)
  expect(await rows.count()).toBe(reduced)
  expect(requests).toHaveLength(1)
})

test('manual choice persists and disables fitting, with an explicit return to automatic sizing', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1_280, height: 470 })
  await lists(page)
  await page.goto('/available')
  const size = page.getByRole('button', { name: 'Строк', exact: true })
  await expect(size).toHaveText(/^\d+$/)
  await size.click()
  await page.getByRole('option', { name: '20', exact: true }).click()
  await expect(size).toHaveText('20')
  await expect(page.locator('tbody tr')).toHaveCount(20)
  expect(await page.evaluate(() => localStorage.getItem('appleseum.pageSize.catalog'))).toBe('20')
  await page.reload()
  await expect(size).toHaveText('20')
  await expect(page.locator('tbody tr')).toHaveCount(20)
  await page.setViewportSize({ width: 820, height: 400 })
  await expect(size).toHaveText('20')
  await size.click()
  await page.getByRole('option', { name: 'Подогнать по высоте', exact: true }).click()
  await expect(size).toHaveText(/^\d+$/)
  await expect.poll(() => page.locator('tbody tr').count()).toBeLessThan(10)
  expect(await page.evaluate(() => localStorage.getItem('appleseum.pageSize.catalog'))).toBeNull()
})

test('existing manual preferences stay fixed without rewriting storage', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('illbeback.pageSize.catalog', '25'))
  await page.setViewportSize({ width: 390, height: 470 })
  await lists(page)
  await page.goto('/available')
  await expect(page.getByRole('button', { name: 'Строк', exact: true })).toHaveText('25')
  await expect(page.locator('tbody tr')).toHaveCount(25)
  expect(await page.evaluate(() => localStorage.getItem('appleseum.pageSize.catalog'))).toBeNull()
})

test('live download status changes shrink the page without refetching, and completion does not grow it', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1_280, height: 600 })
  await lists(page, true)
  let status = 'done'
  const requests: URL[] = []
  await page.route(
    (url) => url.pathname === '/api/library',
    async (route) => {
      const url = new URL(route.request().url())
      requests.push(url)
      const size = Number(url.searchParams.get('limit'))
      const rows = Array.from({ length: size }, (_, i) => ({
        id: String(i + 1),
        appId: String(i + 1),
        name: `Download ${i + 1}`,
        status,
        received: 50_000_000,
        total: 100_000_000,
        size: 100_000_000,
        speed: 1_000_000,
        version: '1.0',
        minimumOs: '16.0',
        createdAt: '',
        error: '',
        icon: '',
        canManage: true,
      }))
      await route.fulfill({
        json: {
          total: 35,
          items: rows.map((row, i) => ({ ...row, cursor: `after-${i + 1}` })),
          next: true,
          previous: false,
        },
      })
    },
  )
  await page.goto('/downloads')
  const rows = page.locator('tbody tr')
  const size = page.getByRole('button', { name: 'Строк', exact: true })
  await expect(size).toHaveText(/^\d+$/)
  await expect.poll(() => rows.count()).toBeLessThan(10)
  const initial = await rows.count()
  expect(initial).toBeGreaterThan(1)
  expect(requests).toHaveLength(1)
  status = 'downloading'
  await expect.poll(() => rows.count()).toBeLessThan(initial)
  const reduced = await rows.count()
  expect(requests).toHaveLength(2)
  await expect(size).toContainText(String(reduced))
  status = 'done'
  await expect.poll(() => requests.length).toBe(3)
  await expect(page.locator('.download-progress')).toHaveCount(0)
  await page.waitForTimeout(200)
  expect(await rows.count()).toBe(reduced)
  expect(requests).toHaveLength(3)
})

test('version picker stays fixed at five and does not offer height fitting', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 470 })
  await lists(page)
  const limits: number[] = []
  await page.route(
    (url) => url.pathname === '/api/catalog/1/versions',
    async (route) => {
      limits.push(route.request().postDataJSON().limit)
      await route.fulfill({
        json: {
          name: 'Application 1',
          total: 20,
          next: 'next-versions',
          previous: false,
          items: Array.from({ length: 5 }, (_, i) => ({
            id: String(i + 1),
            version: `1.${i}`,
            minimumOs: '16.0',
            latest: i === 0,
            error: '',
          })),
        },
      })
    },
  )
  await page.goto('/available')
  await page.getByRole('checkbox', { name: 'Application 1', exact: true }).click()
  await page.getByRole('button', { name: /^Скачать/ }).click()
  const dialog = page.getByRole('dialog', { name: 'Скачать', exact: true })
  await dialog.getByRole('radio', { name: 'Выбрать конкретную версию', exact: true }).check()
  await expect(dialog.locator('tbody tr')).toHaveCount(5)
  expect(limits).toEqual([5])
  const size = dialog.getByRole('button', { name: 'Строк', exact: true })
  await expect(size).toHaveText('5')
  await size.click()
  const options = page.getByRole('dialog', { name: 'Строк', exact: true })
  await expect(
    options.getByRole('option', { name: 'Подогнать по высоте', exact: true }),
  ).toHaveCount(0)
})

test('the page-size plus button stops at 128', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('appleseum.pageSize.catalog', '127'))
  await page.setViewportSize({ width: 1_280, height: 700 })
  await lists(page)
  await page.goto('/available')
  const size = page.getByRole('button', { name: 'Строк', exact: true })
  await expect(size).toHaveText('127')
  await size.click()
  const more = page.getByRole('button', { name: 'Больше строк', exact: true })
  await more.click()
  await expect(size).toHaveText('128')
  await expect(more).toBeDisabled()
  await page.locator('.page-size-options').press('ArrowRight')
  await expect(size).toHaveText('128')
  expect(await page.evaluate(() => localStorage.getItem('appleseum.pageSize.catalog'))).toBe('128')
})

for (const width of [390, 1_280]) {
  test(`paginator resets only its manual size and stays automatic after reload (${width})`, async ({
    page,
  }) => {
    await page.addInitScript(() => {
      if (sessionStorage.getItem('height-reset-fixture')) return
      sessionStorage.setItem('height-reset-fixture', '1')
      localStorage.setItem('appleseum.pageSize.catalog', '20')
      localStorage.setItem('illbeback.pageSize.catalog', '50')
      localStorage.setItem('appleseum.pageSize.library', '25')
      localStorage.setItem('appleseum.pageSize.versions.v2', '5')
    })
    await page.setViewportSize({ width, height: 470 })
    await lists(page)
    await page.goto('/available')
    const size = page.getByRole('button', { name: 'Строк', exact: true })
    await expect(size).toHaveText('20')
    await page.getByRole('button', { name: 'Ещё', exact: true }).click()
    const menu = page.getByRole('dialog', { name: 'Ещё', exact: true })
    await expect(
      menu.getByRole('button', { name: 'Авто высота таблицы', exact: true }),
    ).toHaveCount(0)
    await menu.getByRole('button', { name: 'Закрыть', exact: true }).click()
    await size.click()
    await page.getByRole('option', { name: 'Подогнать по высоте', exact: true }).click()
    await expect(menu).toHaveCount(0)
    await expect(size).toHaveText(/^\d+$/)
    await expect.poll(() => page.locator('tbody tr').count()).toBeLessThan(10)
    const saved = await page.evaluate(() => ({
      catalog: localStorage.getItem('appleseum.pageSize.catalog'),
      legacy: localStorage.getItem('illbeback.pageSize.catalog'),
      library: localStorage.getItem('appleseum.pageSize.library'),
      versions: localStorage.getItem('appleseum.pageSize.versions.v2'),
    }))
    expect(saved).toEqual({ catalog: null, legacy: null, library: '25', versions: '5' })
    await page.reload()
    await expect(size).toHaveText(/^\d+$/)
  })
}

test('a phone loads ten rows first and can fit six without an extra request', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 1_400 })
  const requests = await lists(page)
  await page.goto('/available')
  const rows = page.locator('tbody tr')
  await expect(rows).toHaveCount(10)
  const height = await page.locator('main').evaluate((main) => {
    const items = main.querySelectorAll('tbody tr')
    const controlsHeight =
      main.getBoundingClientRect().bottom - items[9]!.getBoundingClientRect().bottom
    return Math.ceil(items[5]!.getBoundingClientRect().bottom + controlsHeight + 4)
  })
  await page.setViewportSize({ width: 390, height })
  await expect(rows).toHaveCount(6)
  await expect(page.getByRole('button', { name: 'Строк', exact: true })).toHaveText('6')
  expect(requests).toHaveLength(1)
  expect(requests[0]!.searchParams.get('limit')).toBe('10')
})
