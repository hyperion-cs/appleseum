import { devices } from '@playwright/test'
import { test, expect, fakeApi } from './fixtures.ts'

for (const width of [375, 1_280]) {
  test(`hosting clears its selection on success and preserves rejected duplicates at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await page.clock.install()
    const control = await fakeApi(page)
    const active = {
      id: 'test-share',
      url: 'itms-services://localhost/?action=download-manifest&url=https%3A%2F%2F192.168.50.10%3A8443%2Fmanifests%2Ftest.plist',
      count: 1,
      icon: '',
      name: 'Test app',
    }
    let requests = 0
    await page.route('**/api/shares', async (route) => {
      requests++
      expect(route.request().postDataJSON().ids).toEqual(['01900000-0000-7000-8000-000000000001'])
      if (requests === 1) {
        control.setShare({ ...active, icon: '' })
        await route.fulfill({ status: 201, json: active })
      } else {
        await route.fulfill({ status: 409, json: { code: 'sharing_duplicate' } })
      }
    })
    await page.goto('/')
    await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
    await page.getByRole('link', { name: 'Загрузки', exact: true }).click()
    const selected = page
      .locator('.downloads-table')
      .getByRole('checkbox', { name: 'Example App', exact: true, includeHidden: true })
    const start = page.getByRole('button', { name: 'Раздать', exact: true })
    await selected.click()
    await start.click()
    const dialog = page.getByRole('dialog', { name: 'Приложения готовы к установке' })
    await expect(dialog).toBeVisible()
    await expect(selected).toHaveAttribute('aria-checked', 'false')
    await dialog.getByRole('button', { name: 'Закрыть', exact: true }).click()
    await expect(
      page
        .getByRole('group', { name: 'Раздел загрузок', exact: true })
        .getByRole('button', { name: /^Раздачи/ }),
    ).toHaveAttribute('aria-pressed', 'true')
    await page.getByRole('button', { name: 'Файлы', exact: true }).click()
    await expect(start).toBeDisabled()
    await page.getByRole('link', { name: 'Доступно', exact: true }).click()
    await expect(page.getByRole('checkbox', { name: 'Example App', exact: true })).toHaveAttribute(
      'aria-checked',
      'true',
    )
    await page.getByRole('link', { name: 'Загрузки', exact: true }).click()
    await expect(selected).toHaveAttribute('aria-checked', 'false')
    await selected.click()
    await start.click()
    await expect(page.getByRole('alert')).toHaveText('Раздача с таким набором файлов уже запущена.')
    await expect(selected).toHaveAttribute('aria-checked', 'true')
    await expect(dialog).toHaveCount(0)
    const failure = page.getByRole('dialog', { name: 'Раздача уже запущена', exact: true })
    await expect(failure).toBeVisible()
    await page.clock.runFor(5_200)
    await expect(failure).toBeVisible()
    await expect(page.locator('.notice')).toHaveCount(0)
    await failure.getByRole('button', { name: 'Понятно', exact: true }).click()
    await page.clock.runFor(5_200)
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(selected).toHaveAttribute('aria-checked', 'true')
    await expect(
      page
        .getByRole('group', { name: 'Раздел загрузок', exact: true })
        .getByRole('button', { name: /^Раздачи/ }),
    ).toContainText('1')
    await page.getByRole('button', { name: 'English', exact: true }).click()
    await page.getByRole('button', { name: 'Start hosting', exact: true }).click()
    const english = page.getByRole('dialog', { name: 'Hosting is already active', exact: true })
    await expect(english.getByRole('alert')).toHaveText(
      'The same set of files is already being hosted.',
    )
    await english.getByRole('button', { name: 'Got it', exact: true }).click()
    await expect(selected).toHaveAttribute('aria-checked', 'true')
    expect(requests).toBe(3)
  })
}

test('install link opens in a new tab without displaying its raw URL', async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'userAgent', {
      value:
        'Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Mobile/15E148 Safari/604.1',
    })
  })
  await fakeApi(page)
  await page.goto('/')
  await page.getByRole('link', { name: 'Загрузки', exact: true }).click()
  await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
  await page.getByRole('button', { name: /^Раздать/ }).click()
  const dialog = page.getByRole('dialog', { name: 'Приложения готовы к установке' })
  await expect(dialog).toBeVisible()
  const open = dialog.getByRole('link', { name: 'Установить', exact: true })
  await expect(open).toHaveAttribute('target', '_blank')
  await expect(open).toHaveAttribute('href', /^itms-services:\/\//)
  await expect(dialog.getByRole('textbox')).toHaveCount(0)
  await expect(
    dialog.getByRole('button', { name: 'Скопировать ссылку', exact: true }),
  ).toBeVisible()
  await expect(open).toHaveAttribute('rel', 'noopener noreferrer')
})

test('copy install link over HTTP without displaying a link field', async ({ page }) => {
  await fakeApi(page)
  await page.goto('/downloads')
  await page.evaluate(() => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
    document.execCommand = (command) => {
      const input = document.activeElement as HTMLTextAreaElement
      if (command !== 'copy' || input.tagName !== 'TEXTAREA') return false
      document.documentElement.dataset.copiedInstallLink = input.value
      return true
    }
  })
  await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
  await page.getByRole('button', { name: /^Раздать/ }).click()
  const dialog = page.getByRole('dialog', { name: 'Приложения готовы к установке' })
  await expect(dialog.getByRole('link', { name: 'Установить', exact: true })).toHaveCount(0)
  const expected =
    'itms-services://localhost/?action=download-manifest&url=https%3A%2F%2F192.168.50.10%3A8443%2Fmanifests%2Ftest.plist'
  await dialog.getByRole('button', { name: 'Скопировать ссылку', exact: true }).click()
  await expect(dialog.getByRole('button', { name: 'Скопировано', exact: true })).toBeVisible()
  await expect(page.locator('html')).toHaveAttribute('data-copied-install-link', expected!)
  await expect(page.locator('textarea')).toHaveCount(0)
})

for (const width of [375, 1_280]) {
  test(`share DNS verification failure keeps selection and explains two-device recovery at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    const control = await fakeApi(page)
    control.setState({ mode: { ish: true } })
    await page.route('**/api/shares', (route) =>
      route.fulfill({ status: 502, json: { code: 'delivery_verification_failed' } }),
    )
    await page.goto('/downloads')
    const selected = page.getByRole('checkbox', { name: 'Example App', exact: true })
    await selected.click()
    await page.getByRole('button', { name: 'Раздать', exact: true }).click()
    const error = page.getByRole('alert')
    await expect(error).toHaveText(
      'Не удалось подтвердить подлинность элемента доставки. Используйте два устройства: одно для раздачи, другое для установки.',
    )
    await expect(selected).toHaveAttribute('aria-checked', 'true')
    const failure = page.getByRole('dialog', { name: 'Действие не выполнено', exact: true })
    await expect(failure).toBeVisible()
    await expect(page.locator('.notice')).toHaveCount(0)
    await expect(page.locator('.shares-section')).toHaveCount(0)
    await failure.getByRole('button', { name: 'Понятно', exact: true }).click()
    await page.getByRole('button', { name: 'English', exact: true }).click()
    await page.getByRole('button', { name: 'Start hosting', exact: true }).click()
    await expect(error).toHaveText(
      'A delivery component could not be authenticated. Use two devices: one to host apps and another to install them.',
    )
    await expect(selected).toHaveAttribute('aria-checked', 'true')
  })
}

