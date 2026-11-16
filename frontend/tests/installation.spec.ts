import { devices, type Page } from '@playwright/test'
import { test, expect, fakeApi } from './fixtures.ts'
import type { InstallationProgress } from '../src/api/types.ts'

const profile = devices['iPhone 13']!
test.use({
  viewport: profile.viewport,
  userAgent: profile.userAgent,
  isMobile: true,
  hasTouch: true,
})

function progress(stage: InstallationProgress['stage'] = 'waiting', percent = 0, skipped = 0) {
  return { stage, percent, skipped } satisfies InstallationProgress
}

// count is the number of apps in the share the dialog opens for.
async function openInstallation(page: Page, count = 1) {
  await page.clock.install()
  await page.addInitScript(() => {
    document.addEventListener(
      'click',
      (event) => {
        const link = event.target instanceof Element ? event.target.closest('a[href]') : null
        if (link?.getAttribute('href')?.startsWith('itms-services:')) event.preventDefault()
      },
      true,
    )
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
    document.execCommand = (command) => command === 'copy'
  })
  await fakeApi(page)
  if (count > 1) {
    const share = {
      id: 'test-share',
      url: 'itms-services://localhost/?action=download-manifest&url=https%3A%2F%2F192.168.50.10%3A8443%2Fmanifests%2Ftest.plist',
      count,
      icon: '',
    }
    await page.route('**/api/shares', (route) =>
      route.request().method() === 'POST'
        ? route.fulfill({ status: 201, json: share })
        : route.fallback(),
    )
    await page.route('**/api/shares/test-share', (route) => route.fulfill({ json: share }))
  }
  const control = { status: progress(), unavailable: false, traces: [] as string[] }
  await page.route('**/api/shares/*/progress/*', (route) => {
    control.traces.push(new URL(route.request().url()).pathname.split('/').at(-1)!)
    return route.fulfill(
      control.unavailable
        ? { status: 503, json: { code: 'server_unavailable' } }
        : { json: control.status },
    )
  })
  await page.goto('/downloads')
  await page.getByRole('checkbox', { name: 'Example App', exact: true }).tap()
  await page.getByRole('button', { name: 'Раздать', exact: true }).tap()
  const dialog = page.getByRole('dialog', { name: 'Приложения готовы к установке' })
  await expect(dialog).toBeVisible()
  return { dialog, control }
}

async function expectStatusBlock(page: Page, dialog: ReturnType<Page['getByRole']>, tone: string) {
  const status = dialog.locator('.installation-status')
  await expect(status).toHaveCount(1)
  await expect(status).toHaveClass(`installation-status installation-status-${tone}`)
  const original = await page.locator('html').getAttribute('data-theme')
  for (const theme of ['dark', 'light']) {
    await page.locator('html').evaluate((element, value) => {
      element.dataset.theme = value
    }, theme)
    const typography = (element: Element) => {
      const style = getComputedStyle(element)
      return {
        fontFamily: style.fontFamily,
        fontSize: style.fontSize,
        fontWeight: style.fontWeight,
        lineHeight: style.lineHeight,
      }
    }
    expect(await status.evaluate(typography)).toEqual(
      await dialog.locator('.modal-body > p').last().evaluate(typography),
    )
    const colors = await status.evaluate((element, tone) => {
      const probe = document.createElement('div')
      const foreground = tone === 'info' ? 'accent' : tone
      const background = tone === 'info' ? 'selected' : `${tone}-bg`
      probe.style.color = `var(--${foreground})`
      probe.style.backgroundColor = `var(--${background})`
      document.body.append(probe)
      const expected = getComputedStyle(probe)
      const actual = getComputedStyle(element)
      const colors = {
        actual: [actual.color, actual.backgroundColor],
        expected: [expected.color, expected.backgroundColor],
      }
      probe.remove()
      return colors
    }, tone)
    expect(colors.actual).toEqual(colors.expected)
  }
  await page.locator('html').evaluate((element, value) => {
    if (value === null) delete element.dataset.theme
    else element.dataset.theme = value
  }, original)
  await expect(page.getByRole('dialog')).toHaveCount(1)
}

