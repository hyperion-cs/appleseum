import { test, expect, account, catalog, fakeApi } from './fixtures.ts'

test('profile sign-in time follows renewed state without refreshing the cached profile', async ({
  page,
}) => {
  const control = await fakeApi(page)
  await page.route('**/api/profile', (route) =>
    route.fulfill({
      json: {
        catalogApps: 2,
        delistedApps: 1,
        signedInAt: '2026-11-04T20:17:00Z',
        apple: { nickname: 'Cached profile' },
        appleError: '',
      },
    }),
  )
  const calls: string[] = []
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname
    if (path.startsWith('/api/')) calls.push(request.method() + ' ' + path)
  })
  await page.clock.install()
  await page.goto('/profile')
  const field = page.locator('.profile-field').filter({ hasText: 'Вход выполнен' })
  await expect(page.getByText('Cached profile', { exact: true })).toBeVisible()
  await expect(field).toHaveCount(0)
  control.setSignedInAt('2026-11-04T20:17:00Z')
  await page.clock.runFor(2_600)
  await expect(field.locator('dd')).toContainText('04.11.2026')
  const previous = await field.locator('dd').innerText()
  control.setSignedInAt('2026-11-05T01:23:00Z')
  await page.clock.runFor(2_600)
  await expect(field.locator('dd')).toContainText('05.11.2026')
  await expect(field.locator('dd')).not.toHaveText(previous)
  await expect(page.getByText('Cached profile', { exact: true })).toBeVisible()
  expect(calls.filter((call) => call === 'GET /api/profile')).toHaveLength(1)
  expect(calls.filter((call) => call === 'POST /api/profile/refresh')).toHaveLength(0)
  expect(calls.filter((call) => call === 'POST /api/login')).toHaveLength(0)
})

