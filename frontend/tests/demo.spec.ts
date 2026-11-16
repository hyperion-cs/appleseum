import type { Page } from '@playwright/test'
import { test, expect } from './fixtures.ts'
import { createDemo } from '../src/api/demo.ts'

const productionKeys = {
  'appleseum.sessionKey': 'existing-session',
  'appleseum.pageSize.catalog': '20',
}

async function isolated(page: Page) {
  const requests: string[] = []
  await page.route(
    (url) => url.pathname.startsWith('/api/'),
    async (route) => {
      requests.push(route.request().method() + ' ' + new URL(route.request().url()).pathname)
      await route.abort()
    },
  )
  await page.addInitScript((keys) => {
    for (const [key, value] of Object.entries(keys))
      if (localStorage.getItem(key) === null) localStorage.setItem(key, value)
  }, productionKeys)
  return requests
}

async function expectProductionUntouched(page: Page) {
  expect(
    await page.evaluate(() =>
      Object.fromEntries(
        Object.entries(localStorage).filter(([key]) => !key.startsWith('appleseum.demo.')),
      ),
    ),
  ).toEqual(productionKeys)
}

for (const mobile of [false, true]) {
  test(`prepared screens and isolated preferences work (${mobile ? 'mobile' : 'desktop'})`, async ({
    page,
  }) => {
    await page.setViewportSize(mobile ? { width: 390, height: 844 } : { width: 1_365, height: 960 })
    const requests = await isolated(page)
    await page.goto('/?demo')
    await expect(page.locator('tbody tr').first()).toBeVisible()
    expect(await page.locator('tbody tr').count()).toBeLessThanOrEqual(10)
    await expect(page).toHaveURL(/\/available\?demo(?:$|&)/)
    await expect(page.locator('body')).not.toContainText(/demo|демо/i)
    await expect
      .poll(() =>
        page
          .locator('.app-icon img')
          .first()
          .evaluate((img: HTMLImageElement) => img.naturalWidth),
      )
      .toBeGreaterThan(0)
    await page.getByRole('searchbox', { name: 'Search apps' }).fill('Atlas')
    await expect(page.locator('tbody tr')).toHaveCount(1)
    await expect(page.locator('tbody')).toContainText('Atlas Maps')
    await page.getByRole('link', { name: 'Downloads', exact: true }).click()
    await expect(page.locator('tbody tr').first()).toBeVisible()
    expect(await page.locator('tbody tr').count()).toBeLessThanOrEqual(8)
    await expect(page.locator('.download-progress')).toHaveCount(0)
    await page.getByRole('link', { name: 'Profile', exact: true }).click()
    await expect(page.locator('.profile-details')).toContainText('Alex Morgan')
    await page.getByRole('link', { name: 'Settings', exact: true }).click()
    await expect(page.locator('main')).toContainText('MacBook Pro')
    await page.getByRole('button', { name: 'Light theme', exact: true }).click()
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
    await page.getByRole('button', { name: 'Русский', exact: true }).click()
    await expect(page.locator('html')).toHaveAttribute('lang', 'ru')
    await page.getByRole('link', { name: 'Профиль', exact: true }).click()
    await expect(page.locator('.profile-details')).toContainText('Alex Morgan')
    await expectProductionUntouched(page)
    await expect
      .poll(() =>
        page.evaluate(
          () => JSON.parse(localStorage.getItem('appleseum.demo.preferences') ?? '{}').language,
        ),
      )
      .toBe('ru')
    await page.reload()
    await expect(page.locator('html')).toHaveAttribute('lang', 'ru')
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
    await expect(page.locator('.profile-details')).toContainText('Alex Morgan')
    await expectProductionUntouched(page)
    expect(requests).toEqual([])
  })
}