test('the install dialog keeps one colored status block with consistent typography across stages', async ({
  page,
}) => {
  const { dialog, control } = await openInstallation(page)
  await dialog.getByRole('button', { name: 'Скопировать ссылку', exact: true }).tap()
  await page.clock.runFor(11_000)
  expect(control.traces).toHaveLength(0)
  await expect(dialog.locator('.install-feedback')).toHaveCount(0)
  const install = dialog.getByRole('link', { name: 'Установить', exact: true })
  await install.tap()
  await expect.poll(() => control.traces.length).toBeGreaterThan(0)
  const link = new URL((await install.getAttribute('href'))!)
  const trace = new URL(link.searchParams.get('url')!).searchParams.get('trace_id')!
  expect(trace).toMatch(/^[a-z0-9]{7}$/)
  expect(control.traces[0]).toBe(trace)
  await expect(dialog).not.toContainText(trace)
  await expect(dialog.getByRole('status')).toContainText('Подтвердите установку')
  await expectStatusBlock(page, dialog, 'info')
  control.status = progress('manifest')
  await page.clock.runFor(1_100)
  await expect(dialog.getByRole('status')).toHaveText('Загрузка манифеста…')
  await expectStatusBlock(page, dialog, 'info')
  control.status = progress('waitingIpa')
  await page.clock.runFor(1_100)
  await expect(dialog.getByRole('status')).toContainText('Манифест передан')
  await expectStatusBlock(page, dialog, 'info')
  control.status = progress('transferring', 43)
  await page.clock.runFor(1_100)
  await expect(dialog.getByRole('status')).toHaveText('Загрузка IPA-файлов (43%)…')
  await expectStatusBlock(page, dialog, 'info')
  await expect(dialog.locator('dl, ul, ol, progress, [role=progressbar]')).toHaveCount(0)
  control.status = progress('complete', 100)
  await page.clock.runFor(1_100)
  await expect(dialog.getByRole('status')).toContainText('Все файлы переданы')
  await expectStatusBlock(page, dialog, 'success')
  await expect(dialog.locator('.install-feedback')).toContainText('экране «Домой»')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await install.tap()
  await expect.poll(() => control.traces.at(-1)).not.toBe(trace)
  await dialog.getByRole('button', { name: 'Закрыть', exact: true }).tap()
  const before = control.traces.length
  await page.clock.runFor(15_000)
  expect(control.traces).toHaveLength(before)
})

for (const delay of [
  { kind: 'noManifest', text: 'Устройство пока не запросило' },
  { kind: 'noIpa', text: 'Манифест запрошен' },
  { kind: 'interrupted', text: 'Скачивание прервалось' },
] as const) {
  test(`${delay.kind} warns inside the install dialog and keeps observing until the device recovers`, async ({
    page,
  }) => {
    const { dialog, control } = await openInstallation(page)
    if (delay.kind === 'noIpa') control.status = progress('waitingIpa')
    if (delay.kind === 'interrupted') control.status = progress('transferring', 43)
    await dialog.getByRole('link', { name: 'Установить', exact: true }).tap()
    if (delay.kind === 'interrupted') {
      await expect(dialog.getByRole('status')).toContainText('43%')
      control.status = progress('interrupted', 43)
      await page.clock.runFor(1_100)
    } else {
      await page.clock.runFor(9_000)
      await expect(dialog.getByRole('alert')).toHaveCount(0)
      await page.clock.runFor(1_100)
    }
    const warning = dialog.getByRole('alert')
    await expect(warning).toContainText(delay.text)
    await expect(dialog.getByRole('status')).toHaveCount(0)
    await expectStatusBlock(page, dialog, 'warning')
    await expect(page.getByRole('dialog')).toHaveCount(1)
    await expect(dialog.locator('.install-feedback')).toContainText('экране «Домой»')
    const before = control.traces.length
    await page.clock.runFor(3_100)
    await expect.poll(() => control.traces.length).toBeGreaterThan(before)
    control.status = progress('transferring', 60)
    await page.clock.runFor(1_100)
    await expect(dialog.getByRole('status')).toHaveText('Загрузка IPA-файлов (60%)…')
    await expect(warning).toHaveCount(0)
    await expectStatusBlock(page, dialog, 'info')
  })
}

