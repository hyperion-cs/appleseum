import type { LibraryItem } from '../src/api/types'
import { test, expect, catalog, fakeApi } from './fixtures.ts'

test('sorting requests follow the selected column', async ({ page }) => {
  await page.setViewportSize({ width: 1_440, height: 900 })
  await fakeApi(page)
  await page.goto('/')
  await expect(page.getByText('Example App', { exact: true })).toBeVisible()
  const response = page.waitForRequest(
    (r) =>
      r.url().includes('/api/catalog?') &&
      r.url().includes('sort=name') &&
      r.url().includes('direction=desc'),
  )
  await page.getByRole('columnheader', { name: 'Приложение' }).getByRole('button').click()
  await response
  await expect(page.getByRole('columnheader', { name: 'Приложение' })).toHaveAttribute(
    'aria-sort',
    'descending',
  )
})

test('refresh stays disabled while catalog is loading', async ({ page }) => {
  const control = await fakeApi(page)
  control.setState({ catalog: { status: 'loading' } })
  await page.goto('/')
  await expect(page.getByRole('button', { name: /^Обновление/ })).toBeDisabled()
  await expect(page.getByText('Получаем список приложений', { exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: /^Обновление/ })).toHaveAttribute(
    'aria-busy',
    'true',
  )
})

test('catalog contacts Apple only when refresh is clicked', async ({ page }) => {
  const control = await fakeApi(page)
  control.setState({ catalog: { status: 'idle' } })
  let refreshes = 0
  await page.route('**/api/catalog?**', (route) =>
    route.fulfill({ json: { items: [], next: false, previous: false } }),
  )
  await page.route('**/api/catalog/refresh', async (route) => {
    refreshes++
    await route.fulfill({ status: 202 })
  })
  await page.goto('/')
  await expect(
    page.getByText('Обновите список, чтобы получить приложения аккаунта.', { exact: true }),
  ).toBeVisible()
  await page.getByRole('link', { name: 'Профиль', exact: true }).click()
  await page.getByRole('link', { name: 'Доступно', exact: true }).click()
  await expect(
    page.getByText('Обновите список, чтобы получить приложения аккаунта.', { exact: true }),
  ).toBeVisible()
  expect(refreshes).toBe(0)
  await page.locator('.empty').getByRole('button', { name: 'Обновить', exact: true }).click()
  await expect.poll(() => refreshes).toBe(1)
})

for (const accepted of [true, false]) {
  test(`download selection is cleared only on success: ${accepted}`, async ({ page }) => {
    await fakeApi(page)
    await page.route('**/api/downloads', async (route) => {
      expect(route.request().postDataJSON().selection).toEqual({
        ids: ['1001', '1002'],
        filter: null,
      })
      await route.fulfill(accepted ? { status: 202 } : { status: 409, json: { code: 'busy' } })
    })
    await page.goto('/')
    await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
    await page.getByRole('checkbox', { name: 'Delisted App', exact: true }).click()
    await page.getByRole('button', { name: /^Скачать/ }).click()
    await page
      .getByRole('dialog', { name: 'Скачать', exact: true })
      .getByRole('button', { name: 'Скачать', exact: true })
      .click()
    if (accepted) {
      await expect(page.getByRole('region', { name: 'Загрузки', exact: true })).toBeVisible()
      await page.getByRole('link', { name: 'Доступно', exact: true }).click()
    } else {
      await expect(page.getByRole('alert')).toBeVisible()
      await page
        .getByRole('dialog', { name: 'Скачать', exact: true })
        .getByRole('button', { name: 'Отмена', exact: true })
        .click()
    }
    for (const name of ['Example App', 'Delisted App']) {
      await expect(page.getByRole('checkbox', { name, exact: true })).toHaveAttribute(
        'aria-checked',
        String(!accepted),
      )
    }
    if (accepted) await expect(page.getByRole('button', { name: /^Скачать/ })).toBeDisabled()
  })
}