for (const width of [390, 1_365]) {
  test(`action messages use the shared dialog in both languages and themes at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    const requests = await isolated(page)
    await page.goto('/downloads?demo')
    const selected = page.getByRole('checkbox', { name: 'Atlas Maps', exact: true })
    await selected.click()
    await page.getByRole('button', { name: 'Start hosting', exact: true }).click()
    await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).click()
    await page.getByRole('button', { name: 'Files', exact: true }).click()
    for (const russian of [false, true]) {
      if (russian) {
        await page.getByRole('button', { name: 'Light theme', exact: true }).click()
        await page.getByRole('button', { name: 'Русский', exact: true }).click()
      }
      if (!russian) await selected.click()
      await page
        .getByRole('button', { name: russian ? 'Раздать' : 'Start hosting', exact: true })
        .click()
      const dialog = page.getByRole('dialog', {
        name: russian ? 'Раздача уже запущена' : 'Hosting is already active',
        exact: true,
      })
      await expect(dialog.getByRole('alert')).toContainText(
        russian ? 'уже запущена' : 'already being hosted',
      )
      await expect(page.locator('.notice')).toHaveCount(0)
      const box = (await dialog.boundingBox())!
      expect(box.x).toBeGreaterThanOrEqual(0)
      expect(box.x + box.width).toBeLessThanOrEqual(width)
      await dialog
        .getByRole('button', { name: russian ? 'Понятно' : 'Got it', exact: true })
        .click()
      await expect(dialog).toHaveCount(0)
      await expect(selected).toHaveAttribute('aria-checked', 'true')
    }
    expect(requests).toEqual([])
    await expectProductionUntouched(page)
  })
}

for (const width of [375, 1_280]) {
  test(`More menus share centered action buttons at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    const requests = await isolated(page)
    await page.goto('/available?demo')
    for (const russian of [false, true]) {
      if (russian) {
        await page.getByRole('button', { name: 'Light theme', exact: true }).click()
        await page.getByRole('button', { name: 'Русский', exact: true }).click()
      }
      for (const section of [
        russian ? 'Доступно' : 'Available',
        russian ? 'Загрузки' : 'Downloads',
        russian ? 'Профиль' : 'Profile',
      ]) {
        await page.getByRole('link', { name: section, exact: true }).click()
        await page.getByRole('button', { name: russian ? 'Ещё' : 'More', exact: true }).click()
        const menu = page.getByRole('dialog')
        await expect(menu.locator('.menu-button:visible').first()).toBeVisible()
        const errors = await menu.locator('.menu-button:visible').evaluateAll((buttons) =>
          buttons.flatMap((button) => {
            const rect = button.getBoundingClientRect(),
              label = button.querySelector('.menu-button-label')!.getBoundingClientRect(),
              icon = button.querySelector('svg')?.getBoundingClientRect(),
              left = icon?.left ?? label.left
            const center = Math.abs((left + label.right - rect.left - rect.right) / 2)
            const aligned =
              !icon || Math.abs((icon.top + icon.bottom - label.top - label.bottom) / 2) < 1
            const gap = !icon || Math.abs(label.left - icon.right - 8) < 1
            return center < 1 && aligned && gap && rect.height >= 44 ? [] : [button.textContent]
          }),
        )
        expect(errors).toEqual([])
        await menu.getByRole('button', { name: russian ? 'Закрыть' : 'Close', exact: true }).click()
      }
    }
    await expectProductionUntouched(page)
    expect(requests).toEqual([])
  })
}

