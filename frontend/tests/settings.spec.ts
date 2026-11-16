import { devices } from '@playwright/test'
import {
  test,
  expect,
  devicePlatform,
  deviceModels,
  deviceVersions,
  fakeApi,
  chooseOption,
} from './fixtures.ts'

for (const width of [375, 1_280]) {
  test(`settings help icons align with labels at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 })
    await fakeApi(page, false, false, false, true)
    await page.goto('/settings')
    await expect(page.locator('.device-field-label')).toHaveCount(4)
    for (const field of await page.locator('.device-field-label').all()) {
      expect(
        await field.evaluate((node) => {
          const label = node.querySelector('label')!.getBoundingClientRect()
          const button = node.querySelector('button')!.getBoundingClientRect()
          const icon = node.querySelector('button span')!.getBoundingClientRect()
          return (
            Math.abs((label.top + label.bottom - icon.top - icon.bottom) / 2) <= 1 &&
            button.width >= 44 &&
            button.height >= 44
          )
        }),
      ).toBe(true)
    }
    await expect(page.locator('.field-help')).toHaveCount(7)
    await page.getByRole('button', { name: 'Справка: Локаль', exact: true }).click()
    const help = page.getByRole('dialog', { name: 'Локаль', exact: true })
    await expect(help).toContainText('BCP 47')
    await help.getByRole('button', { name: 'Закрыть', exact: true }).click()
    await expect(help).toHaveCount(0)
  })
}

for (const width of [375, 768, 1_280]) {
  test(`unified request locale and time zone save independently at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await fakeApi(page, false, false, false, true)
    const defaults = { locale: 'en-US', timeZone: 'UTC' }
    let settings = { ...defaults }
    await page.route('**/api/admin/device', async (route) => {
      if (route.request().method() === 'PUT') settings = route.request().postDataJSON()
      await route.fulfill({
        json: {
          readOnly: false,
          settings: { ...devicePlatform, ...settings },
          defaults: { ...devicePlatform, ...defaults },
          models: deviceModels,
          osVersions: deviceVersions,
          id: '00000000-0000-4000-8000-000000000001',
          guid: '000000000000',
          model: 'MacBookPro18,1',
          osVersion: 'fixture',
          clientVersion: 'fixture',
          amdProvisioned: false,
        },
      })
    })
    await page.goto('/settings')
    await expect(page.locator('.device-field .choice-trigger')).toHaveCount(4)
    await expect(page.locator('select')).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Локаль', exact: true })).toHaveText('en-US')
    if (width === 1_280) {
      await page.getByRole('button', { name: 'English', exact: true }).click()
      await expect(page.getByRole('button', { name: 'Locale', exact: true })).toHaveText('en-US')
      await page.getByRole('button', { name: 'Русский', exact: true }).click()
    }
    await chooseOption(page, 'Локаль', 'ru-KZ')
    await expect(page.getByRole('button', { name: 'Часовой пояс', exact: true })).toContainText(
      'UTC',
    )
    await chooseOption(page, 'Часовой пояс', 'Asia/Almaty')
    await expect(page.getByRole('button', { name: 'Локаль', exact: true })).toContainText('ru-KZ')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    for (const select of await page.locator('.device-field .choice-trigger').all()) {
      expect((await select.boundingBox())!.height).toBeGreaterThanOrEqual(44)
    }
    if (width === 1_280) {
      const fields = await page
        .locator('.device-field')
        .evaluateAll((nodes) => nodes.map((node) => node.getBoundingClientRect().top))
      expect(Math.max(...fields) - Math.min(...fields)).toBeLessThanOrEqual(1)
      expect((await page.locator('.settings-panel .panel').boundingBox())!.height).toBeLessThan(700)
    }
    await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
    await expect(page.locator('.settings-panel form')).toHaveAttribute('aria-busy', 'false')
    expect(settings).toEqual({
      ...devicePlatform,
      locale: 'ru-KZ',
      timeZone: 'Asia/Almaty',
    })
    await page.reload()
    await expect(page.getByRole('button', { name: 'Локаль', exact: true })).toContainText('ru-KZ')
    await expect(page.getByRole('button', { name: 'Часовой пояс', exact: true })).toContainText(
      'Asia/Almaty',
    )
  })
}