for (const width of [375, 1_280]) {
  test(`catalog identifies and prevents selecting downloaded apps in both languages at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await fakeApi(page, true, false, true)
    await page.goto('/')
    const downloaded = page.locator('tbody tr').filter({ hasText: 'Example App' })
    const other = page
      .getByRole('checkbox', { name: 'Delisted App', exact: true })
      .locator('xpath=ancestor::tr')
    for (const language of ['ru', 'en']) {
      if (language === 'en')
        await page.getByRole('button', { name: 'English', exact: true }).click()
      const badge = language === 'ru' ? 'Скачано' : 'Downloaded'
      const more = language === 'ru' ? 'Ещё' : 'More'
      const download = page.getByRole('button', {
        name: language === 'ru' ? 'Скачать' : 'Download',
        exact: true,
      })
      await expect(downloaded.getByText(badge, { exact: true })).toBeVisible()
      await expect(other.getByText(badge, { exact: true })).toHaveCount(0)
      await expect(downloaded.getByRole('checkbox')).toHaveCount(0)
      await downloaded.getByText('Example App', { exact: true }).click()
      await expect(downloaded).not.toHaveClass(/selected/)
      await expect(downloaded.getByRole('checkbox')).toHaveCount(0)
      await expect(download).toBeDisabled()
      if (width > 600) {
        await page.locator('thead').getByRole('checkbox').click()
      } else {
        await page.getByRole('button', { name: more, exact: true }).click()
        await page
          .getByRole('dialog', { name: more, exact: true })
          .getByRole('button', {
            name: language === 'ru' ? 'Отметить все' : 'Select all',
            exact: true,
          })
          .click()
      }
      await expect(downloaded).not.toHaveClass(/selected/)
      await expect(downloaded.getByRole('checkbox')).toHaveCount(0)
      await expect(other.getByRole('checkbox')).toHaveAttribute('aria-checked', 'true')
      await expect(download).toBeEnabled()
      await expect(download).toContainText('(1)')
      if (width > 600) {
        await page.locator('thead').getByRole('checkbox').click()
      } else {
        await page.getByRole('button', { name: more, exact: true }).click()
        await page
          .getByRole('dialog', { name: more, exact: true })
          .getByRole('button', {
            name: language === 'ru' ? 'Снять все' : 'Clear selection',
            exact: true,
          })
          .click()
      }
      await expect(other.getByRole('checkbox')).toHaveAttribute('aria-checked', 'false')
      await expect(download).toBeDisabled()
    }
  })
}

test('queue response clears the catalog selection after navigating away', async ({ page }) => {
  await fakeApi(page)
  let accept: (() => void) | undefined
  await page.route('**/api/downloads', async (route) => {
    await new Promise<void>((resolve) => (accept = resolve))
    await route.fulfill({ status: 202 })
  })
  await page.goto('/')
  await page.getByRole('link', { name: 'Загрузки', exact: true }).click()
  await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
  await page.getByRole('link', { name: 'Доступно', exact: true }).click()
  await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
  await page.getByRole('button', { name: /^Скачать/ }).click()
  await page
    .getByRole('dialog', { name: 'Скачать', exact: true })
    .getByRole('button', { name: 'Скачать', exact: true })
    .click()
  await expect.poll(() => Boolean(accept)).toBe(true)
  await page
    .getByRole('link', { name: 'Загрузки', exact: true })
    .evaluate((el: HTMLAnchorElement) => el.click())
  accept!()
  const localCheckbox = page.getByRole('checkbox', { name: 'Example App', exact: true })
  await expect(localCheckbox).toBeEnabled()
  await expect(localCheckbox).toHaveAttribute('aria-checked', 'true')
  await page.getByRole('link', { name: 'Доступно', exact: true }).click()
  await expect(page.getByRole('checkbox', { name: 'Example App', exact: true })).toHaveAttribute(
    'aria-checked',
    'false',
  )
})

test('catalog shows known sizes and unknown placeholders', async ({ page }) => {
  await fakeApi(page)
  await page.setViewportSize({ width: 1_440, height: 900 })
  const items = [...catalog, { ...catalog[0]!, id: '1003', name: 'Unknown Size App', size: null }]
  await page.route('**/api/catalog?**', (route) => {
    const delisted = new URL(route.request().url()).searchParams.get('delisted') === 'true'
    const visible = items.filter((item) => !delisted || item.delisted)
    return route.fulfill({
      json: { items: visible, total: visible.length, next: '', previous: '' },
    })
  })
  await page.goto('/')
  const size = page.getByRole('columnheader', { name: 'Размер', exact: true })
  await expect(page.getByText('Delisted App', { exact: true })).toBeVisible()
  await expect(size).toHaveCount(0)
  await page.getByRole('button', { name: 'Ещё', exact: true }).click()
  const filter = page.getByRole('switch', { name: 'Только скрытые в App Store', exact: true })
  await expect(filter).toHaveAttribute('aria-checked', 'true')
  const allApps = page.waitForRequest((r) => {
    const url = new URL(r.url())
    return url.pathname === '/api/catalog' && !url.searchParams.has('delisted')
  })
  await filter.click()
  await allApps
  await page
    .getByRole('dialog', { name: 'Ещё', exact: true })
    .getByRole('button', { name: 'Закрыть', exact: true })
    .click()
  await expect(size).toBeVisible()
  await expect(page.getByText('123 МБ', { exact: true })).toBeVisible()
  await expect(page.getByTitle('Размер неизвестен', { exact: true })).toHaveText('—')
  await expect(
    page.getByTitle('Apple не сообщает размер скрытого приложения в ответе магазина.', {
      exact: true,
    }),
  ).toHaveText('—')
  const request = page.waitForRequest(
    (r) => r.url().includes('/api/catalog?') && r.url().includes('sort=size'),
  )
  await size.getByRole('button').click()
  await request
  await expect(size).toHaveAttribute('aria-sort', 'ascending')
})

test('cached app artwork falls back when unavailable', async ({ page }) => {
  await fakeApi(page)
  await page.route('**/api/catalog?**', (route) =>
    route.fulfill({
      json: {
        items: [{ ...catalog[0], icon: '/api/icons/test.png' }],
        next: false,
        previous: false,
      },
    }),
  )
  let fail = false
  await page.route('**/api/icons/test.png', async (route) => {
    if (fail) await route.fulfill({ status: 404 })
    else
      await route.fulfill({
        contentType: 'image/png',
        body: Buffer.from(
          'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a/a0AAAAASUVORK5CYII=',
          'base64',
        ),
      })
  })
  await page.goto('/')
  const icon = page.locator('.app-icon img')
  await expect(icon).toBeVisible()
  await expect.poll(() => icon.evaluate((el) => (el as HTMLImageElement).naturalWidth)).toBe(1)
  fail = true
  await page.reload()
  await expect(page.locator('.app-icon svg')).toBeVisible()
  await expect(icon).toHaveCount(0)
})

test('overlapping table requests cannot leave a stale loading bar', async ({ page }) => {
  await fakeApi(page)
  await page.goto('/')
  await expect(page.getByText('Example App', { exact: true })).toBeVisible()
  await expect(page.locator('table')).toHaveAttribute('aria-busy', 'false')
  await page.clock.install()
  const replies: Array<() => Promise<void>> = []
  await page.route('**/api/catalog?**', async (route) => {
    await new Promise<void>((resolve) => {
      replies.push(async () => {
        await route.fulfill({
          json: { items: catalog, total: catalog.length, next: false, previous: false },
        })
        resolve()
      })
    })
  })
  await page
    .getByRole('columnheader', { name: 'Приложение', exact: true })
    .getByRole('button')
    .click()
  await expect.poll(() => replies.length).toBe(1)
  // Pointer actions are blocked while the table updates; its sort controls
  // remain reachable by keyboard and can start another request.
  const nextSort = page
    .getByRole('columnheader', { name: 'Куплено', exact: true })
    .getByRole('button')
  await page.keyboard.press('Tab')
  await expect(nextSort).toBeFocused()
  await page.keyboard.press('Enter')
  await expect.poll(() => replies.length).toBe(2)
  await replies[0]!()
  await page.clock.runFor(400)
  await expect(page.locator('table')).toHaveAttribute('aria-busy', 'true')
  await expect(page.locator('.load-line')).toBeVisible()
  await replies[1]!()
  await expect(page.locator('table')).toHaveAttribute('aria-busy', 'false')
  await page.clock.runFor(400)
  await expect(page.locator('.load-line')).toHaveCount(0)
})

test('clearing available apps cache empties rows and selection without changing library', async ({
  page,
}) => {
  await fakeApi(page)
  await page.goto('/available')
  await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
  await page.getByRole('button', { name: 'Ещё', exact: true }).click()
  await page.getByRole('button', { name: 'Очистить кэш доступных', exact: true }).click()
  const confirmation = page.getByRole('dialog', { name: 'Очистить кэш доступных', exact: true })
  const deletes: string[] = []
  page.on('request', (request) => {
    if (request.method() === 'DELETE') deletes.push(new URL(request.url()).pathname)
  })
  await expect(confirmation).toContainText('Вход и скачанные IPA сохранятся')
  expect(deletes).toEqual([])
  await confirmation.getByRole('button', { name: 'Отмена', exact: true }).click()
  await page.waitForFunction(() => !history.state?.modal)
  await expect(page.getByRole('checkbox', { name: 'Example App', exact: true })).toBeChecked()
  expect(deletes).toEqual([])
  await page.getByRole('button', { name: 'Ещё', exact: true }).click()
  await page.getByRole('button', { name: 'Очистить кэш доступных', exact: true }).click()
  await confirmation.getByRole('button', { name: 'Очистить', exact: true }).click()
  await expect.poll(() => deletes).toEqual(['/api/catalog'])
  await expect(page.getByRole('checkbox', { name: 'Example App', exact: true })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Скачать', exact: true })).toBeDisabled()
  await page.getByRole('link', { name: 'Загрузки', exact: true }).click()
  await expect(page.getByRole('checkbox', { name: 'Example App', exact: true })).toBeVisible()
  await page.getByRole('link', { name: 'Доступно', exact: true }).click()
  await expect(page.getByRole('checkbox', { name: 'Example App', exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Ещё', exact: true }).click()
  await expect(
    page.getByRole('button', { name: 'Очистить кэш доступных', exact: true }),
  ).toBeDisabled()
})

test('purchase-history authentication failure keeps the cached catalog and its notice', async ({
  page,
}) => {
  await fakeApi(page, true, false, false, false, true)
  await page.goto('/available')
  await expect(page.getByText('Example App', { exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Профиль', exact: true })).toBeVisible()
  await expect(page.getByRole('alert')).toContainText(
    'Apple не разрешила прочитать историю покупок.',
  )
  await expect(page.getByRole('alert')).toContainText('Сессия и сохранённый каталог оставлены.')
  await expect(page).toHaveURL(/available$/)
})

for (const { width, language, theme } of [
  { width: 375, language: 'ru', theme: 'dark' },
  { width: 1_280, language: 'en', theme: 'light' },
  { width: 375, language: 'en', theme: 'light' },
  { width: 1_280, language: 'ru', theme: 'dark' },
]) {
  test(`version picker is lazy and shows minimum OS at ${width} ${language} ${theme}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    if (width >= 600)
      await page.addInitScript(() => localStorage.setItem('appleseum.pageSize.versions', '10'))
    await fakeApi(page)
    const requests: { cursor: string; limit: number }[] = []
    let submitted: unknown
    await page.route('**/api/catalog/1001/versions', async (route) => {
      const input = route.request().postDataJSON()
      requests.push(input)
      const second = input.cursor === 'second'
      await route.fulfill({
        json: {
          name: 'Example App',
          total: 520,
          items: [
            {
              id: second ? '100' : '200',
              cursor: second ? 'first' : 'second',
              version: second ? '1.5' : '2.4.1',
              minimumOs: second ? '13.0' : '16.2',
              latest: !second,
              error: '',
            },
          ],
          next: !second,
          previous: second,
        },
      })
    })
    await page.route('**/api/downloads', async (route) => {
      submitted = route.request().postDataJSON()
      await route.fulfill({ status: 202 })
    })
    await page.goto('/')
    if (language === 'en') await page.getByRole('button', { name: 'English', exact: true }).click()
    if (theme === 'light')
      await page
        .getByRole('button', {
          name: language === 'ru' ? 'Светлая тема' : 'Light theme',
          exact: true,
        })
        .click()
    await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
    const download = language === 'ru' ? 'Скачать' : 'Download'
    const latest = language === 'ru' ? 'Последняя доступная' : 'Latest available'
    await page.getByRole('button', { name: new RegExp('^' + download) }).click()
    const dialog = page.getByRole('dialog', { name: download, exact: true })
    await expect(dialog.getByRole('radio', { name: latest, exact: true })).toBeChecked()
    expect(requests).toHaveLength(0)
    await dialog
      .getByRole('radio', {
        name: language === 'ru' ? 'Выбрать конкретную версию' : 'Choose a specific version',
      })
      .check()
    await expect(dialog.getByRole('radio', { name: /^2\.4\.1/ })).toBeVisible()
    await expect(dialog.getByRole('columnheader')).toHaveCount(2)
    expect(
      await dialog.locator('thead').evaluate((el) => el.getBoundingClientRect().height),
    ).toBeGreaterThan(10)
    expect(
      await dialog
        .locator('.version-option')
        .first()
        .evaluate((el) => el.getBoundingClientRect().height),
    ).toBeLessThan(100)
    await expect(dialog.getByRole('cell', { name: '16.2+', exact: true })).toBeVisible()
    const radioSpacing = await dialog
      .locator('.version-option')
      .first()
      .evaluate((label) => {
        const radio = label.querySelector('input')!.getBoundingClientRect()
        const text = label.querySelector('span')!.getBoundingClientRect()
        const cell = label.closest('td')!.getBoundingClientRect()
        return { inset: radio.left - cell.left, gap: text.left - radio.right }
      })
    expect(radioSpacing.inset).toBeGreaterThanOrEqual(11)
    expect(radioSpacing.gap).toBeGreaterThanOrEqual(11)
    expect(requests).toEqual([{ cursor: '', limit: 5, move: '', revision: '' }])
    await expect(dialog.getByRole('button', { name: download, exact: true })).toBeDisabled()
    await dialog
      .getByRole('button', { name: language === 'ru' ? 'Вперёд' : 'Next', exact: true })
      .click()
    await expect(dialog.getByText('1.5', { exact: true })).toBeVisible()
    await dialog.getByRole('radio', { name: '1.5', exact: true }).check()
    await dialog
      .getByRole('button', { name: language === 'ru' ? 'Назад' : 'Previous', exact: true })
      .click()
    await expect(dialog.getByRole('radio', { name: /^2\.4\.1/ })).toBeVisible()
    expect(requests).toHaveLength(3)
    await dialog
      .getByRole('button', { name: language === 'ru' ? 'Вперёд' : 'Next', exact: true })
      .click()
    await expect(dialog.getByText('1.5', { exact: true }).first()).toBeVisible()
    expect(requests).toHaveLength(3)
    expect(await dialog.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
    await dialog.getByRole('button', { name: download, exact: true }).click()
    await expect(dialog).toHaveCount(0)
    expect(submitted).toEqual({
      selection: { ids: ['1001'], filter: null },
      versions: [{ appId: '1001', versionId: '100' }],
    })
  })
}