for (const width of [375, 1_280]) {
  test(`downloads and shares use one visible table and retain their section at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    const requests = await isolated(page)
    await page.goto('/downloads?demo')
    const picker = page.locator('.library-sections')
    await expect(picker).toHaveCount(0)
    for (const russian of [false, true]) {
      if (russian) {
        await page.getByRole('button', { name: 'Light theme', exact: true }).click()
        await page.getByRole('button', { name: 'Русский', exact: true }).click()
      }
      const downloads = russian ? 'Загрузки' : 'Downloads',
        files = russian ? 'Файлы' : 'Files',
        shares = russian ? 'Раздачи' : 'Hosting',
        profile = russian ? 'Профиль' : 'Profile',
        settings = russian ? 'Настройки' : 'Settings',
        close = russian ? 'Закрыть' : 'Close',
        stop = russian ? 'Остановить' : 'Stop',
        start = russian ? 'Раздать' : 'Start hosting',
        search = page.getByRole('searchbox', {
          name: russian ? 'Поиск приложений' : 'Search apps',
          exact: true,
        })
      for (const [index, name] of ['Atlas Maps', 'Aurora'].entries()) {
        await search.fill(name)
        await page.locator('.downloads-table').getByRole('checkbox', { name, exact: true }).click()
        await page.getByRole('button', { name: start, exact: true }).click()
        await expect(page.getByRole('dialog')).toBeVisible()
        await page.getByRole('dialog').getByRole('button', { name: close, exact: true }).click()
        await expect(
          picker.getByRole('button', { name: new RegExp('^' + shares) }),
        ).toHaveAttribute('aria-pressed', 'true')
        await expect(picker.getByRole('button', { name: new RegExp('^' + shares) })).toContainText(
          russian ? `${index + 1} шт.` : `${index + 1} ${index ? 'items' : 'item'}`,
        )
        await expect(picker.locator('svg')).toHaveCount(2)
        await expect(picker.getByRole('button').first()).toHaveCSS(
          'font-size',
          await page
            .locator('.tabs a')
            .first()
            .evaluate((element) => getComputedStyle(element).fontSize),
        )
        await expect(page.locator('.downloads-table')).toBeHidden()
        await expect(page.locator('.shares-table').getByText(name, { exact: true })).toBeVisible()
        await expect(page.locator('.table-wrap:visible')).toHaveCount(1)
        await picker.getByRole('button', { name: files, exact: true }).click()
        await expect(search).toHaveValue(name)
      }
      await picker.getByRole('button', { name: new RegExp('^' + shares) }).click()
      const hosted = page.getByRole('region', { name: shares, exact: true })
      const shareSearch = hosted.getByRole('searchbox')
      await shareSearch.fill('Aurora')
      await hosted.getByRole('checkbox', { name: 'Aurora', exact: true }).click()
      await picker.getByRole('button', { name: files, exact: true }).click()
      await picker.getByRole('button', { name: new RegExp('^' + shares) }).click()
      await expect(shareSearch).toHaveValue('Aurora')
      await expect(hosted.getByRole('checkbox', { name: 'Aurora', exact: true })).toHaveAttribute(
        'aria-checked',
        'true',
      )
      for (const other of [profile, settings]) {
        await page.getByRole('link', { name: other, exact: true }).click()
        if (other === settings) await expect(page.locator('.device-form')).toBeVisible()
        await page.getByRole('link', { name: downloads, exact: true }).click()
        await expect(
          picker.getByRole('button', { name: new RegExp('^' + shares) }),
        ).toHaveAttribute('aria-pressed', 'true')
        await expect(page.locator('.downloads-table')).toBeHidden()
      }
      for (const name of ['Atlas Maps', 'Aurora'])
        await hosted.getByRole('checkbox', { name, exact: true }).click()
      await hosted.getByRole('button', { name: stop, exact: true }).click()
      await page.getByRole('dialog').getByRole('button', { name: stop, exact: true }).click()
      await expect(picker).toHaveCount(0)
      await expect(page.locator('.downloads-table')).toBeVisible()
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      )
    }
    expect(requests).toEqual([])
    await expectProductionUntouched(page)
  })
}

test('a new download opens its progress even when Hosting was selected', async ({ page }) => {
  const requests = await isolated(page)
  await page.goto('/downloads?demo')
  await page.getByRole('checkbox', { name: 'Atlas Maps', exact: true }).click()
  await page.getByRole('button', { name: 'Start hosting', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).click()
  await expect(
    page.locator('.library-sections').getByRole('button', { name: /^Hosting/ }),
  ).toHaveAttribute('aria-pressed', 'true')
  await page.getByRole('link', { name: 'Available', exact: true }).click()
  await page.getByRole('searchbox', { name: 'Search apps', exact: true }).fill('Wave')
  await page.getByRole('checkbox', { name: 'Wave', exact: true }).click()
  await page.getByRole('button', { name: 'Download', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Download', exact: true }).click()
  await expect(page).toHaveURL(/\/downloads\?demo/)
  await expect(
    page.locator('.library-sections').getByRole('button', { name: 'Files', exact: true }),
  ).toHaveAttribute('aria-pressed', 'true')
  await expect(page.locator('.downloads-table').getByText('Wave', { exact: true })).toBeVisible()
  await expect(page.locator('.shares-table')).toBeHidden()
  expect(requests).toEqual([])
})

test('downloads show 50 Mbit/s, finish and can be shared; reloading restores eight ready apps', async ({
  page,
}) => {
  const requests = await isolated(page)
  await page.clock.install()
  await page.goto('/?demo')
  await page.getByRole('searchbox', { name: 'Search apps' }).fill('Wave')
  await expect(page.locator('tbody tr')).toHaveCount(1)
  await page.getByRole('checkbox', { name: 'Wave', exact: true }).click()
  await page.getByRole('button', { name: 'Download', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toBeVisible()
  await dialog.getByRole('button', { name: 'Download', exact: true }).click()
  await expect(page).toHaveURL(/\/downloads\?demo/)
  const wave = page.locator('tbody tr').filter({ hasText: 'Wave' })
  await expect(wave).toContainText('50 Mb/s')
  await expect(wave.getByRole('progressbar')).toBeVisible()
  await page.clock.runFor(35_000)
  await expect(wave).toContainText('Ready to host')
  await expect(wave.getByRole('progressbar')).toHaveCount(0)
  await wave.getByRole('checkbox', { name: 'Wave', exact: true }).click()
  await page.getByRole('button', { name: 'Start hosting', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await expect(page.getByRole('dialog').getByRole('button', { name: /Copy/ })).toBeVisible()
  await expect(page.locator('body')).not.toContainText(/demo|демо/i)
  await expectProductionUntouched(page)
  await page.reload()
  await expect(page.locator('tbody tr')).toHaveCount(8)
  await expect(page.locator('tbody')).not.toContainText('Wave')
  await expect(page.locator('.download-progress')).toHaveCount(0)
  expect(requests).toEqual([])
})

test('mock uploads add files without XHR or API requests', async ({ page }) => {
  const requests = await isolated(page)
  await page.addInitScript(() => localStorage.setItem('appleseum.demo.pageSize.library', '10'))
  await page.goto('/downloads?demo')
  await expect(page.locator('tbody tr')).toHaveCount(8)
  await page.locator('input[type=file]').setInputFiles({
    name: 'Travel Notes.ipa',
    mimeType: 'application/octet-stream',
    buffer: Buffer.from('unused'),
  })
  await expect(page.locator('tbody tr').filter({ hasText: 'Travel Notes' })).toContainText(
    'Ready to host',
  )
  await expect(page.locator('tbody tr')).toHaveCount(9)
  await expectProductionUntouched(page)
  expect(requests).toEqual([])
})

test('normal mode never loads the mock; module prefetch follows hover and focus without API calls', async ({
  page,
}) => {
  const scripts: string[] = [],
    apiCalls: string[] = []
  const { request: mock } = createDemo()
  page.on('request', (request) => {
    if (request.resourceType() === 'script') scripts.push(new URL(request.url()).pathname)
  })
  await page.route(
    (url) => url.pathname.startsWith('/api/'),
    async (route) => {
      const request = route.request(),
        url = new URL(request.url())
      apiCalls.push(url.pathname)
      const response = mock(
        url.pathname.slice(4) + url.search,
        request.method(),
        request.postData() ? request.postDataJSON() : undefined,
      )
      await route.fulfill({
        status: response.status,
        headers: Object.fromEntries(response.headers),
        body: await response.text(),
      })
    },
  )
  await page.addInitScript(() => localStorage.setItem('appleseum.pageSize.catalog', '10'))
  await page.goto('/available')
  await expect(page.locator('tbody tr')).toHaveCount(10)
  expect(scripts.some((path) => /\/demo[-.]/.test(path))).toBe(false)
  expect(scripts.some((path) => path.includes('ProfileView'))).toBe(false)
  await page.getByRole('link', { name: 'Profile', exact: true }).hover()
  await expect.poll(() => scripts.some((path) => path.includes('ProfileView'))).toBe(true)
  expect(apiCalls.filter((path) => path === '/api/profile')).toHaveLength(0)
  await page.getByRole('link', { name: 'Profile', exact: true }).click()
  await expect(page.locator('.profile-details')).toContainText('Alex Morgan')
  await page.getByRole('link', { name: 'Settings', exact: true }).focus()
  await expect.poll(() => scripts.some((path) => path.includes('SettingsView'))).toBe(true)
  expect(apiCalls.filter((path) => path === '/api/admin/device')).toHaveLength(0)
  expect(scripts.some((path) => /\/demo[-.]/.test(path))).toBe(false)
})

for (const width of [390, 1_365]) {
  test(`hosted file deletion replaces confirmation in both languages and themes at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    const requests = await isolated(page)
    await page.goto('/downloads?demo')
    const selected = page.getByRole('checkbox', { name: 'Atlas Maps', exact: true })
    await selected.click()
    await page.getByRole('button', { name: 'Start hosting', exact: true }).click()
    await page.getByRole('dialog').getByRole('button', { name: 'Close', exact: true }).click()
    await page.waitForFunction(() => !history.state?.modal)
    await page.getByRole('button', { name: 'Files', exact: true }).click()
    await selected.click()
    for (const russian of [false, true]) {
      if (russian) await page.getByRole('button', { name: 'Русский', exact: true }).click()
      for (const light of [false, true]) {
        if ((await page.locator('html').getAttribute('data-theme')) !== (light ? 'light' : 'dark'))
          await page
            .getByRole('button', {
              name: russian
                ? light
                  ? 'Светлая тема'
                  : 'Тёмная тема'
                : light
                  ? 'Light theme'
                  : 'Dark theme',
              exact: true,
            })
            .click()
        const remove = russian ? 'Удалить' : 'Delete'
        await page.getByRole('button', { name: remove, exact: true }).click()
        const dialog = page.getByRole('dialog')
        await expect(
          dialog.getByRole('button', { name: russian ? 'Отмена' : 'Cancel', exact: true }),
        ).toBeVisible()
        const entry = await page.evaluate(() => history.state.modal)
        await dialog.getByRole('button', { name: remove, exact: true }).click()
        await expect(dialog).toHaveAccessibleName(
          russian ? 'Действие не выполнено' : 'Action could not be completed',
        )
        await expect(page.getByRole('dialog')).toHaveCount(1)
        await expect(dialog.getByRole('alert')).toHaveText(
          russian
            ? 'Некоторые выбранные файлы используются в раздачах. Сначала остановите эти раздачи.'
            : 'Some selected files are being hosted. Stop those hosting sessions first.',
        )
        await expect(dialog.getByRole('button')).toHaveCount(2)
        await expect(dialog).not.toContainText(
          russian ? 'Файлы будут удалены' : 'Files will be deleted',
        )
        expect(await page.evaluate(() => history.state.modal)).toBe(entry)
        const box = (await dialog.boundingBox())!
        expect(box.x).toBeGreaterThanOrEqual(0)
        expect(box.x + box.width).toBeLessThanOrEqual(width)
        if (light) await page.goBack()
        else
          await dialog
            .getByRole('button', { name: russian ? 'Понятно' : 'Got it', exact: true })
            .click()
        await page.waitForFunction(() => !history.state?.modal)
        await expect(page.getByRole('dialog')).toHaveCount(0)
        await expect(selected).toHaveAttribute('aria-checked', 'true')
        await expect(
          page.getByRole('button', { name: russian ? /^Раздачи/ : /^Hosting/ }),
        ).toContainText('1')
        await expect(page.locator('.notice')).toHaveCount(0)
      }
    }
    expect(requests).toEqual([])
    await expectProductionUntouched(page)
  })
}