for (const width of [375, 1_280]) {
  test(`device model and macOS choices stay compatible and persist at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await fakeApi(page, false, false, false, true)
    const defaults = { ...devicePlatform, locale: 'en-US', timeZone: 'UTC' }
    let settings = { ...defaults },
      writes = 0,
      identity = 1
    await page.route('**/api/admin/device{,/defaults}', (route) => {
      if (route.request().method() !== 'GET') {
        const next =
          route.request().method() === 'PUT' ? route.request().postDataJSON() : { ...defaults }
        if (next.model !== settings.model) identity++
        settings = next
        writes++
      }
      return route.fulfill({
        json: {
          settings,
          defaults,
          models: deviceModels,
          osVersions: deviceVersions,
          id: `00000000-0000-4000-8000-00000000000${identity}`,
          guid: `00000000000${identity}`,
          model: settings.model,
          osVersion: settings.osVersion,
          clientVersion: 'Configurator 2.21',
          amdProvisioned: false,
        },
      })
    })
    await page.goto('/settings')
    await page.getByRole('button', { name: 'Модель', exact: true }).click()
    let dialog = page.getByRole('dialog', { name: 'Модель', exact: true })
    await expect(dialog.getByRole('option')).toHaveCount(deviceModels.length)
    const labels = await dialog.innerText()
    expect(deviceModels.some((model: { id: string }) => labels.includes(model.id))).toBe(false)
    await expect(dialog.getByRole('option').first()).toContainText('2026')
    await dialog.getByRole('combobox').fill('MacBookAir9,1')
    await dialog.getByRole('option').click()
    await page.waitForFunction(() => !history.state?.modal)
    const os = page.getByRole('button', { name: 'Версия macOS', exact: true })
    await expect(os).toHaveText('macOS 15.7')
    await os.click()
    dialog = page.getByRole('dialog', { name: 'Версия macOS', exact: true })
    await expect(dialog.getByRole('option')).toHaveCount(1)
    await dialog.getByRole('option').click()
    await page.waitForFunction(() => !history.state?.modal)
    expect(writes).toBe(0)
    await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
    const warning = page.getByRole('dialog', { name: 'Сменить модель устройства?', exact: true })
    await expect(warning).toContainText('новые идентификаторы устройства')
    await expect(warning).toContainText('двухфакторной аутентификации')
    expect(writes).toBe(0)
    await warning.getByRole('button', { name: 'Отмена', exact: true }).click()
    await expect(warning).toHaveCount(0)
    await page.waitForFunction(() => !history.state?.modal)
    expect(writes).toBe(0)
    await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
    await warning.getByRole('button', { name: 'Изменить', exact: true }).click()
    await expect(page.locator('.settings-panel form')).toHaveAttribute('aria-busy', 'false')
    expect(identity).toBe(2)
    await expect(
      page.getByText('00000000-0000-4000-8000-000000000002', { exact: true }),
    ).toBeVisible()
    expect(settings).toEqual({
      model: 'MacBookAir9,1',
      osVersion: '15.7',
      locale: 'en-US',
      timeZone: 'UTC',
    })
    const restore = page.getByRole('dialog', { name: 'Вернуть штатные настройки', exact: true })
    await page.getByRole('button', { name: 'Вернуть штатные настройки', exact: true }).click()
    await expect(restore).toBeVisible()
    await expect(restore).toContainText('новые идентификаторы устройства')
    await expect(restore).toContainText('данные проверки запросов')
    await expect(restore).toContainText('двухфакторной аутентификации')
    await expect(restore).toContainText('Версия macOS: macOS 26.5')
    await expect(restore).toContainText(
      deviceModels.find((model) => model.id === defaults.model)!.name,
    )
    expect(writes).toBe(1)
    await restore.getByRole('button', { name: 'Отмена', exact: true }).click()
    await page.waitForFunction(() => !history.state?.modal)
    expect(writes).toBe(1)
    await page.reload()
    await expect(os).toHaveText('macOS 15.7')
    await chooseOption(page, 'Модель', 'Mac17,3')
    await expect(os).toHaveText('macOS 27.2')
    expect(
      await os.locator('span').evaluate((node) => {
        const range = document.createRange()
        range.selectNodeContents(node)
        return range.getClientRects().length
      }),
    ).toBe(1)
    await os.click()
    dialog = page.getByRole('dialog', { name: 'Версия macOS', exact: true })
    expect(await dialog.getByRole('option').allTextContents()).toEqual([
      'macOS 27.2',
      'macOS 27.0.1',
      'macOS 26.6.2',
      'macOS 26.5',
    ])
    await dialog.getByRole('option', { name: 'macOS 26.5', exact: true }).click()
    await expect(page.getByText('Configurator 2.21', { exact: true })).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.getByRole('button', { name: 'Вернуть штатные настройки', exact: true }).click()
    await restore.getByRole('button', { name: 'Вернуть', exact: true }).click()
    await expect(page.locator('.settings-panel form')).toHaveAttribute('aria-busy', 'false')
    expect(settings).toEqual(defaults)
    expect(identity).toBe(3)
    // An OS-only update keeps the identity and needs no destructive confirmation.
    await chooseOption(page, 'Версия macOS', '27.2')
    await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
    await expect(page.locator('.settings-panel form')).toHaveAttribute('aria-busy', 'false')
    expect(identity).toBe(3)
    expect(settings.osVersion).toBe('27.2')
    expect(writes).toBe(3)
    // OS compatibility is also shown before restoring defaults, without claiming
    // the unchanged model will replace the identity.
    await page.getByRole('button', { name: 'Вернуть штатные настройки', exact: true }).click()
    await expect(restore).toContainText('Версия macOS: macOS 26.5')
    await expect(restore).not.toContainText('новые идентификаторы')
    expect(writes).toBe(3)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await restore.getByRole('button', { name: 'Отмена', exact: true }).click()
    await page.waitForFunction(() => !history.state?.modal)
    expect(writes).toBe(3)
    await page.getByRole('button', { name: 'Вернуть штатные настройки', exact: true }).click()
    await restore.getByRole('button', { name: 'Вернуть', exact: true }).click()
    await expect(page.locator('.settings-panel form')).toHaveAttribute('aria-busy', 'false')
    expect(settings).toEqual(defaults)
    expect(identity).toBe(3)
    expect(writes).toBe(4)
  })
}

test('settings failure replaces its confirmation with an acknowledgement', async ({ page }) => {
  await fakeApi(page, true, false, false, true)
  await page.route('**/api/admin/device/reset', (route) =>
    route.fulfill({ status: 503, json: { code: 'storage_failed' } }),
  )
  await page.goto('/settings')
  await page.getByRole('button', { name: 'Создать новое устройство', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('button', { name: 'Создать', exact: true }).click()
  await expect(dialog).toHaveAccessibleName('Действие не выполнено')
  await expect(dialog.getByRole('button', { name: 'Создать', exact: true })).toHaveCount(0)
  await expect(dialog.getByRole('alert')).toBeVisible()
  await expect(page.getByRole('dialog')).toHaveCount(1)
  await expect(page.locator('.notice')).toHaveCount(0)
  await dialog.getByRole('button', { name: 'Понятно', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByRole('alert')).toHaveCount(0)
})

test('settings keep saved values outside the lists and help remains available while signed in', async ({
  page,
}) => {
  await fakeApi(page, true, false, false, true)
  const settings = {
    locale: 'en-EU',
    timeZone: 'Etc/GMT+3',
  }
  await page.route('**/api/admin/device', (route) =>
    route.fulfill({
      json: {
        settings: { ...devicePlatform, ...settings },
        defaults: { ...devicePlatform, ...settings },
        models: deviceModels,
        osVersions: deviceVersions,
        id: '00000000-0000-4000-8000-000000000001',
        guid: '000000000000',
        model: 'MacBookPro18,1',
        osVersion: 'fixture',
        clientVersion: 'fixture',
        amdProvisioned: true,
      },
    }),
  )
  await page.goto('/settings')
  for (const [label, value] of [
    [
      'Модель',
      deviceModels.find((model: { id: string }) => model.id === devicePlatform.model).name,
    ],
    ['Локаль', 'en-EU'],
    ['Часовой пояс', 'Etc/GMT+3'],
  ]) {
    await expect(page.getByRole('button', { name: label!, exact: true })).toContainText(value!)
    await expect(page.getByRole('button', { name: label!, exact: true })).toBeEnabled()
  }
  await expect(page.getByRole('button', { name: 'Версия macOS', exact: true })).toBeEnabled()
  await page.getByRole('button', { name: 'Справка: Локаль', exact: true }).click()
  await expect(page.getByRole('dialog', { name: 'Локаль', exact: true })).toBeVisible()
})

test('custom choices support keyboard search, cancellation and desktop dismissal', async ({
  page,
}) => {
  await page.setViewportSize({ width: 1_280, height: 800 })
  await fakeApi(page, false, false, false, true)
  await page.goto('/settings')
  const trigger = page.getByRole('button', { name: 'Локаль', exact: true })
  await trigger.focus()
  await trigger.press('ArrowDown')
  let dialog = page.getByRole('dialog', { name: 'Локаль', exact: true })
  const search = dialog.getByRole('combobox')
  await expect(search).toBeFocused()
  const initialBounds = await dialog.boundingBox()
  await search.fill('fr-CA')
  await expect(dialog.getByRole('option')).toHaveCount(1)
  expect(await dialog.boundingBox()).toEqual(initialBounds)
  await search.press('Enter')
  await expect(trigger).toBeFocused()
  await expect(trigger).toContainText('fr-CA')
  await trigger.click()
  dialog = page.getByRole('dialog', { name: 'Локаль', exact: true })
  await dialog.getByRole('combobox').fill('no matching value 987654')
  await expect(dialog.getByRole('status')).toHaveText('Ничего не найдено')
  expect(await dialog.boundingBox()).toEqual(initialBounds)
  await dialog.getByRole('combobox').press('Escape')
  await expect(dialog).toHaveCount(0)
  await expect(trigger).toBeFocused()
  await expect(trigger).toContainText('fr-CA')
  await trigger.click()
  await page.getByRole('heading', { name: 'Устройство Apple', exact: true }).click()
  await expect(dialog).toHaveCount(0)
  await trigger.click()
  await dialog.getByRole('combobox').press('Tab')
  await expect(dialog).toHaveCount(0)
  await expect(
    page.getByRole('button', { name: 'Справка: Часовой пояс', exact: true }),
  ).toBeFocused()
})

test('custom choices keep the selected time zone visible and fit a short desktop viewport', async ({
  page,
}) => {
  await page.setViewportSize({ width: 900, height: 440 })
  await fakeApi(page, false, false, false, true)
  await page.goto('/settings')
  const trigger = page.getByRole('button', { name: 'Часовой пояс', exact: true })
  await trigger.scrollIntoViewIfNeeded()
  await trigger.click()
  const dialog = page.getByRole('dialog', { name: 'Часовой пояс', exact: true })
  await expect(dialog.getByRole('option', { name: 'UTC', exact: true })).toHaveAttribute(
    'aria-selected',
    'true',
  )
  const panel = await dialog.boundingBox()
  expect(panel!.y).toBeGreaterThanOrEqual(0)
  expect(panel!.y + panel!.height).toBeLessThanOrEqual(440)
  const list = await dialog.getByRole('listbox').boundingBox()
  const selected = await dialog.getByRole('option', { name: 'UTC', exact: true }).boundingBox()
  expect(selected!.y).toBeGreaterThanOrEqual(list!.y - 1)
  expect(selected!.y + selected!.height).toBeLessThanOrEqual(list!.y + list!.height + 1)
})

test.describe('custom choice sheet on iPhone', () => {
  const device = devices['iPhone 11 Pro']
  test.use({
    viewport: device.viewport,
    userAgent: device.userAgent,
    deviceScaleFactor: device.deviceScaleFactor,
    isMobile: true,
    hasTouch: true,
  })
  test('searches long lists, handles Back and rotation without changing the value', async ({
    page,
  }) => {
    await fakeApi(page, false, false, false, true)
    await page.goto('/settings')
    const trigger = page.getByRole('button', { name: 'Часовой пояс', exact: true })
    await trigger.click()
    let dialog = page.getByRole('dialog', { name: 'Часовой пояс', exact: true })
    await expect(dialog).toHaveClass(/sheet-modal/)
    await expect(dialog.getByRole('listbox')).toBeFocused()
    const initialBounds = await dialog.boundingBox()
    await dialog.getByRole('combobox').fill('Almaty')
    expect(await dialog.boundingBox()).toEqual(initialBounds)
    await dialog.getByRole('option', { name: 'Asia/Almaty', exact: true }).tap()
    await expect(trigger).toContainText('Asia/Almaty')
    await page.waitForFunction(() => !history.state?.modal)
    await trigger.click()
    await page.goBack()
    await expect(dialog).toHaveCount(0)
    await expect(page).toHaveURL('/settings')
    await expect(trigger).toContainText('Asia/Almaty')
    await trigger.click()
    await page.setViewportSize({ width: 812, height: 375 })
    await expect(dialog).toHaveClass(/sheet-modal/)
    const bounds = await dialog.boundingBox()
    expect(bounds!.x).toBeGreaterThanOrEqual(0)
    expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(375)
  })
})

for (const width of [375, 1_280]) {
  test(`macOS changes while signed in without logout or identity changes at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await fakeApi(page, true, false, false, true)
    let settings = { ...devicePlatform, locale: 'en-US', timeZone: 'UTC' }
    let writes = 0
    const actions: string[] = []
    page.on('request', (request) => {
      if (request.method() === 'POST') actions.push(new URL(request.url()).pathname)
    })
    await page.route('**/api/admin/device', (route) => {
      if (route.request().method() === 'PUT') {
        settings = route.request().postDataJSON()
        writes++
      }
      return route.fulfill({
        json: {
          settings,
          defaults: { ...devicePlatform, locale: 'en-US', timeZone: 'UTC' },
          models: deviceModels,
          osVersions: deviceVersions,
          id: '00000000-0000-4000-8000-000000000001',
          guid: '000000000001',
          clientVersion: 'Configurator 2.21',
          amdProvisioned: true,
        },
      })
    })
    await page.goto('/settings')
    for (const label of ['Модель', 'Локаль', 'Часовой пояс', 'Создать новое устройство'])
      await expect(page.getByRole('button', { name: label, exact: true })).toBeEnabled()
    await expect(
      page.getByRole('button', { name: 'Вернуть штатные настройки', exact: true }),
    ).toBeDisabled()
    await chooseOption(page, 'Версия macOS', '27.2')
    await page.getByRole('button', { name: 'Сохранить', exact: true }).click()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(page.locator('.settings-panel form')).toHaveAttribute('aria-busy', 'false')
    expect(writes).toBe(1)
    expect(settings.osVersion).toBe('27.2')
    expect(actions).not.toContain('/api/logout')
    expect(actions).not.toContain('/api/login')
    await page.reload()
    await expect(page.getByRole('button', { name: 'Версия macOS', exact: true })).toContainText(
      '27.2',
    )
    await expect(
      page.getByText('00000000-0000-4000-8000-000000000001', { exact: true }),
    ).toBeVisible()
    await expect(page.getByRole('button', { name: 'Выйти', exact: true })).toBeEnabled()
  })
}