test('version requirements show compatibility and the dialog scrollbar stays visible at rest', async ({
  page,
}) => {
  await page.setViewportSize({ width: 375, height: 640 })
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'userAgent', {
      value:
        'Mozilla/5.0 (iPhone; CPU iPhone OS 17_2 like Mac OS X) AppleWebKit/605.1.15 Version/17.2 Mobile/15E148 Safari/604.1',
    })
  })
  await fakeApi(page)
  await page.route('**/api/catalog/1001/versions', (route) =>
    route.fulfill({
      json: {
        name: 'Example App',
        total: 5,
        next: false,
        previous: false,
        items: ['16.2', '18.0', '17.2.1', '', '15.0'].map((minimumOs, i) => ({
          id: String(i),
          version: `2.${i}`,
          minimumOs: minimumOs,
          latest: i === 0,
          error: '',
        })),
      },
    }),
  )
  await page.goto('/')
  await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
  await page.getByRole('button', { name: 'Скачать', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Скачать', exact: true })
  await dialog.getByRole('radio', { name: 'Выбрать конкретную версию' }).check()
  await expect(dialog.getByText('16.2+', { exact: true })).toHaveClass(/os-compatible/)
  await expect(dialog.getByText('18.0+', { exact: true })).toHaveClass(/os-incompatible/)
  await expect(dialog.getByText('17.2.1+', { exact: true })).toHaveClass(/os-unknown/)
  const scrollbar = page.locator('.persistent-scrollbar.vertical:not([hidden])')
  await expect(scrollbar).toHaveCount(1)
  await expect(scrollbar).toBeVisible()
  const initial = await scrollbar.locator('span').evaluate((el) => el.getBoundingClientRect().top)
  await dialog.evaluate((el) => {
    el.scrollTop += 150
  })
  await expect
    .poll(() => scrollbar.locator('span').evaluate((el) => el.getBoundingClientRect().top))
    .toBeGreaterThan(initial)
  await page.waitForTimeout(1_200)
  await expect(scrollbar).toBeVisible()
  const track = (await scrollbar.boundingBox())!
  await page.mouse.click(track.x + track.width / 2, track.y + track.height - 3)
  await expect
    .poll(() => dialog.evaluate((el) => el.scrollTop + el.clientHeight >= el.scrollHeight - 2))
    .toBe(true)
})