for (const device of ['iPhone 13', 'iPad Mini']) {
  test.describe(`explicit installation on ${device}`, () => {
    const profile = devices[device]!
    test.use({
      viewport: profile.viewport,
      userAgent: profile.userAgent,
      deviceScaleFactor: profile.deviceScaleFactor,
      isMobile: profile.isMobile,
      hasTouch: profile.hasTouch,
    })
    for (const delay of [0, 500]) {
      test(`starting a share never activates installation with ${delay}ms response`, async ({
        page,
      }) => {
        await page.addInitScript(() => {
          document.addEventListener(
            'click',
            (event) => {
              const link = event.target instanceof Element ? event.target.closest('a[href]') : null
              if (!link?.getAttribute('href')?.startsWith('itms-services:')) return
              // Observe the handoff without invoking a system installer during the test.
              event.preventDefault()
              const root = document.documentElement
              root.dataset.installActivations = String(
                Number(root.dataset.installActivations || '0') + 1,
              )
            },
            true,
          )
          Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
          document.execCommand = (command) => command === 'copy'
        })
        let popups = 0
        page.on('popup', () => popups++)
        await fakeApi(page)
        await page.route('**/api/shares', async (route) => {
          if (delay) await new Promise((resolve) => setTimeout(resolve, delay))
          await route.fallback()
        })
        await page.goto('/downloads')
        await page.getByRole('checkbox', { name: 'Example App', exact: true }).tap()
        await page.getByRole('button', { name: 'Раздать', exact: true }).tap()
        const dialog = page.getByRole('dialog', { name: 'Приложения готовы к установке' })
        await expect(dialog).toBeVisible()
        const install = dialog.getByRole('link', { name: 'Установить', exact: true })
        await expect(install).toBeVisible()
        await expect(install).not.toBeFocused()
        await expect(dialog).toBeFocused()
        // Give a delayed touch compatibility click time to reach the newly opened dialog.
        await page.waitForTimeout(400)
        await expect(dialog.locator('.install-feedback')).toHaveCount(0)
        await expect(page.locator('html')).not.toHaveAttribute('data-install-activations')
        expect(popups).toBe(0)
        await dialog.getByRole('button', { name: 'Скопировать ссылку', exact: true }).tap()
        await expect(dialog.getByRole('button', { name: 'Скопировано', exact: true })).toBeVisible()
        await expect(page.locator('html')).not.toHaveAttribute('data-install-activations')
        await expect(dialog.locator('.install-feedback')).toHaveCount(0)
        await install.tap()
        await expect(page.locator('html')).toHaveAttribute('data-install-activations', '1')
        await expect(dialog.locator('.install-feedback')).toBeVisible()
        await dialog.getByRole('button', { name: 'Закрыть', exact: true }).tap()
        await expect(dialog).toHaveCount(0)
        await page.getByRole('button', { name: 'Посмотреть раздачу — Test app', exact: true }).tap()
        await expect(dialog).toBeVisible()
        await expect(dialog.locator('.install-feedback')).toHaveCount(0)
        await expect(page.locator('html')).toHaveAttribute('data-install-activations', '1')
      })
    }
  })
}