for (const width of [320, 375, 1_280]) {
  test(`saved model names remain complete without technical IDs at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 900 })
    await fakeApi(page, true, false, false, true)
    const models = deviceModels as Array<{ id: string; name: string; minOs: number; maxOs: number }>
    const longest = models.reduce((a, b) => (a.name.length > b.name.length ? a : b))
    let model = models.find((item) => item.id === devicePlatform.model)!
    await page.route('**/api/admin/device', (route) =>
      route.fulfill({
        json: {
          settings: {
            model: model.id,
            osVersion: deviceVersions.find((version: { version: string }) => {
              const major = Number(version.version.split('.')[0])
              return major >= model.minOs && major <= model.maxOs
            }).version,
            locale: 'en-US',
            timeZone: 'America/Argentina/ComodRivadavia',
          },
          defaults: { ...devicePlatform, locale: 'en-US', timeZone: 'UTC' },
          models: deviceModels,
          osVersions: deviceVersions,
          id: '00000000-0000-4000-8000-000000000001',
          guid: '000000000001',
          clientVersion: 'Configurator 2.21',
          amdProvisioned: true,
        },
      }),
    )
    for (const item of [model, longest]) {
      model = item
      await page.goto('/settings')
      const field = page.getByRole('button', { name: 'Модель', exact: true })
      await expect(field).toBeEnabled()
      await expect(field).toHaveText(model.name)
      expect(
        await field.locator('.choice-label').evaluate((node) => {
          const bounds = node.closest('button')!.getBoundingClientRect()
          const range = document.createRange()
          range.selectNodeContents(node)
          return (
            getComputedStyle(node).whiteSpace === 'normal' &&
            node.scrollWidth <= node.clientWidth + 1 &&
            [...range.getClientRects()].every(
              (rect) =>
                rect.left >= bounds.left &&
                rect.right <= bounds.right &&
                rect.top >= bounds.top &&
                rect.bottom <= bounds.bottom,
            )
          )
        }),
      ).toBe(true)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(
        true,
      )
      const zone = page.getByRole('button', { name: 'Часовой пояс', exact: true })
      await expect(zone).toHaveText('America/Argentina/ComodRivadavia')
      expect(
        await zone
          .locator('.choice-label')
          .evaluate((node) => node.scrollWidth <= node.clientWidth + 1),
      ).toBe(true)
    }
  })
}

test('navigation can leave settings while their initial data is pending', async ({ page }) => {
  await fakeApi(page, true, false, false, true)
  let release!: () => void
  const gate = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route('**/api/admin/device', async (route) => {
    await gate
    await route.fallback()
  })
  await page.goto('/profile')
  await expect(page.locator('.profile-details')).toBeVisible()
  const requested = page.waitForRequest('**/api/admin/device')
  await page.getByRole('link', { name: 'Настройки', exact: true }).click()
  await requested
  await expect(page.locator('.profile-details')).toBeVisible()
  await page.getByRole('link', { name: 'Доступно', exact: true }).click()
  await expect(page.getByText('Example App', { exact: true })).toBeVisible()
  const response = page.waitForResponse('**/api/admin/device')
  release()
  await response
  await expect(page).toHaveURL('/available')
  await expect(page.locator('.settings-panel')).toHaveCount(0)
})
