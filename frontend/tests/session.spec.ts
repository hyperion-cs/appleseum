import { devices } from '@playwright/test'
import { test, expect, account, catalog, fakeApi } from './fixtures.ts'

for (const ish of [true, false]) {
  test(`connection loss uses the last server environment (iSH: ${ish})`, async ({ page }) => {
    const control = await fakeApi(page, false)
    control.setState({ mode: { ish } })
    let disconnected = false
    let apiError = false
    await page.route('**/api/state', (route) => {
      if (disconnected) return route.abort('connectionrefused')
      if (apiError) return route.fulfill({ status: 503, json: { code: 'invalid_request' } })
      return route.fallback()
    })
    await page.goto('/login')
    await expect(page.getByRole('button', { name: 'Войти', exact: true })).toBeVisible()
    await expect(page.getByRole('alert')).toHaveCount(0)

    disconnected = true
    const alert = page.getByRole('alert')
    await expect(alert).toContainText('Связь с сервером потеряна.', { timeout: 10_000 })
    if (ish) await expect(alert).toContainText('iOS/iPadOS могла приостановить')
    else await expect(alert).not.toContainText('iSH')

    await page.getByRole('button', { name: 'English', exact: true }).click()
    await expect(alert).toContainText('Connection to the server lost.')
    if (ish) await expect(alert).toContainText('iOS/iPadOS may have suspended')
    else await expect(alert).not.toContainText('iSH')

    disconnected = false
    await expect(alert).toHaveCount(0, { timeout: 10_000 })
    apiError = true
    await expect(alert).toHaveText('Check the entered values.', { timeout: 10_000 })
    apiError = false
    await expect(alert).toHaveCount(0, { timeout: 10_000 })
    await expect(page.getByRole('dialog')).toHaveCount(0)
  })
}

for (const width of [375, 1_280]) {
  for (const empty of [true, false]) {
    test(`guest home opens sign-in and keeps direct downloads available at ${width}px (empty: ${empty})`, async ({
      page,
    }) => {
      await page.setViewportSize({ width, height: 900 })
      await fakeApi(page, false, true, false, true)
      let libraryReads = 0
      await page.route('**/api/library?*', (route) => {
        libraryReads++
        return empty
          ? route.fulfill({ json: { items: [], total: 0, next: false, previous: false } })
          : route.fallback()
      })
      await page.goto('/')
      await expect(page).toHaveURL('/login')
      await expect(page.getByLabel('Почта или телефон', { exact: false })).toBeVisible()
      expect(libraryReads).toBe(0)
      await page.getByRole('link', { name: 'Загрузки', exact: true }).click()
      await expect(page).toHaveURL('/downloads')
      await expect(page.getByRole('region', { name: 'Загрузки', exact: true })).toBeVisible()
      if (!empty) await expect(page.getByText('Example App', { exact: true })).toBeVisible()
      await expect.poll(() => libraryReads).toBeGreaterThan(0)
      await page.reload()
      await expect(page).toHaveURL('/downloads')
      await expect(page.getByRole('region', { name: 'Загрузки', exact: true })).toBeVisible()
      await page.goto('/')
      await expect(page).toHaveURL('/login')
    })
  }
}

test('Enter signs in and verification stays in progress until accepted', async ({ page }) => {
  await page.setViewportSize({ width: 1_440, height: 1_000 })
  await fakeApi(page, false)
  await page.goto('/login')
  await page.getByLabel('Почта или телефон', { exact: false }).fill('example@example.test')
  await page.getByLabel(/^Пароль/).fill('example-password')
  await page.getByLabel(/^Пароль/).press('Enter')
  await expect(page.getByRole('status')).toHaveText('Выполняется вход')
  await expect(page.getByRole('textbox', { name: 'Почта или телефон', exact: false })).toHaveCount(
    0,
  )
  await expect(page.getByLabel('Код подтверждения', { exact: false })).toBeVisible()
  await page.getByLabel('Код подтверждения', { exact: false }).fill('123456')
  await page.getByRole('button', { name: 'Войти', exact: true }).click()
  await expect(page.getByRole('status')).toHaveText('Выполняется вход')
  await expect(page.getByLabel(/^Пароль/)).toHaveCount(0)
  await expect(page.getByText('Example App', { exact: true })).toBeVisible()
  await expect(page.getByLabel(/^Пароль/)).toHaveCount(0)
})