test('hidden shares do not poll and opening the section fetches current items', async ({
  page,
}) => {
  await page.clock.install()
  const control = await fakeApi(page)
  control.setShare({ id: 'test-share', url: 'about:blank', count: 1, icon: '' })
  let reads = 0
  await page.route('**/api/shares?*', (route) => {
    reads++
    return route.fallback()
  })
  await page.goto('/downloads')
  const picker = page.getByRole('group', { name: 'Раздел загрузок', exact: true })
  await expect(picker).toBeVisible()
  await expect(picker.getByRole('button', { name: 'Файлы', exact: true })).toHaveAttribute(
    'aria-pressed',
    'true',
  )
  await page.clock.runFor(7_800)
  expect(reads).toBe(0)
  await picker.getByRole('button', { name: /^Раздачи/ }).click()
  await expect(page.locator('.shares-table').getByText('Test app', { exact: true })).toBeVisible()
  expect(reads).toBeGreaterThan(0)
  await picker.getByRole('button', { name: 'Файлы', exact: true }).click()
  const previous = reads
  await page.clock.runFor(7_800)
  expect(reads).toBe(previous)
  await expect(page.locator('.downloads-table')).toBeVisible()
})

for (const width of [375, 1_440]) {
  test(`shares have independent controls, cursor pages and bulk stop at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 1_000 })
    const pageSize = width > 600 ? 5 : 3
    await page.addInitScript(
      (size) => localStorage.setItem('appleseum.pageSize.shares', String(size)),
      pageSize,
    )
    const control = await fakeApi(page)
    let shares = Array.from({ length: 13 }, (_, i) => ({
      id: `share-${i + 1}`,
      name: `Shared app ${String(i + 1).padStart(2, '0')}`,
      icon: '',
      count: i + 1,
      owner: `user${i}@example.test`,
      createdAt: '2026-11-10T12:00:00Z',
      url: 'about:blank#share-' + (i + 1),
    }))
    control.setState({ shareCount: shares.length, mode: { service: true } })
    const requests: string[] = [],
      stopped: string[][] = []
    await page.route('**/api/shares?**', async (route) => {
      const query = new URL(route.request().url()).searchParams
      expect(query.has('offset')).toBe(false)
      requests.push(query.toString())
      const found = shares.filter((share) =>
        share.name.toLowerCase().includes((query.get('q') ?? '').toLowerCase()),
      )
      const limit = Number(query.get('limit') ?? 5),
        cursor = query.get('cursor') ?? ''
      const boundary = found.findIndex((share) => share.id === cursor)
      const start = cursor
        ? query.get('move') === 'prev'
          ? Math.max(0, boundary - limit)
          : boundary + 1
        : 0
      await route.fulfill({
        json: {
          items: found.slice(start, start + limit).map((row) => ({ ...row, cursor: row.id })),
          total: found.length,
          next: start + limit < found.length,
          previous: start > 0,
        },
      })
    })
    await page.route('**/api/shares/selection', (route) =>
      route.fulfill({
        json: {
          ids: shares
            .filter((share) =>
              share.name
                .toLowerCase()
                .includes((route.request().postDataJSON().query ?? '').toLowerCase()),
            )
            .map((share) => share.id),
        },
      }),
    )
    await page.route('**/api/shares/*', async (route) => {
      const path = new URL(route.request().url()).pathname
      if (path.endsWith('/stop')) {
        const ids = route.request().postDataJSON().ids as string[]
        stopped.push(ids)
        shares = shares.filter((share) => !ids.includes(share.id))
        control.setState({ shareCount: shares.length })
        return route.fulfill({ status: 204 })
      }
      const share = shares.find((share) => path.endsWith('/' + share.id))
      return share ? route.fulfill({ json: share }) : route.fallback()
    })
    await page.goto('/downloads')
    await page.getByRole('button', { name: /^Раздачи/ }).click()
    const region = page.getByRole('region', { name: 'Раздачи', exact: true })
    const table = region.getByRole('table', { name: 'Раздачи', exact: true })
    await expect(table.locator('tbody tr')).toHaveCount(pageSize)
    await expect(page.locator('.downloads-table')).toBeHidden()
    await expect(page.locator('.table-wrap:visible')).toHaveCount(1)
    if (width > 600) {
      expect(
        await table.locator('thead').evaluate((el) => el.getBoundingClientRect().height),
      ).toBeLessThanOrEqual(44)
    }
    await expect(table.locator('thead')).toContainText('Начало раздачи')
    await expect(table.locator('thead')).not.toContainText('Файлов')
    await expect(region.getByRole('button', { name: 'Ещё', exact: true })).toHaveCount(
      width > 600 ? 0 : 1,
    )
    const firstRow = table.locator('tbody tr').first()
    const firstCheck = firstRow.getByRole('checkbox')
    await firstRow.getByText('Shared app 01', { exact: true }).click()
    await expect(firstCheck).toHaveAttribute('aria-checked', 'true')
    await firstCheck.click()
    await expect(firstCheck).toHaveAttribute('aria-checked', 'false')
    if (width > 600) {
      await table.locator('thead').getByRole('checkbox').click()
      await expect(table.locator('tbody [aria-checked="true"]')).toHaveCount(pageSize)
      await table.locator('thead').getByRole('checkbox').click()
      await expect(table.locator('tbody [aria-checked="true"]')).toHaveCount(0)
    }
    await table
      .getByRole('button', { name: 'Посмотреть раздачу — Shared app 01', exact: true })
      .click()
    const view = page.getByRole('dialog', { name: 'Приложения готовы к установке', exact: true })
    await expect(view).toBeVisible()
    await expect(firstCheck).toHaveAttribute('aria-checked', 'false')
    await view.getByRole('button', { name: 'Закрыть', exact: true }).click()
    await table.getByRole('checkbox', { name: 'Shared app 01', exact: true }).click()
    await region.getByRole('button', { name: 'Вперёд', exact: true }).click()
    await expect(
      table.getByText(`Shared app ${String(pageSize + 1).padStart(2, '0')}`, { exact: true }),
    ).toBeVisible()
    await table
      .getByRole('checkbox', {
        name: `Shared app ${String(pageSize + 1).padStart(2, '0')}`,
        exact: true,
      })
      .click()
    await region.getByRole('button', { name: 'Остановить', exact: true }).click()
    const confirm = page.getByRole('dialog', { name: 'Остановить выбранные раздачи?', exact: true })
    await confirm.getByRole('button', { name: 'Остановить', exact: true }).click()
    await expect.poll(() => stopped.length).toBe(1)
    expect(stopped[0].sort()).toEqual(['share-1', `share-${pageSize + 1}`].sort())
    expect(
      requests.some((query) => new URLSearchParams(query).get('cursor') === `share-${pageSize}`),
    ).toBe(true)
    const search = region.getByRole('searchbox', { name: 'Поиск раздач', exact: true })
    await search.fill('Shared app 13')
    await expect(table.locator('tbody tr')).toHaveCount(1)
    await expect(table.getByText('Shared app 13', { exact: true })).toBeVisible()
    await expect(search).toBeVisible()
    await expect(table.getByRole('checkbox', { name: 'Shared app 13', exact: true })).toBeVisible()
    if (width <= 600) {
      await region.getByRole('button', { name: 'Ещё', exact: true }).click()
      const menu = page.getByRole('dialog', { name: 'Ещё', exact: true })
      const options = menu.locator('.sort-options')
      await expect(options).toBeVisible()
      await expect(options.locator('.chosen')).toContainText('По убыванию')
      const name = options.getByRole('button').filter({ hasText: 'Приложение' })
      await name.click()
      await expect(menu).toBeVisible()
      await expect(name).toHaveClass(/\bchosen\b/)
      await expect(name).toContainText('По возрастанию')
      await name.click()
      await expect(name).toContainText('По убыванию')
      await expect(options.locator('svg')).toHaveCount(0)
      await menu.getByRole('button', { name: 'Закрыть', exact: true }).click()
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
}

for (const width of [375, 1_440]) {
  test(`a single share uses compact controls and follows count changes at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await page.clock.install()
    const control = await fakeApi(page)
    const first = {
      id: 'share-one',
      name: 'Shared app',
      icon: '',
      count: 2,
      owner: 'user@example.test',
      createdAt: '2026-11-10T12:00:00Z',
      url: 'about:blank#one',
    }
    const second = { ...first, id: 'share-two', name: 'Other share', url: 'about:blank#two' }
    let shares = [first]
    control.setState({ shareCount: shares.length, mode: { service: true } })
    const stopped: string[][] = []
    await page.route('**/api/shares?**', (route) => {
      const query = new URL(route.request().url()).searchParams
      const items = shares.filter((share) => share.name.includes(query.get('q') ?? ''))
      return route.fulfill({ json: { items, total: items.length, next: false, previous: false } })
    })
    await page.route('**/api/shares/*', (route) => {
      const path = new URL(route.request().url()).pathname
      if (path.endsWith('/stop')) {
        const ids = route.request().postDataJSON().ids as string[]
        stopped.push(ids)
        if (stopped.length === 1)
          return route.fulfill({ status: 503, json: { code: 'storage_failed' } })
        shares = shares.filter((share) => !ids.includes(share.id))
        control.setState({ shareCount: shares.length })
        return route.fulfill({ status: 204 })
      }
      const share = shares.find((share) => path.endsWith('/' + share.id))
      return share ? route.fulfill({ json: share }) : route.fallback()
    })
    await page.goto('/downloads')
    await page.getByRole('button', { name: /^Раздачи/ }).click()
    const region = page.getByRole('region', { name: 'Раздачи', exact: true })
    const table = region.getByRole('table')
    await expect(table.getByText(first.name, { exact: true })).toBeVisible()
    await expect(region.locator('.toolbar, thead, .pagination, [role=checkbox]')).toHaveCount(0)
    await expect(table.getByText('и ещё 1 шт.', { exact: true })).toBeVisible()
    await table.getByText(first.name, { exact: true }).click()
    await expect(table.locator('tr.selected')).toHaveCount(0)
    await region
      .getByRole('button', { name: 'Посмотреть раздачу — Shared app', exact: true })
      .click()
    await page.getByRole('dialog').getByRole('button', { name: 'Закрыть', exact: true }).click()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)

    shares = [first, second]
    control.setState({ shareCount: shares.length })
    await page.clock.runFor(2_600)
    const search = region.getByRole('searchbox', { name: 'Поиск раздач', exact: true })
    await expect(search).toBeVisible()
    await expect(table.locator('tbody tr')).toHaveCount(2)
    await search.fill('Other')
    await page.clock.runFor(300)
    await expect(table.locator('tbody tr')).toHaveCount(1)
    await expect(search).toBeVisible()
    await table.getByRole('checkbox', { name: second.name, exact: true }).click()
    shares = [first]
    control.setState({ shareCount: shares.length })
    await page.clock.runFor(2_900)
    await expect(table.getByText(first.name, { exact: true })).toBeVisible()
    await expect(region.locator('.toolbar, thead, .pagination, [role=checkbox]')).toHaveCount(0)

    const stop = region.getByRole('button', { name: 'Остановить — Shared app', exact: true })
    await stop.click()
    const confirm = page.getByRole('dialog', { name: 'Остановить раздачу?', exact: true })
    await expect(confirm).toBeVisible()
    await confirm.getByRole('button', { name: 'Отмена', exact: true }).click()
    expect(stopped).toEqual([])
    await stop.click()
    await confirm.getByRole('button', { name: 'Остановить', exact: true }).click()
    const failure = page.getByRole('dialog', { name: 'Действие не выполнено', exact: true })
    await expect(failure.getByRole('alert')).toHaveText('Не удалось сохранить данные на сервере.')
    await expect(table.getByText(first.name, { exact: true })).toBeVisible()
    await failure.getByRole('button', { name: 'Понятно', exact: true }).click()
    await page.waitForFunction(() => !history.state?.modal)
    await stop.click()
    await confirm.getByRole('button', { name: 'Остановить', exact: true }).click()
    await expect(region).toHaveCount(0)
    await expect(page.locator('.segmented-control')).toHaveCount(0)
    await expect(page.locator('.downloads-table')).toBeVisible()
    expect(stopped).toEqual([[first.id], [first.id]])
  })
}

test('local guests keep Hosting open when the share count changes and state is polled', async ({
  page,
}) => {
  await page.setViewportSize({ width: 375, height: 900 })
  await fakeApi(page, false)
  await page.clock.install()
  await page.goto('/downloads')
  await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
  await page.getByRole('button', { name: 'Раздать', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Закрыть', exact: true }).click()
  const hosting = page.locator('.library-sections').getByRole('button', { name: /^Раздачи/ })
  await expect(hosting).toHaveAttribute('aria-pressed', 'true')
  await page.clock.runFor(5_200)
  await expect(hosting).toHaveAttribute('aria-pressed', 'true')
  await expect(page.locator('.shares-table')).toBeVisible()
  await expect(page.locator('.downloads-table')).toBeHidden()
})