for (const outcome of [
  { count: 3, skipped: 1, text: 'Ещё 1 шт. устройство не запросило' },
  { count: 1, skipped: 1, text: 'Устройство не стало скачивать приложения' },
] as const) {
  test(`completion with ${outcome.skipped} of ${outcome.count} apps not requested says they may be installed`, async ({
    page,
  }) => {
    const { dialog, control } = await openInstallation(page, outcome.count)
    control.status = progress('waitingIpa')
    await dialog.getByRole('link', { name: 'Установить', exact: true }).tap()
    await expect(dialog.getByRole('status')).toContainText('Манифест передан')
    control.status = progress('complete', 100, outcome.skipped)
    await page.clock.runFor(1_100)
    await expect(dialog.getByRole('status')).toContainText(outcome.text)
    await expect(dialog.getByRole('alert')).toHaveCount(0)
    await expectStatusBlock(page, dialog, 'warning')
    const before = control.traces.length
    await page.clock.runFor(5_000)
    expect(control.traces).toHaveLength(before)
  })
}

for (const failure of [
  { kind: 'manifestFailed', text: 'Не удалось передать манифест' },
  { kind: 'unavailable', text: 'Не удаётся получить состояние скачивания' },
  { kind: 'expired', text: 'Данные о скачивании больше недоступны' },
] as const) {
  test(`${failure.kind} stays in the status block, stops observation and allows a new attempt`, async ({
    page,
  }) => {
    const { dialog, control } = await openInstallation(page)
    control.status = progress('waitingIpa')
    await dialog.getByRole('link', { name: 'Установить', exact: true }).tap()
    await expect(dialog.getByRole('status')).toContainText('Манифест передан')
    if (failure.kind === 'manifestFailed') control.status = progress('failed')
    if (failure.kind === 'expired') control.status = progress()
    if (failure.kind === 'unavailable') {
      control.unavailable = true
      // A single failed read is not enough to give up.
      await page.clock.runFor(1_100)
      await expect(dialog).toBeVisible()
      await page.clock.runFor(2_200)
    } else await page.clock.runFor(1_100)
    await expect(dialog).toBeVisible()
    await expect(dialog.getByRole('alert')).toHaveText(new RegExp(failure.text))
    await expect(dialog.getByRole('status')).toHaveCount(0)
    await expectStatusBlock(page, dialog, failure.kind === 'manifestFailed' ? 'danger' : 'warning')
    await expect(dialog).toContainText('экране «Домой»')
    const before = control.traces.length
    await page.clock.runFor(15_000)
    expect(control.traces).toHaveLength(before)
    await expect(dialog).toBeVisible()
    control.unavailable = false
    control.status = progress('transferring', 25)
    await dialog.getByRole('link', { name: 'Установить', exact: true }).tap()
    await expect.poll(() => control.traces.length).toBeGreaterThan(before)
    await expect(dialog.getByRole('status')).toHaveText('Загрузка IPA-файлов (25%)…')
    await expect(dialog.getByRole('alert')).toHaveCount(0)
    await expectStatusBlock(page, dialog, 'info')
    await dialog.getByRole('button', { name: 'Закрыть', exact: true }).tap()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    const after = control.traces.length
    await page.clock.runFor(5_000)
    await expect(page.getByRole('dialog')).toHaveCount(0)
    expect(control.traces).toHaveLength(after)
  })
}

test('copy failure replaces the share dialog without starting installation observation', async ({
  page,
}) => {
  const { dialog, control } = await openInstallation(page)
  await page.evaluate(() => {
    document.execCommand = () => false
  })
  await dialog.getByRole('button', { name: 'Скопировать ссылку', exact: true }).tap()
  const message = page.getByRole('dialog', { name: 'Действие не выполнено', exact: true })
  await expect(message).toBeVisible()
  await expect(message.getByRole('alert')).toHaveText(
    'Не удалось скопировать ссылку. Повторите попытку.',
  )
  await expect(dialog).toHaveCount(0)
  await expect(page.getByRole('dialog')).toHaveCount(1)
  await page.clock.runFor(15_000)
  expect(control.traces).toHaveLength(0)
})
