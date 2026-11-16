import { test, expect, account, catalog, fakeApi } from './fixtures.ts'

for (const width of [390, 820, 1_440]) {
  test(`layout, selection and page size at ${width}`, async ({ page }) => {
    const failures: string[] = []
    page.on('pageerror', (e) => failures.push(e.message))
    await page.setViewportSize({ width, height: 900 })
    await fakeApi(page)
    await page.goto('/')
    await expect(page.getByText('Example App', { exact: true })).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
    await expect(page.getByRole('checkbox', { name: 'Example App', exact: true })).toHaveAttribute(
      'aria-checked',
      'true',
    )
    const control = page.getByRole('button', { name: 'Строк', exact: true })
    await control.click()
    await expect(page.getByRole('dialog', { name: 'Строк' })).toBeVisible()
    await page.getByRole('option', { name: '50', exact: true }).click()
    await expect(control).toContainText('50')
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await page.getByRole('button', { name: 'Светлая тема', exact: true }).click()
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
    await page.getByRole('button', { name: 'English', exact: true }).click()
    await expect(page.getByRole('link', { name: 'Downloads', exact: true })).toBeVisible()
    await page.getByRole('link', { name: 'Downloads', exact: true }).click()
    await expect(page.getByText(account.email, { exact: true })).toHaveCount(0)
    await page.getByRole('link', { name: 'Profile', exact: true }).click()
    await expect(page.getByText(account.email, { exact: true })).toBeVisible()
    await expect(page.getByText('12345', { exact: true })).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    expect(failures).toEqual([])
  })
}

test('selected controls keep their fill on hover in both themes', async ({ page }) => {
  await page.setViewportSize({ width: 1_440, height: 900 })
  await fakeApi(page)
  await page.goto('/')
  for (const theme of ['dark', 'light']) {
    await page.locator('html').evaluate((el, value) => {
      el.dataset.theme = value
    }, theme)
    const checkbox = page.getByRole('checkbox', { name: 'Example App', exact: true })
    await checkbox.click()
    await expect(checkbox).toHaveAttribute('aria-checked', 'true')
    await page.mouse.move(0, 0)
    const expected = await checkbox.evaluate((el) => {
      const probe = document.createElement('span')
      probe.style.color = 'var(--primary)'
      el.appendChild(probe)
      const color = getComputedStyle(probe).color
      probe.remove()
      return color
    })
    await checkbox.hover()
    await expect(checkbox).toHaveCSS('background-color', expected)
    await checkbox.click()
    await page.getByRole('button', { name: 'Ещё', exact: true }).click()
    const toggle = page.getByRole('switch', { name: 'Только скрытые в App Store', exact: true })
    await expect(toggle).toHaveAttribute('aria-checked', 'true')
    await toggle.hover()
    await expect(toggle).toHaveCSS('background-color', expected)
    await page.getByRole('button', { name: 'Закрыть', exact: true }).click()
  }
})

test.describe('mobile page size dismissal', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })
  for (const value of [4, 20]) {
    test(`closing preserves ${value} rows after stepping`, async ({ page }) => {
      await page.addInitScript(() => localStorage.setItem('appleseum.pageSize.catalog', '25'))
      await fakeApi(page)
      await page.goto('/available')
      const trigger = page.getByRole('button', { name: 'Строк', exact: true })
      await trigger.tap()
      const dialog = page.getByRole('dialog', { name: 'Строк', exact: true })
      const close = dialog.getByRole('button', { name: 'Закрыть', exact: true })
      const before = await close.boundingBox()
      for (let i = 25; i > value; i--) {
        await dialog.getByRole('button', { name: 'Меньше строк', exact: true }).tap()
      }
      await expect(dialog.locator('output')).toHaveText(String(value))
      const after = await close.boundingBox()
      expect(after?.y).toBe(before?.y)
      await close.tap()
      await expect(dialog).toHaveCount(0)
      await expect(trigger).toContainText(String(value))
      await trigger.tap()
      await expect(dialog.locator('output')).toHaveText(String(value))
      await page.goBack()
      await expect(dialog).toHaveCount(0)
      await expect(trigger).toContainText(String(value))
    })
  }
})