for (const width of [375, 1_280]) {
  test(`library versions include minimum OS beside the release at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 812 })
    await fakeApi(page, false)
    await page.route('**/api/library?*', (route) =>
      route.fulfill({
        json: {
          items: ['17.5', ''].map(
            (minimumOs, i) =>
              ({
                id: `01900000-0000-7000-8000-00000000000${i + 1}`,
                appId: String(1_001 + i),
                name: minimumOs ? 'With requirement' : 'Without requirement',
                version: '1.4.1',
                minimumOs: minimumOs,
                status: 'done',
                received: 100,
                total: 100,
                size: 100,
                speed: 0,
                error: '',
                canManage: true,
                icon: '',
                createdAt: '',
              }) satisfies LibraryItem,
          ),
          total: 2,
          next: false,
          previous: false,
        },
      }),
    )
    await page.goto('/downloads')
    for (const [name, expected] of [
      ['With requirement', '1.4.1 (17.5+)'],
      ['Without requirement', '1.4.1'],
    ]) {
      const row = page.locator('tbody tr').filter({ has: page.getByText(name!, { exact: true }) })
      await expect(row.locator('.version-cell')).toHaveText(expected!)
      await expect(row.locator('.mobile-only.app-meta')).toContainText(expected!)
      if (width < 600) await expect(row.locator('.mobile-only.app-meta')).toBeVisible()
      else await expect(row.locator('.version-cell')).toBeVisible()
    }
    expect(await page.locator('#app').evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
  })
}

for (const width of [375, 1_280]) {
  test(`version loading reports real progress and resets cancelled requests at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await fakeApi(page)
    await page.addInitScript(() => {
      const original = window.fetch.bind(window)
      const control = {
        aborted: 0,
        requests: 0,
        accept: '',

        send: (_event: unknown, _finish = false) => {},
      }
      Object.assign(window, { versionProgressTest: control })
      window.fetch = async (input, init) => {
        const path =
          typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
        if (!path.endsWith('/api/catalog/1001/versions')) return original(input, init)
        const request = new Request(input, init)
        control.requests++
        control.accept = request.headers.get('Accept') ?? ''
        let ended = false
        const body = new ReadableStream<Uint8Array>({
          start(controller) {
            control.send = (event, finish = false) => {
              controller.enqueue(new TextEncoder().encode(JSON.stringify(event) + '\n'))
              if (finish) {
                ended = true
                controller.close()
              }
            }
            control.send({ progress: 0, page: null, error: '' })
            request.signal.addEventListener('abort', () => {
              if (ended) return
              ended = true
              control.aborted++
              controller.error(new DOMException('Canceled', 'AbortError'))
            })
          },
        })
        return new Response(body, { headers: { 'Content-Type': 'application/x-ndjson' } })
      }
    })

    const control = () =>
      page.evaluate(
        () =>
          (
            window as unknown as {
              versionProgressTest: { aborted: number; requests: number; accept: string }
            }
          ).versionProgressTest,
      )

    const send = (event: unknown, finish = false) =>
      page.evaluate(
        ({ event, finish }) => {
          ;(
            window as unknown as {
              versionProgressTest: { send: (value: unknown, done: boolean) => void }
            }
          ).versionProgressTest.send(event, finish)
        },
        { event, finish },
      )

    await page.goto('/')
    await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
    await page.getByRole('button', { name: 'Скачать', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: 'Скачать', exact: true })
    const specific = dialog.getByRole('radio', { name: 'Выбрать конкретную версию', exact: true })
    const progress = dialog.locator('.version-table').getByRole('progressbar')
    await specific.check()
    await expect(progress).toHaveAttribute('aria-valuenow', '0')
    await send({ progress: 33, page: null, error: '' })
    await expect(progress).toContainText('33%')
    await expect(progress).toHaveAttribute('aria-valuenow', '33')
    expect((await control()).accept).toBe('application/x-ndjson')
    await dialog.getByRole('radio', { name: 'Последняя доступная', exact: true }).check()
    await expect(progress).toHaveCount(0)
    await expect.poll(async () => (await control()).aborted).toBe(1)
    await specific.check()
    await expect(progress).toHaveAttribute('aria-valuenow', '0')
    await send({ progress: 66, page: null, error: '' })
    await expect(progress).toContainText('66%')
    const result = {
      name: 'Example App',
      total: 2,
      next: true,
      previous: false,
      items: [
        { id: '200', cursor: 'next', version: '2.4.1', minimumOs: '17.5', latest: true, error: '' },
      ],
    }
    await send({ progress: 100, page: result, error: '' }, true)
    await expect(progress).toHaveCount(0)
    await expect(dialog.getByRole('radio', { name: /^2\.4\.1/ })).toBeVisible()
    await dialog.getByRole('button', { name: 'Вперёд', exact: true }).click()
    await expect(progress).toHaveAttribute('aria-valuenow', '0')
    await expect(dialog.getByRole('radio', { name: /^2\.4\.1/ })).toHaveCount(0)
    await send({ progress: 60, page: null, error: '' })
    await expect(progress).toContainText('60%')
    await send({ progress: 0, page: null, error: 'storage_failed' }, true)
    await expect(progress).toHaveCount(0)
    await expect(dialog.getByRole('alert')).toBeVisible()
    await expect(dialog.getByRole('radio', { name: /^2\.4\.1/ })).toHaveCount(0)
    await dialog.locator('.version-error button').click()
    await expect(progress).toHaveAttribute('aria-valuenow', '0')
    await send({ progress: 100, page: { ...result, next: false, previous: true }, error: '' }, true)
    await expect(progress).toHaveCount(0)
    await expect(dialog.getByRole('alert')).toHaveCount(0)
    expect((await control()).requests).toBe(4)
  })
}