for (const [width, language, theme] of [
  [375, 'ru', 'dark'],
  [1_280, 'en', 'light'],
] as const) {
  test(`debug summary labels catalog progress and marks the active session at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    const control = await fakeApi(page, true, true)
    control.setState({
      preferences: { language, theme, revision: 1 },
      catalog: { status: 'idle', progress: 42 },
    })
    await page.route('**/api/debug?*', (route) =>
      route.fulfill({
        json: {
          runtime: 'fixture',
          executable: '/fixture/appleseum',
          arguments: [],
          uptimeSeconds: 60,
          heapBytes: 1_234,
          goroutines: 5,
          items: [],
          total: 0,
        },
      }),
    )
    await page.goto('/debug')
    const summary = page.locator('.debug-summary'),
      session = summary.locator('.debug-session')
    await expect(session).toHaveText(language === 'ru' ? 'Сессия активна' : 'Session active')
    await expect(session.locator('.connection-dot')).toBeVisible()
    const color = await session
      .locator('.connection-dot')
      .evaluate((node) => getComputedStyle(node).backgroundColor)
    const [red, green, blue] = color.match(/\d+/g)!.map(Number)
    expect(green!).toBeGreaterThan(red!)
    expect(green!).toBeGreaterThan(blue!)
    for (const status of ['idle', 'ready', 'failed']) {
      control.setState({ catalog: { status: status } })
      await page.reload()
      await expect(summary).toBeVisible()
      await expect(summary).not.toContainText('%')
      await expect(summary).not.toContainText(status)
    }
    control.setState({ catalog: { status: 'loading' } })
    await page.reload()
    await expect(summary).toContainText(
      language === 'ru' ? 'Обновление каталога: 42%' : 'Refreshing catalog: 42%',
    )
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    control.setState({ account: null, catalog: { status: 'idle' } })
    await page.reload()
    await expect(session).toHaveText(language === 'ru' ? 'Нет сессии' : 'No session')
    await expect(session.locator('.connection-dot')).toHaveCount(0)
  })
}

test('custom debug type list sends repeated filter values and clears the filter', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1_280, height: 900 })
  await fakeApi(page, true, true)
  const sources: string[][] = []
  await page.route('**/api/debug?*', (route) => {
    sources.push(new URL(route.request().url()).searchParams.getAll('source'))
    return route.fulfill({
      json: {
        runtime: 'fixture',
        executable: '/fixture/appleseum',
        arguments: [],
        uptimeSeconds: 60,
        heapBytes: 1_234,
        goroutines: 5,
        capacity: 1_000,
        version: 'fixture',
        items: sources.at(-1)?.includes('System')
          ? [
              {
                id: 1,
                at: '2026-11-05T00:00:00Z',
                source: 'System',
                method: '',
                target: 'ipa_save',
                status: 0,
                durationMs: 0,
                detail: 'storage_failed',
                hasResponse: false,
                responseBytes: 0,
                responseTruncated: false,
              },
            ]
          : [],
        total: sources.at(-1)?.includes('System') ? 1 : 0,
        next: false,
        previous: false,
        startedAt: '2026-11-05T00:00:00Z',
      },
    })
  })
  await page.goto('/debug')
  await expect(page.locator('select')).toHaveCount(0)
  const trigger = page.getByRole('button', { name: 'Тип', exact: true })
  await trigger.click()
  let dialog = page.getByRole('dialog', { name: 'Тип', exact: true })
  await expect(dialog.getByRole('option')).toHaveCount(5)
  await expect(dialog.getByRole('combobox')).toHaveCount(0)
  await dialog.getByRole('option', { name: 'Раздача', exact: true }).click()
  await expect(trigger).toHaveText('Раздача')
  await expect.poll(() => sources.at(-1)).toEqual(['Hosting'])
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole('listbox')).toHaveAttribute('aria-multiselectable', 'true')
  await expect(dialog.getByRole('option', { name: 'Раздача', exact: true })).toHaveAttribute(
    'aria-selected',
    'true',
  )
  await dialog.getByRole('listbox').press('Escape')
  await expect(dialog).toHaveCount(0)
  for (const width of [375, 1_280]) {
    await page.setViewportSize({ width, height: 900 })
    expect(
      await trigger.locator('span').evaluate((node) => {
        const range = document.createRange()
        range.selectNodeContents(node)
        return range.getClientRects().length
      }),
    ).toBe(1)
  }
  await trigger.click()
  dialog = page.getByRole('dialog', { name: 'Тип', exact: true })
  await dialog.getByRole('listbox').press('End')
  await dialog.getByRole('listbox').press('Enter')
  await expect(trigger).toHaveText('Категории: 2')
  await expect.poll(() => sources.at(-1)).toEqual(['Hosting', 'System'])
  await expect(dialog.getByRole('option', { name: 'Система', exact: true })).toHaveAttribute(
    'aria-selected',
    'true',
  )
  await expect(page.locator('.debug-table tbody tr')).toContainText('Система')
  await expect(page.locator('.debug-table tbody [role=checkbox]')).toHaveCount(0)
  await dialog.getByRole('option', { name: 'Раздача', exact: true }).click()
  await expect(trigger).toHaveText('Система')
  await expect.poll(() => sources.at(-1)).toEqual(['System'])
  await dialog.getByRole('listbox').press('Home')
  await dialog.getByRole('listbox').press('Enter')
  await expect.poll(() => sources.at(-1)).toEqual([])
  await expect(trigger).toHaveText('Все события')
  await expect(dialog.getByRole('option', { name: 'Все события', exact: true })).toHaveAttribute(
    'aria-selected',
    'true',
  )
  await expect(dialog.getByRole('option', { name: 'Система', exact: true })).toHaveAttribute(
    'aria-selected',
    'false',
  )
})

for (const resource of ['profile', 'debug']) {
  test(`${resource} cache deletion requires confirmation and cancellation sends no request`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 375, height: 812 })
    await fakeApi(page, true, true)
    let removed = false,
      reject = true,
      deletes = 0
    await page.route(`**/api/${resource}{,?*}`, (route) => {
      if (route.request().method() === 'DELETE') {
        deletes++
        if (reject) return route.fulfill({ status: 503, json: { code: 'storage_failed' } })
        removed = true
        return route.fulfill({ status: 204 })
      }
      return route.fulfill({
        json:
          resource === 'profile'
            ? {
                catalogApps: 1,
                delistedApps: 0,
                signedInAt: '',
                apple: removed ? null : { nickname: 'Synthetic profile' },
                appleError: '',
              }
            : {
                executable: '/fixture/appleseum',
                arguments: [],
                runtime: 'fixture',
                uptimeSeconds: 60,
                heapBytes: 1_234,
                goroutines: 5,
                capacity: 1_000,
                version: 'fixture',
                startedAt: '2026-11-05T00:00:00Z',
                items: [],
                total: 0,
                next: false,
                previous: false,
              },
      })
    })
    await page.goto('/' + resource)
    const title = resource === 'profile' ? 'Очистить кэш профиля' : 'Очистить весь журнал'

    async function open() {
      if (resource === 'profile')
        await page.getByRole('button', { name: 'Ещё', exact: true }).click()
      await page.getByRole('button', { name: title, exact: true }).click()
    }

    await open()
    const dialog = page.getByRole('dialog', { name: title, exact: true })
    await expect(dialog).toBeVisible()
    expect(deletes).toBe(0)
    await dialog.getByRole('button', { name: 'Отмена', exact: true }).click()
    await page.waitForFunction(() => !history.state?.modal)
    expect(deletes).toBe(0)
    await open()
    await dialog.getByRole('button', { name: 'Очистить', exact: true }).click()
    const failure = page.getByRole('dialog', { name: 'Действие не выполнено', exact: true })
    await expect(dialog).toHaveCount(0)
    await expect(page.getByRole('dialog')).toHaveCount(1)
    await expect(failure.getByRole('button')).toHaveCount(2)
    await failure.getByRole('button', { name: 'Понятно', exact: true }).click()
    await page.waitForFunction(() => !history.state?.modal)
    reject = false
    await open()
    await dialog.getByRole('button', { name: 'Очистить', exact: true }).click()
    await expect(dialog).toHaveCount(0)
    expect(deletes).toBe(2)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
}

for (const width of [375, 1_280]) {
  test(`debug selection uses the header on desktop and More on mobile at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await fakeApi(page, true, true)
    await page.route('**/api/debug?*', (route) =>
      route.fulfill({
        json: {
          runtime: 'fixture',
          executable: '/fixture/appleseum',
          arguments: [],
          uptimeSeconds: 60,
          heapBytes: 1_234,
          goroutines: 5,
          total: 3,
          next: false,
          previous: false,
          items: [true, false, true].map((hasResponse, i) => ({
            id: i + 1,
            at: '2026-11-10T12:00:00Z',
            source: 'API',
            method: 'GET',
            target: '/fixture/' + i,
            status: 200,
            durationMs: 10,
            detail: '',
            hasResponse,
            responseBytes: 100,
            responseTruncated: false,
          })),
        },
      }),
    )
    await page.route('**/api/debug/selection?*', (route) =>
      route.fulfill({ json: { ids: [1, 3] } }),
    )
    await page.goto('/debug')
    const table = page.locator('.debug-table')
    await expect(table.locator('tbody tr')).toHaveCount(3)
    if (width > 600) {
      await expect(page.getByRole('button', { name: 'Ещё', exact: true })).toHaveCount(0)
      await table.locator('thead').getByRole('checkbox').click()
    } else {
      await page.getByRole('button', { name: 'Ещё', exact: true }).click()
      await page
        .getByRole('dialog')
        .getByRole('button', { name: 'Отметить все', exact: true })
        .click()
    }
    await expect(table.locator('tbody [aria-checked="true"]')).toHaveCount(2)
    if (width > 600) await table.locator('thead').getByRole('checkbox').click()
    else {
      await page.getByRole('button', { name: 'Ещё', exact: true }).click()
      await page.getByRole('dialog').getByRole('button', { name: 'Снять все', exact: true }).click()
    }
    await expect(table.locator('tbody [aria-checked="true"]')).toHaveCount(0)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
}