for (const width of [375, 1_280]) {
  test(`page scrollbar survives lazy settings code and data loading at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 320 })
    await fakeApi(page, true, false, false, true)
    let releaseModule!: () => void, releaseDevice!: () => void
    const moduleGate = new Promise<void>((resolve) => {
      releaseModule = resolve
    })
    const deviceGate = new Promise<void>((resolve) => {
      releaseDevice = resolve
    })
    await page.route(/\/SettingsView-[^/]+\.js$/, async (route) => {
      await moduleGate
      await route.continue()
    })
    await page.route('**/api/admin/device', async (route) => {
      await deviceGate
      await route.fallback()
    })
    await page.goto('/profile')
    await expect(page.locator('.profile-details')).toBeVisible()
    const scrollbar = page.locator('.persistent-scrollbar.vertical').first()
    await expect(scrollbar).toBeVisible()
    const probe = await scrollbar.evaluateHandle((element) => {
      const bar = element as HTMLElement
      let framesHidden = 0,
        recording = true

      const sample = () => {
        if (bar.hidden || !bar.isConnected) framesHidden++
        if (recording) requestAnimationFrame(sample)
      }

      requestAnimationFrame(sample)
      return {
        stop: () => {
          recording = false
          return framesHidden
        },

        bar,
      }
    })
    const moduleRequested = page.waitForRequest(/\/SettingsView-[^/]+\.js$/)
    const deviceRequested = page.waitForRequest('**/api/admin/device')
    try {
      await page.getByRole('link', { name: 'Настройки', exact: true }).click()
      await moduleRequested
      await expect(page.locator('.profile-details')).toBeVisible()
      releaseModule()
      await deviceRequested
      await expect(page.locator('.profile-details')).toBeVisible()
      await page.evaluate(() => new Promise(requestAnimationFrame))
      releaseDevice()
      await expect(page.locator('.device-form')).toBeVisible()
      await page.evaluate(() => new Promise(requestAnimationFrame))
      expect(await probe.evaluate((probe) => probe.stop())).toBe(0)
      expect(await probe.evaluate((probe) => probe.bar.isConnected && !probe.bar.hidden)).toBe(true)
      await expect(page.locator('.profile-details.device-details')).toBeVisible()
    } finally {
      releaseModule()
      releaseDevice()
      await probe.dispose()
    }
  })
}

test('persistent scrollbars follow overflowing tabs and dynamic choice lists', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 812 })
  await fakeApi(page, false, true, false, true)
  await page.goto('/settings')
  await page.locator('.tabs a').evaluateAll((links) => {
    for (const link of links)
      (link as HTMLElement).style.setProperty('font-size', '24px', 'important')
  })
  await expect(page.locator('.persistent-scrollbar.horizontal:not([hidden])')).toBeVisible()
  await page.getByRole('button', { name: 'Часовой пояс', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: 'Часовой пояс', exact: true })
  const list = dialog.getByRole('listbox')
  await expect.poll(() => list.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true)
  await expect(page.locator('.persistent-scrollbar.vertical:not([hidden])')).toBeVisible()
  await dialog.getByRole('combobox').fill('Almaty')
  await expect(dialog.getByRole('option', { name: 'Asia/Almaty', exact: true })).toBeVisible()
  await expect(page.locator('.persistent-scrollbar.vertical:not([hidden])')).toHaveCount(0)
  await page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
  await expect(page.locator('.persistent-scrollbar.horizontal:not([hidden])')).toBeVisible()
})

for (const width of [375, 1_280]) {
  test(`page scrolling uses one app viewport and locks behind dialogs at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 640 })
    await fakeApi(page, false, false, false, true)
    await page.goto('/settings')
    await page.locator('.settings-panel').evaluate((el) => {
      el.style.minHeight = '200vh'
    })
    const app = page.locator('#app')
    await expect.poll(() => app.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true)
    await expect
      .poll(() =>
        page.evaluate(
          () => document.documentElement.scrollHeight - document.documentElement.clientHeight,
        ),
      )
      .toBeLessThanOrEqual(1)
    await expect(app).toHaveCSS('scrollbar-width', 'none')
    await page.keyboard.press('PageDown')
    await expect.poll(() => app.evaluate((el) => el.scrollTop)).toBeGreaterThan(0)
    await page.keyboard.press('Home')
    await expect.poll(() => app.evaluate((el) => el.scrollTop)).toBe(0)
    await page.mouse.move(200, 300)
    await page.mouse.wheel(0, 300)
    await expect.poll(() => app.evaluate((el) => el.scrollTop)).toBeGreaterThan(0)
    await page.getByRole('button', { name: 'Создать новое устройство', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: 'Создать новое устройство', exact: true })
    await expect(dialog).toBeVisible()
    await expect(app).toHaveCSS('overflow-y', 'hidden')
    const offset = await app.evaluate((el) => el.scrollTop)
    await page.mouse.move(2, 2)
    await page.mouse.wheel(0, 300)
    expect(await app.evaluate((el) => el.scrollTop)).toBe(offset)
    await page.keyboard.press('Escape')
    await expect(dialog).toHaveCount(0)
    await expect(app).toHaveCSS('overflow-y', 'auto')
    expect(await page.evaluate(() => window.scrollY)).toBe(0)
  })
}