test('rejected login uses one acknowledgement dialog and keeps the retry form', async ({
  page,
}) => {
  const control = await fakeApi(page, false)
  await page.route('**/api/login', (route) => {
    control.setState({ auth: { state: 'password', error: 'invalid_credentials' } })
    return route.fulfill({ status: 401, json: { code: 'invalid_credentials' } })
  })
  await page.goto('/login')
  await page.getByLabel('Почта или телефон', { exact: false }).fill('example@example.test')
  await page.getByLabel(/^Пароль/).fill('wrong-password')
  await page.getByRole('button', { name: 'Войти', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Действие не выполнено', exact: true })
  await expect(dialog.getByRole('alert')).toBeVisible()
  await expect(page.locator('.notice')).toHaveCount(0)
  await dialog.getByRole('button', { name: 'Понятно', exact: true }).click()
  await expect(page.getByLabel(/^Пароль/)).toBeEditable()
  await page.clock.install()
  await page.clock.runFor(5_200)
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.locator('.notice')).toHaveCount(0)
})

test('expired Apple session leaves the local library accessible', async ({ page }) => {
  const control = await fakeApi(page)
  await page.goto('/')
  await expect(page.getByText('Example App', { exact: true })).toBeVisible()
  control.setState({ account: null, catalog: { status: 'failed', error: 'session_expired' } })
  await expect(page.getByRole('alert')).toHaveText('Apple не принимает сессию. Войдите снова.')
  await expect(page).toHaveURL('/downloads')
  await expect(page.getByRole('region', { name: 'Загрузки', exact: true })).toBeVisible()
  await page.getByRole('link', { name: 'Войти', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Вход', exact: true })).toBeVisible()
})

for (const [path, title] of [
  ['/available', 'Доступно'],
  ['/downloads', 'Загрузки'],
  ['/profile', 'Профиль'],
]) {
  test(`direct route and reload: ${path}`, async ({ page }) => {
    await fakeApi(page)
    await page.goto(path)
    await expect(page.getByRole('region', { name: title, exact: true })).toBeVisible()
    await expect(page.getByRole('link', { name: title, exact: true })).toHaveAttribute(
      'aria-current',
      'page',
    )
    await page.reload()
    await expect(page).toHaveURL(path)
    await expect(page.getByRole('region', { name: title, exact: true })).toBeVisible()
  })
}

test('route history preserves workspace state without reloading', async ({ page }) => {
  await fakeApi(page)
  let refreshes = 0
  await page.route('**/api/catalog/refresh', async (route) => {
    refreshes++
    await route.fulfill({ status: 202 })
  })
  await page.goto('/')
  await expect(page).toHaveURL('/available')
  await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
  await page.getByRole('searchbox').fill('Example')
  await page.evaluate(() => {
    document.documentElement.dataset.navigationTest = 'preserved'
  })
  await page.getByRole('link', { name: 'Загрузки', exact: true }).click()
  await expect(page).toHaveURL('/downloads')
  await page.getByRole('link', { name: 'Профиль', exact: true }).click()
  await expect(page).toHaveURL('/profile')
  await page.goBack()
  await expect(page).toHaveURL('/downloads')
  await expect(page.getByRole('region', { name: 'Загрузки', exact: true })).toBeVisible()
  await page.goBack()
  await expect(page).toHaveURL('/available')
  await expect(page.getByRole('searchbox')).toHaveValue('Example')
  await expect(page.getByRole('checkbox', { name: 'Example App', exact: true })).toHaveAttribute(
    'aria-checked',
    'true',
  )
  await page.goForward()
  await expect(page).toHaveURL('/downloads')
  await expect(page.locator('html')).toHaveAttribute('data-navigation-test', 'preserved')
  expect(refreshes).toBe(0)
})

test('sign-in keeps the requested Apple section', async ({ page }) => {
  await fakeApi(page, false)
  await page.goto('/profile')
  await expect(page).toHaveURL('/downloads')
  await page.getByRole('link', { name: 'Войти', exact: true }).click()
  await page.getByLabel('Почта или телефон', { exact: false }).fill('example@example.test')
  await page.getByLabel(/^Пароль/).fill('example-password')
  await page.getByRole('button', { name: 'Войти', exact: true }).click()
  await page.getByLabel('Код подтверждения', { exact: false }).fill('123456')
  await page.getByRole('button', { name: 'Войти', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Профиль', exact: true })).toBeVisible()
  await expect(page).toHaveURL('/profile')
})

for (const width of [320, 375, 390, 430, 600]) {
  for (const admin of [false, true]) {
    test(`mobile navigation fits ${admin ? 'five' : 'four'} sections at ${width}px in both languages`, async ({
      page,
    }) => {
      await page.setViewportSize({ width, height: 800 })
      await fakeApi(page, true, true, false, admin)
      await page.goto('/downloads')
      const menu = page.locator('header nav')
      await expect(menu.getByRole('link')).toHaveCount(admin ? 5 : 4)
      for (const lang of ['ru', 'en']) {
        await expect(page.locator('html')).toHaveAttribute('lang', lang)
        await expect(menu.getByRole('link').first()).toContainText(
          lang === 'ru' ? 'Доступно' : 'Available',
        )
        const fits = await menu.evaluate((nav) => {
          const box = nav.getBoundingClientRect()
          const header = nav.closest('header')!
          const headerTop = header.getBoundingClientRect().top
          // The first grid row is the header bar; its bottom edge is where the divider was.
          const divider = headerTop + parseFloat(getComputedStyle(header).gridTemplateRows)
          const controls = [...header.querySelectorAll('.preferences button')]
          const links = [...nav.querySelectorAll('a')]
          const scrolls = nav.scrollWidth > nav.clientWidth + 1
          return (
            box.left >= 0 &&
            box.right <= document.documentElement.clientWidth &&
            (!scrolls || (innerWidth < 375 && getComputedStyle(nav).overflowX === 'auto')) &&
            box.height <= 72 &&
            controls.every((control) => {
              const rect = control.getBoundingClientRect()
              return (
                rect.height >= 44 &&
                rect.width >= 44 &&
                rect.top - headerTop >= 8 &&
                divider - rect.bottom >= 8
              )
            }) &&
            links.every((link) => {
              const rect = link.getBoundingClientRect()
              const label = link.querySelector('span')!
              const style = getComputedStyle(label)
              const line = parseFloat(style.lineHeight)
              return (
                style.whiteSpace === 'nowrap' &&
                label.getBoundingClientRect().height <= line + 1 &&
                label.scrollWidth <= label.clientWidth + 1 &&
                (scrolls || (rect.left >= box.left && rect.right <= box.right + 1)) &&
                rect.height >= 44 &&
                link.scrollWidth <= link.clientWidth + 1 &&
                link.querySelector('svg') !== null
              )
            })
          )
        })
        expect(fits).toBe(true)
        for (const link of await menu.getByRole('link').all()) {
          await link.scrollIntoViewIfNeeded()
          expect(
            await link.evaluate((item) => {
              const box = item.closest('nav')!.getBoundingClientRect()
              const rect = item.getBoundingClientRect()
              return rect.left >= box.left - 1 && rect.right <= box.right + 1
            }),
          ).toBe(true)
        }
        if (lang === 'ru') {
          await page.getByRole('button', { name: 'English', exact: true }).click()
          await page.getByRole('button', { name: 'Light theme', exact: true }).click()
        }
      }
    })
  }
}

test.describe('iPhone 11 Pro navigation', () => {
  const device = devices['iPhone 11 Pro']
  test.use({
    viewport: device.viewport,
    userAgent: device.userAgent,
    deviceScaleFactor: device.deviceScaleFactor,
    isMobile: true,
    hasTouch: true,
  })
  test('keeps labels intact after rotation and selected tabs visible with larger text', async ({
    page,
  }) => {
    const failures: string[] = []
    page.on('pageerror', (error) => failures.push(error.message))
    await fakeApi(page, true, true, false, true)
    await page.goto('/downloads')
    const menu = page.locator('header nav')
    await expect(menu.getByRole('link')).toHaveCount(5)
    for (const width of [375, 812, 375]) {
      await page.setViewportSize({ width, height: width === 812 ? 375 : 812 })
      await expect
        .poll(() =>
          menu.evaluate((nav) => {
            const box = nav.getBoundingClientRect()
            return (
              box.right <= innerWidth && box.height <= 72 && nav.scrollWidth <= nav.clientWidth + 1
            )
          }),
        )
        .toBe(true)
      for (const label of await menu.locator('a > span').all()) {
        expect(
          await label.evaluate((node) => {
            const range = document.createRange()
            range.selectNodeContents(node)
            return range.getClientRects().length === 1
          }),
        ).toBe(true)
      }
    }
    await menu.locator('a').evaluateAll((links) => {
      for (const link of links)
        (link as HTMLElement).style.setProperty('font-size', '20px', 'important')
    })
    for (const name of ['Настройки', 'Доступно']) {
      // Programmatic activation avoids Playwright scrolling the tab for the app.
      const link = menu.getByRole('link', { name, exact: true })
      await link.evaluate((node) => (node as HTMLElement).click())
      await expect(link).toHaveAttribute('aria-current', 'page')
      await expect
        .poll(() =>
          link.evaluate((node) => {
            const box = node.closest('nav')!.getBoundingClientRect()
            const item = node.getBoundingClientRect()
            return item.left >= box.left - 1 && item.right <= box.right + 1
          }),
        )
        .toBe(true)
    }
    expect(failures).toEqual([])
  })
})

test('restarting a pending sign-in cancels verification and keeps local device settings editable', async ({
  page,
}) => {
  const control = await fakeApi(page, false, false, false, true)
  let cancellations = 0
  await page.route('**/api/logout', (route) => {
    cancellations++
    control.setState({ auth: { state: 'ready' } })
    return route.fulfill({ status: 204 })
  })
  await page.goto('/login')
  await page.getByLabel('Почта или телефон', { exact: false }).fill('synthetic@example.test')
  await page.getByLabel(/^Пароль/).fill('synthetic-password')
  await page.getByRole('button', { name: 'Войти', exact: true }).click()
  await expect(page.getByLabel('Код подтверждения', { exact: false })).toBeVisible()
  const pendingState = page.waitForResponse(
    (response) => new URL(response.url()).pathname === '/api/state',
  )
  control.setState({ auth: { state: 'code' } })
  await pendingState
  await page.getByRole('link', { name: 'Настройки', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Модель', exact: true })).toBeEnabled()
  await expect(page.getByRole('button', { name: 'Версия macOS', exact: true })).toBeEnabled()
  await page.getByRole('link', { name: 'Войти', exact: true }).click()
  await page.getByRole('button', { name: 'Код не пришёл?', exact: true }).click()
  const help = page.getByRole('dialog', { name: 'Код не пришёл?', exact: true })
  await expect(help).toBeVisible()
  await help.getByRole('button', { name: 'Закрыть', exact: true }).click()
  await page.getByRole('button', { name: 'Начать заново', exact: true }).click()
  await expect.poll(() => cancellations).toBe(1)
  await expect(page.getByLabel('Код подтверждения', { exact: false })).toHaveCount(0)
  await page.getByRole('link', { name: 'Настройки', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Модель', exact: true })).toBeEnabled()
})

for (const width of [375, 1_280]) {
  test(`local guest can browse and share without signing in at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await fakeApi(page, false)
    const calls: string[] = []
    page.on('request', (request) => {
      if (new URL(request.url()).pathname.startsWith('/api/'))
        calls.push(request.method() + ' ' + new URL(request.url()).pathname)
    })
    await page.goto('/downloads')
    await expect(page).toHaveURL('/downloads')
    const header = page.getByRole('banner')
    await expect(header.getByRole('link', { name: 'Загрузки', exact: true })).toBeVisible()
    await expect(header.getByRole('link', { name: 'Войти', exact: true })).toBeVisible()
    await expect(header.getByRole('link', { name: 'Доступно', exact: true })).toHaveCount(0)
    await expect(header.getByRole('link', { name: 'Профиль', exact: true })).toHaveCount(0)
    await expect(page.getByRole('region', { name: 'Загрузки', exact: true })).toBeVisible()
    await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
    await page.getByRole('button', { name: 'Раздать', exact: true }).click()
    await expect(page.getByRole('dialog')).toBeVisible()
    expect(calls).toContain('POST /api/shares')
    expect(calls.some((call) => /api\/(login|catalog|profile)/.test(call))).toBe(false)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.reload()
    await expect(page).toHaveURL('/downloads')
    await expect(page.getByRole('region', { name: 'Загрузки', exact: true })).toBeVisible()
    await header.getByRole('link', { name: 'Войти', exact: true }).click()
    await expect(page).toHaveURL('/login')
    await expect(page.getByLabel('Почта или телефон', { exact: false })).toBeVisible()
    await header.getByRole('link', { name: 'Загрузки', exact: true }).click()
    await expect(page).toHaveURL('/downloads')
  })
}

test('local sign-out opens the library and keeps an active share', async ({ page }) => {
  await fakeApi(page)
  await page.goto('/downloads')
  await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
  await page.getByRole('button', { name: 'Раздать', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.getByRole('link', { name: 'Профиль', exact: true }).click()
  await page.getByRole('banner').getByRole('button', { name: 'Выйти', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Выйти', exact: true }).click()
  await expect(page).toHaveURL('/downloads')
  await expect(page.getByRole('link', { name: 'Войти', exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Профиль', exact: true })).toHaveCount(0)
  await expect(page.getByRole('region', { name: 'Загрузки', exact: true })).toBeVisible()
  await expect(page.locator('.downloads-table')).toBeVisible()
  await page.getByRole('button', { name: /^Раздачи/ }).click()
  await expect(page.locator('.shares-section')).toBeVisible()
})

for (const width of [375, 1_280]) {
  test(`sign-out warning explains re-entry and cancel sends no request at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 812 })
    await fakeApi(page)
    let exits = 0
    await page.route('**/api/logout', (route) => {
      exits++
      return route.fulfill({ status: 204 })
    })
    await page.goto('/profile')
    await page.getByRole('banner').getByRole('button', { name: 'Выйти', exact: true }).click()
    let dialog = page.getByRole('dialog', { name: 'Выйти из аккаунта?', exact: true })
    await expect(dialog).toContainText(
      'При следующем входе потребуется заново ввести логин и пароль.',
    )
    await dialog.getByRole('button', { name: 'Отмена', exact: true }).click()
    await page.waitForFunction(() => !history.state?.modal)
    expect(exits).toBe(0)
    await page.getByRole('button', { name: 'English', exact: true }).click()
    await page.getByRole('banner').getByRole('button', { name: 'Sign out', exact: true }).click()
    dialog = page.getByRole('dialog', { name: 'Sign out of your account?', exact: true })
    await expect(dialog).toContainText('You will need to enter your sign-in details again.')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await dialog.getByRole('button', { name: 'Sign out', exact: true }).click()
    await expect.poll(() => exits).toBe(1)
    await expect(dialog).toHaveCount(0)
  })
}

for (const action of ['logout', 'catalog', 'stop']) {
  test(`${action} refusal replaces confirmation without a second dialog or request`, async ({
    page,
  }) => {
    await fakeApi(page)
    const endpoint =
      action === 'logout' ? '/logout' : action === 'catalog' ? '/catalog' : '/shares/stop'
    let attempts = 0
    await page.route('**/api' + endpoint, (route) => {
      if (action === 'catalog' && route.request().method() !== 'DELETE') return route.fallback()
      attempts++
      return route.fulfill({ status: 409, json: { code: 'action_unavailable' } })
    })
    await page.goto(
      action === 'logout' ? '/profile' : action === 'catalog' ? '/available' : '/downloads',
    )
    let button: string
    if (action === 'logout') {
      await page.getByRole('button', { name: 'Выйти', exact: true }).first().click()
      button = 'Выйти'
    } else if (action === 'catalog') {
      await page.getByRole('button', { name: 'Ещё', exact: true }).click()
      await page.getByRole('button', { name: 'Очистить кэш доступных', exact: true }).click()
      button = 'Очистить'
    } else {
      await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
      await page.getByRole('button', { name: 'Раздать', exact: true }).click()
      await page.getByRole('dialog').getByRole('button', { name: 'Закрыть', exact: true }).click()
      await page.waitForFunction(() => !history.state?.modal)
      await page.getByRole('button', { name: /^Остановить —/ }).click()
      button = 'Остановить'
    }
    await page.getByRole('dialog').getByRole('button', { name: button, exact: true }).click()
    const failure = page.getByRole('dialog', { name: 'Действие не выполнено', exact: true })
    await expect(failure).toBeVisible()
    await expect(page.getByRole('dialog')).toHaveCount(1)
    await expect(failure.getByRole('button', { name: button, exact: true })).toHaveCount(0)
    await expect(failure.getByRole('button', { name: 'Отмена', exact: true })).toHaveCount(0)
    await failure.getByRole('button', { name: 'Понятно', exact: true }).click()
    await page.waitForFunction(() => !history.state?.modal)
    expect(attempts).toBe(1)
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(page.locator('.notice')).toHaveCount(0)
  })
}