for (const width of [375, 1_280]) {
  test(`action failures remain visible inside the More dialog at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: width === 1_280 ? 360 : 900 })
    await fakeApi(page, false)
    await page.route('**/api/library/export', (route) =>
      route.fulfill({ status: 409, json: { code: 'missing_package' } }),
    )
    await page.goto('/downloads')
    await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
    await page.getByRole('button', { name: 'Ещё', exact: true }).click()
    const menu = page.getByRole('dialog', { name: 'Ещё', exact: true })
    await menu.getByRole('button', { name: 'Экспорт .ipa', exact: true }).click()
    await expect(menu.getByRole('alert')).toBeInViewport()
    await expect(page.getByRole('alert')).toHaveCount(1)
    let selections = 0
    await page.route('**/api/*/selection', (route) => {
      selections++
      return route.fulfill({ status: 503, json: { code: 'storage_failed' } })
    })
    if (width > 600) {
      await expect(menu.getByRole('button', { name: 'Отметить все', exact: true })).toHaveCount(0)
      return
    }
    await menu.getByRole('button', { name: 'Отметить все', exact: true }).click()
    await expect.poll(() => selections).toBe(1)
    await expect(menu.getByRole('alert')).toBeInViewport()
    await expect(page.getByRole('alert')).toHaveCount(1)
  })
  test(`five rows remain selectable after choosing a larger page at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await fakeApi(page, false)
    await page.goto('/downloads')
    const size = page.getByRole('button', { name: 'Строк', exact: true })
    await size.click()
    await page.getByRole('option', { name: '20', exact: true }).click()
    await expect(size).toContainText('20')
    await expect.poll(() => page.evaluate(() => history.state?.modal ?? null)).toBeNull()
    await size.click()
    await page.getByRole('option', { name: '5', exact: true }).click()
    await expect(size).toContainText('5')
  })
}

test('desktop choices close when their trigger scrolls out of view', async ({ page }) => {
  await page.setViewportSize({ width: 1_280, height: 320 })
  await fakeApi(page, false, false, false, true)
  await page.goto('/settings')
  await page.locator('.settings-panel').evaluate((el) => {
    el.style.minHeight = '200vh'
  })
  await page.getByRole('button', { name: 'Модель', exact: true }).click()
  const choice = page.getByRole('dialog', { name: 'Модель', exact: true })
  await expect(choice).toBeVisible()
  await page.locator('#app').evaluate((el) => {
    el.scrollTop = el.scrollHeight
  })
  await expect(choice).toHaveCount(0)
})
