import { type Page } from '@playwright/test'
import type { LibraryItem, LibrarySelectionResult } from '../src/api/types'
import { test, expect, catalog, fakeApi, publicPreview } from './fixtures.ts'

test('library updates download progress and cancels the transfer', async ({ page }) => {
  await fakeApi(page)
  let received = 1_000_000,
    canceled = false
  await page.route('**/api/library?**', async (route) => {
    received += 1_000_000
    await route.fulfill({
      json: {
        items: canceled
          ? []
          : [
              {
                id: '01900000-0000-7000-8000-000000000002',
                appId: '1002',
                name: 'Downloading App',
                status: 'downloading',
                received,
                total: 10_000_000,
                speed: 2_000_000,
                version: '',
                minimumOs: '',
                size: 10_000_000,
                error: '',
                createdAt: '2026-10-25T10:00:00Z',
                canManage: true,
                icon: '',
              } satisfies LibraryItem,
            ],
        total: canceled ? 0 : 1,
        next: false,
        previous: false,
      },
    })
  })
  await page.route('**/api/downloads/*/cancel', async (route) => {
    expect(route.request().method()).toBe('POST')
    expect(new URL(route.request().url()).pathname).toBe(
      '/api/downloads/01900000-0000-7000-8000-000000000002/cancel',
    )
    canceled = true
    await route.fulfill({ status: 202 })
  })
  await page.goto('/')
  await page.getByRole('link', { name: 'Загрузки', exact: true }).click()
  const progress = page.getByRole('progressbar', { name: 'Downloading App' })
  await expect(progress).toHaveAttribute('aria-valuenow', '20')
  await expect(page.getByText('16 Мбит/с', { exact: true })).toBeVisible()
  await expect(progress).toHaveAttribute('aria-valuenow', '30')
  await page.getByRole('checkbox', { name: 'Downloading App', exact: true }).click()
  await page
    .getByRole('button', { name: 'Отменить загрузку — Downloading App', exact: true })
    .click()
  await expect(page.getByText('Downloading App', { exact: true })).toHaveCount(0)
  await expect(page.getByRole('alert')).toHaveCount(0)
})

test('selection actions stay gray and disabled until apps are selected', async ({ page }) => {
  await fakeApi(page)
  await page.goto('/')
  const download = page.getByRole('button', { name: /^Скачать/ })
  await expect(download).toBeDisabled()
  await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
  await expect(download).toBeEnabled()
  await page.getByRole('link', { name: 'Загрузки', exact: true }).click()
  const remove = page.getByRole('button', { name: 'Удалить', exact: true }),
    share = page.getByRole('button', { name: /^Раздать/ })
  await expect(remove).toBeDisabled()
  await expect(share).toBeDisabled()
  await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
  await expect(remove).toBeEnabled()
  await expect(share).toBeEnabled()
  await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
  await expect(remove).toBeDisabled()
  await expect(share).toBeDisabled()
})

test.describe('touch selection', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })
  test('library checkbox remains visible after a tap', async ({ page }) => {
    await fakeApi(page)
    await page.goto('/')
    await page.getByRole('link', { name: 'Загрузки', exact: true }).tap()
    const checkbox = page.getByRole('checkbox', { name: 'Example App', exact: true })
    for (const theme of ['dark', 'light']) {
      await page.locator('html').evaluate((el, value) => {
        el.dataset.theme = value
      }, theme)
      await checkbox.tap()
      await expect(checkbox).toHaveAttribute('aria-checked', 'true')
      const svg = checkbox.locator('svg')
      await expect(svg).toBeVisible()
      await expect(checkbox).not.toHaveCSS('background-color', 'rgba(0, 0, 0, 0)')
      const colors = await checkbox.evaluate((el) => ({
        background: getComputedStyle(el).backgroundColor,
        stroke: getComputedStyle(el.querySelector('svg')!).stroke,
      }))
      expect(colors.stroke).not.toBe(colors.background)
      const iconBox = await svg.boundingBox(),
        controlBox = await checkbox.boundingBox()
      expect(iconBox!.width).toBeGreaterThan(0)
      expect(iconBox!.x).toBeGreaterThanOrEqual(controlBox!.x)
      expect(iconBox!.x + iconBox!.width).toBeLessThanOrEqual(controlBox!.x + controlBox!.width)
      await checkbox.tap()
      await expect(checkbox).toHaveAttribute('aria-checked', 'false')
    }
  })
})

test('sharing waits for selected unfinished apps, including other pages', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 900 })
  const control = await fakeApi(page)
  control.setState({ downloadsActive: true })
  let finished = false
  const readyId = '01900000-0000-7000-8000-000000000001',
    queuedId = '01900000-0000-7000-8000-000000000002'
  const selectionChecks: string[][] = []
  await page.route('**/api/library/selection', (route) => {
    const input = route.request().postDataJSON()
    const ids: string[] = input.ids?.length ? input.ids : [readyId, queuedId]
    if (input.ids?.length) selectionChecks.push(input.ids)
    return route.fulfill({
      json: {
        ids,
        readyIds: ids.filter((id) => id === readyId || finished),
        manageIds: ids,
      } satisfies LibrarySelectionResult,
    })
  })
  await page.goto('/')
  await page.getByRole('link', { name: 'Загрузки', exact: true }).click()
  const share = page.getByRole('button', { name: /^Раздать/ })
  await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
  await expect(share).toBeEnabled()
  await page.getByRole('button', { name: 'Ещё', exact: true }).click()
  await page.getByRole('button', { name: 'Отметить все', exact: true }).click()
  await expect(share).toContainText('(2)')
  await expect(share).toBeDisabled()
  await expect(page.getByRole('table').locator('tbody').getByRole('checkbox')).toHaveCount(1)
  finished = true
  await expect(share).toBeEnabled()
  expect(selectionChecks).toContainEqual([queuedId])
})

test('share becomes available when the selected transfer finishes', async ({ page }) => {
  await fakeApi(page)
  let ready = false
  await page.route('**/api/library?**', (route) =>
    route.fulfill({
      json: {
        items: [
          {
            id: '01900000-0000-7000-8000-000000000001',
            appId: '1001',
            name: 'Example App',
            status: ready ? 'done' : 'downloading',
            version: ready ? '1.0' : '',
            minimumOs: '',
            size: 10_000,
            received: ready ? 10_000 : 5_000,
            total: 10_000,
            speed: ready ? 0 : 1_000,
            error: '',
            createdAt: '2026-10-25T10:00:00Z',
            canManage: true,
            icon: '',
          } satisfies LibraryItem,
        ],
        total: 1,
        next: false,
        previous: false,
      },
    }),
  )
  await page.goto('/')
  await page.getByRole('link', { name: 'Загрузки', exact: true }).click()
  await page.getByRole('checkbox', { name: 'Example App', exact: true }).click()
  const share = page.getByRole('button', { name: /^Раздать/ })
  await expect(share).toBeDisabled()
  ready = true
  await expect(share).toBeEnabled()
})

test('download starts empty before the total size is known', async ({ page }) => {
  await fakeApi(page)
  let received = 0,
    total = 0
  await page.route('**/api/library?**', (route) =>
    route.fulfill({
      json: {
        items: [
          {
            id: '01900000-0000-7000-8000-000000000001',
            appId: '1001',
            name: 'Example App',
            status: 'downloading',
            received,
            total,
            speed: 0,
            error: '',
            version: '',
            minimumOs: '',
            size: 0,
            createdAt: '2026-10-25T10:00:00Z',
            icon: '',
            canManage: true,
          } satisfies LibraryItem,
        ],
        total: 1,
        next: false,
        previous: false,
      },
    }),
  )
  await page.goto('/downloads')
  const bar = page.getByRole('progressbar', { name: 'Example App', exact: true })
  await expect(bar).not.toHaveClass(/indeterminate/)
  await expect(bar.locator('span')).toHaveCSS('transform', 'matrix(0, 0, 0, 1, 0, 0)')
  received = 200
  total = 1_000
  await page.getByRole('columnheader', { name: 'Размер', exact: true }).getByRole('button').click()
  await expect(bar).toHaveAttribute('aria-valuenow', '20')
  await expect(bar.locator('span')).toHaveCSS('transform', 'matrix(0.2, 0, 0, 1, 0, 0)')
})

test('delete all keeps its warning open and cancellation preserves downloads', async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 })
  await fakeApi(page)
  let removals = 0
  await page.route('**/api/library/delete', (route) => {
    removals++
    return route.fulfill({ status: 204 })
  })
  await page.goto('/downloads')

  async function open() {
    await page.getByRole('button', { name: 'Ещё', exact: true }).click()
    await page.getByRole('button', { name: 'Удалить всё', exact: true }).click()
  }

  await open()
  const dialog = page.getByRole('dialog', {
    name: 'Удалить всё в разделе «Загрузки»?',
    exact: true,
  })
  await expect(dialog).toContainText('Файлы будут удалены с этого сервера')
  expect(removals).toBe(0)
  await dialog.getByRole('button', { name: 'Отмена', exact: true }).click()
  await page.waitForFunction(() => !history.state?.modal)
  expect(removals).toBe(0)
  await expect(page.getByRole('checkbox', { name: 'Example App', exact: true })).toBeVisible()
  await open()
  await dialog.getByRole('button', { name: 'Удалить', exact: true }).click()
  await expect(dialog).toHaveCount(0)
  expect(removals).toBe(1)
})

for (const width of [320, 1_280]) {
  test(`IPA import button works without Apple sign-in at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 812 })
    await fakeApi(page, false)
    let imports = 0
    let reads = 0
    await page.route('**/api/library?*', async (route) => {
      reads++
      await route.fulfill({ json: { items: [], total: imports, next: false, previous: false } })
    })
    await page.route('**/api/library/imports/*', async (route) => {
      const request = route.request()
      expect(request.method()).toBe('PUT')
      expect(request.url()).toMatch(/\/imports\/[a-f0-9-]{36}$/)
      expect(request.headers()['x-csrf-token']).toBe('test-token')
      expect(request.headers()['x-session-key']).toBeUndefined()
      expect(request.headers()['content-disposition']).toBe(
        "attachment; filename*=UTF-8''existing.ipa",
      )
      expect(request.postData()).toBe('synthetic IPA')
      imports++
      await route.fulfill({ status: 201, json: { id: 'imported' } })
    })
    await page.goto('/downloads')
    const button = page.getByRole('button', { name: 'Импорт .ipa', exact: true })
    await expect(button).toHaveCount(0)
    await page.getByRole('button', { name: 'Ещё', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Экспорт .ipa', exact: true })).toBeDisabled()
    await expect(button).toBeEnabled()
    await expect(page.getByText('Выберите файлы .ipa. До 4 ГиБ на файл.')).toHaveCount(0)
    await button.click()
    const dialog = page.getByRole('dialog', { name: 'Ручной импорт IPA', exact: true })
    await expect(dialog).toBeVisible()
    await expect(dialog).toContainText('Выберите файлы .ipa. До 4 ГиБ на файл.')
    await dialog.getByRole('button', { name: 'Отмена', exact: true }).click()
    await expect(dialog).toHaveCount(0)
    expect(imports).toBe(0)
    await page.getByRole('button', { name: 'Ещё', exact: true }).click()
    await button.click()
    const chooser = page.waitForEvent('filechooser')
    await dialog.getByRole('button', { name: 'Выбрать файлы', exact: true }).click()
    await (
      await chooser
    ).setFiles({
      name: 'existing.ipa',
      mimeType: 'application/octet-stream',
      buffer: Buffer.from('synthetic IPA'),
    })
    await expect(page.locator('input[type=file]')).toBeEnabled()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(dialog).toHaveCount(0)
    expect(imports).toBe(1)
    expect(reads).toBeGreaterThanOrEqual(2)
    expect(await page.locator('input[type=file]').inputValue()).toBe('')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  })
}

test('IPA drag and drop preserves successful files and reports each rejected file', async ({
  page,
}) => {
  await fakeApi(page, false)
  const names: string[] = []
  await page.route('**/api/library/imports/*', async (route) => {
    const body = route.request().headers()['content-disposition'] ?? ''
    names.push(body)
    await new Promise((resolve) => setTimeout(resolve, 100))
    await route.fulfill(
      body.includes('broken.ipa')
        ? { status: 400, json: { code: 'invalid_ipa' } }
        : { status: 201, json: { id: 'imported' } },
    )
  })
  await page.goto('/downloads')
  const section = page.getByRole('region', { name: 'Загрузки', exact: true })
  const transfer = await page.evaluateHandle(() => {
    const data = new DataTransfer()
    for (const name of ['valid.ipa', 'notes.txt', 'broken.ipa', 'another.IPA']) {
      data.items.add(new File(['synthetic IPA'], name, { type: 'application/octet-stream' }))
    }
    return data
  })
  await section.dispatchEvent('dragenter', { dataTransfer: transfer })
  await section.locator('.toolbar').dispatchEvent('dragenter', { dataTransfer: transfer })
  await section.locator('.toolbar').dispatchEvent('dragleave', { dataTransfer: transfer })
  await expect(section.locator('.import-drop-overlay')).toBeVisible()
  await section.dispatchEvent('drop', { dataTransfer: transfer })
  await expect(page.locator('input[type=file]')).toBeDisabled()
  await expect(section.locator('.import-drop-overlay')).toHaveCount(0)
  const failure = page.getByRole('dialog', { name: 'Действие не выполнено', exact: true })
  await expect(failure.getByRole('alert')).toContainText('notes.txt:')
  await expect(failure.getByRole('alert')).toContainText('broken.ipa:')
  await expect(page.locator('input[type=file]')).toBeEnabled()
  await expect(page.locator('.notice')).toHaveCount(0)
  expect(names).toHaveLength(3)
  await failure.getByRole('button', { name: 'Понятно', exact: true }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
})

// Holds the browser upload so a test can drive its events like a real XHR does.
async function controlBrowserUpload(page: Page) {
  await page.addInitScript(() => {
    const state = window as unknown as {
      testUpload: XMLHttpRequest
      testUploadId: string
      testUploadAborted: boolean
    }
    class ControlledUpload extends XMLHttpRequest {
      // The upload's id is the last segment of its address.
      open(method: string, url: string | URL) {
        state.testUploadId = String(url).split('/').at(-1)!
        super.open(method, url)
      }

      send() {
        state.testUpload = this
      }

      abort() {
        state.testUploadAborted = true
        this.dispatchEvent(new Event('abort'))
      }
    }
    window.XMLHttpRequest = ControlledUpload
  })
}

function uploadProgress(page: Page, loaded: number, total = 1_000) {
  return page.evaluate(
    ({ loaded, total }) =>
      (window as unknown as { testUpload: XMLHttpRequest }).testUpload.upload.dispatchEvent(
        new ProgressEvent('progress', { loaded, total, lengthComputable: true }),
      ),
    { loaded, total },
  )
}

function uploadSent(page: Page) {
  return page.evaluate(() => {
    const { upload } = (window as unknown as { testUpload: XMLHttpRequest }).testUpload
    for (const type of ['load', 'loadend']) upload.dispatchEvent(new ProgressEvent(type))
  })
}

function uploadAccepted(page: Page) {
  return page.evaluate(() => {
    const state = window as unknown as { testUpload: XMLHttpRequest; testUploadId: string }
    const xhr = state.testUpload
    Object.defineProperty(xhr, 'readyState', { value: 4 })
    Object.defineProperty(xhr, 'status', { value: 201 })
    Object.defineProperty(xhr, 'responseText', {
      value: JSON.stringify({ id: state.testUploadId }),
    })
    for (const type of ['load', 'loadend']) xhr.dispatchEvent(new ProgressEvent(type))
  })
}

for (const width of [375, 1_280]) {
  test(`manual IPA transfer uses browser progress through a buffering proxy at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 850 })
    await fakeApi(page, false)
    await controlBrowserUpload(page)
    let serverStarted = false,
      ready = false,
      uploadId = ''
    const base: LibraryItem = {
      id: '01900000-0000-7000-8000-000000000001',
      appId: '1001',
      name: 'Catalog App',
      status: 'downloading',
      received: 250,
      total: 1_000,
      speed: 500,
      error: '',
      version: '',
      minimumOs: '',
      size: 1_000,
      createdAt: '2026-11-07T12:00:00Z',
      canManage: true,
      icon: '',
    }
    await page.route('**/api/library?*', (route) => {
      const manual: LibraryItem = {
        ...base,
        id: uploadId,
        name: ready ? 'Imported App' : 'existing.ipa',
        status: ready ? 'done' : 'downloading',
        received: ready ? 1_000 : 0,
        speed: 0,
      }
      const items = serverStarted || ready ? [manual, base] : [base]
      return route.fulfill({ json: { items, total: items.length, next: false, previous: false } })
    })
    await page.goto('/downloads')
    await page.locator('input[type=file]').setInputFiles({
      name: 'existing.ipa',
      mimeType: 'application/octet-stream',
      buffer: Buffer.from('x'.repeat(1_000)),
    })
    uploadId = await page.evaluate(
      () => (window as unknown as { testUploadId: string }).testUploadId,
    )
    expect(uploadId).toMatch(/^[a-f0-9-]{36}$/)
    const manualRow = page
      .getByRole('row')
      .filter({ has: page.getByText('existing.ipa', { exact: true }) })
    await expect(manualRow.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '0')
    await uploadProgress(page, 250)
    await expect(manualRow.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '25')
    await expect(manualRow.locator('.transfer-speed')).toBeVisible()
    await expect(manualRow.getByText('Загружается', { exact: true })).toBeVisible()
    await manualRow.getByRole('checkbox').click()
    await expect(page.getByRole('button', { name: 'Раздать', exact: true })).toBeDisabled()
    // The server starts receiving later, with smaller counters. Polling must not
    // duplicate the upload row or replace the browser's transfer measurements.
    const polled = page.waitForResponse((response) => response.url().includes('/api/library?'))
    serverStarted = true
    await polled
    await expect(manualRow).toHaveCount(1)
    await expect(manualRow.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '25')
    await uploadProgress(page, 750)
    await expect(manualRow.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '75')
    await uploadSent(page)
    await expect(manualRow.getByRole('progressbar')).toHaveClass(/indeterminate/)
    await expect(manualRow.getByText('Подготовка', { exact: true })).toBeVisible()
    await expect(manualRow.locator('.transfer-speed')).toHaveCount(0)
    await expect(page.getByRole('status')).toHaveCount(0)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    ready = true
    await uploadAccepted(page)
    const done = page
      .getByRole('row')
      .filter({ has: page.getByText('Imported App', { exact: true }) })
    await expect(done.getByText('Доступно к раздаче', { exact: true })).toBeVisible()
    await expect(done.getByRole('progressbar')).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'Раздать', exact: true })).toBeEnabled()
    await page.getByRole('button', { name: 'Ещё', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Импорт .ipa', exact: true })).toBeEnabled()
  })
}

for (const width of [375, 1_280]) {
  test(`completed IPA import stays visible after paging back at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 850 })
    await fakeApi(page, false)
    await controlBrowserUpload(page)
    let ready = false,
      uploadId = ''
    const base: LibraryItem = {
      id: '01900000-0000-7000-8000-000000000001',
      appId: '1001',
      name: 'Existing App',
      status: 'done',
      received: 1_000,
      total: 1_000,
      speed: 0,
      error: '',
      version: '1.0',
      minimumOs: '',
      size: 1_000,
      createdAt: '2026-11-07T12:00:00Z',
      canManage: true,
      icon: '',
    }
    await page.route('**/api/library?*', (route) => {
      const cursor = new URL(route.request().url()).searchParams.get('cursor')
      const items = [{ ...base, cursor: cursor === 'older' ? 'back-to-first' : 'older' }]
      if (ready && !cursor) items.unshift({ ...base, id: uploadId, name: 'Imported App' })
      return route.fulfill({
        json: {
          items,
          total: ready ? 3 : 2,
          next: cursor !== 'older',
          previous: cursor === 'older',
        },
      })
    })
    await page.goto('/downloads')
    await page.getByRole('button', { name: 'Вперёд', exact: true }).click()
    await page.getByRole('button', { name: 'Назад', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Назад', exact: true })).toBeDisabled()
    await page.locator('input[type=file]').setInputFiles({
      name: 'existing.ipa',
      mimeType: 'application/octet-stream',
      buffer: Buffer.from('synthetic IPA'),
    })
    uploadId = await page.evaluate(
      () => (window as unknown as { testUploadId: string }).testUploadId,
    )
    await uploadProgress(page, 1_000)
    await uploadSent(page)
    await expect(page.getByText('Подготовка', { exact: true })).toBeVisible()
    ready = true
    await uploadAccepted(page)
    await expect(page.locator('input[type=file]')).toBeEnabled()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(page.getByText('Imported App', { exact: true })).toBeVisible()
    await expect(page.getByText('existing.ipa', { exact: true })).toHaveCount(0)
    await page.getByRole('checkbox', { name: 'Imported App', exact: true }).click()
    await expect(page.getByRole('button', { name: 'Раздать', exact: true })).toBeEnabled()
    await expect(page.getByRole('button', { name: 'Назад', exact: true })).toBeDisabled()
  })
}

test('manual IPA transfer cancels in the browser before a buffering proxy reaches the server', async ({
  page,
}) => {
  await fakeApi(page, false)
  await controlBrowserUpload(page)
  await page.route('**/api/library?*', (route) =>
    route.fulfill({ json: { items: [], total: 0, next: false, previous: false } }),
  )
  let cancellations = 0
  await page.route('**/api/downloads/*/cancel', async (route) => {
    cancellations++
    await route.fulfill({ status: 404, json: { code: 'not_found' } })
  })
  await page.goto('/downloads')
  await page.locator('input[type=file]').setInputFiles({
    name: 'cancel.ipa',
    mimeType: 'application/octet-stream',
    buffer: Buffer.from('synthetic IPA'),
  })
  await page.getByRole('checkbox', { name: 'cancel.ipa', exact: true }).click()
  await page.getByRole('button', { name: 'Отменить загрузку — cancel.ipa', exact: true }).click()
  await expect(page.getByText('cancel.ipa', { exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Ещё', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Импорт .ipa', exact: true })).toBeEnabled()
  await expect(page.getByRole('alert')).toHaveCount(0)
  expect(
    await page.evaluate(
      () => (window as unknown as { testUploadAborted: boolean }).testUploadAborted,
    ),
  ).toBe(true)
  expect(cancellations).toBe(1)
})

test('shared library only mine switch filters on the server and survives reload', async ({
  page,
}) => {
  await page.setViewportSize({ width: 375, height: 900 })
  await publicPreview(page)
  const api = await fakeApi(page)
  api.setState({ mode: { public: true, sharedLibrary: true } })
  const ownId = '01900000-0000-7000-8000-000000000001'
  const foreignId = '01900000-0000-7000-8000-000000000002'
  const items = [
    { id: ownId, appId: '1001', name: 'My App', canManage: true },
    { id: foreignId, appId: '1002', name: 'Shared App', canManage: false },
  ].map((item) => ({
    ...item,
    status: 'done',
    received: 100,
    total: 100,
    size: 100,
    speed: 0,
    error: '',
    version: '1.0',
    minimumOs: '',
    icon: '',
    createdAt: '',
  }))
  const filters: string[] = []
  await page.route('**/api/library?**', (route) => {
    const mine = new URL(route.request().url()).searchParams.get('mine')!
    filters.push(mine)
    const visible = mine === 'true' ? items.slice(0, 1) : items
    return route.fulfill({
      json: { items: visible, total: visible.length, next: false, previous: false },
    })
  })
  let selection: { filter?: { mine?: boolean } } = {}
  await page.route('**/api/library/selection', (route) => {
    selection = route.request().postDataJSON()
    const ids = selection.filter?.mine ? [ownId] : [ownId, foreignId]
    return route.fulfill({ json: { ids, readyIds: ids, manageIds: [ownId] } })
  })
  await page.goto('https://appleseum.test/downloads')
  await expect(page.getByText('My App', { exact: true })).toBeVisible()
  await expect(page.getByText('Shared App', { exact: true })).toHaveCount(0)
  expect(filters.at(-1)).toBe('true')
  await page.getByRole('button', { name: 'Ещё', exact: true }).click()
  const toggle = page.getByRole('switch', { name: 'Только мои', exact: true })
  await expect(toggle).toHaveAttribute('aria-checked', 'true')
  const saved = page.waitForResponse(
    (r) => r.url().endsWith('/api/table-settings/library') && r.request().method() === 'PUT',
  )
  await toggle.click()
  await saved
  await expect(toggle).toHaveAttribute('aria-checked', 'false')
  await expect(page.getByText('Shared App', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Отметить все', exact: true }).click()
  await expect.poll(() => selection.filter?.mine).toBe(false)
  await expect(page.getByRole('checkbox', { name: 'Shared App', exact: true })).toHaveAttribute(
    'aria-checked',
    'true',
  )
  await page.reload()
  await expect(page.getByText('Shared App', { exact: true })).toBeVisible()
  expect(filters.at(-1)).toBe('false')
  await page.getByRole('button', { name: 'Ещё', exact: true }).click()
  await expect(toggle).toHaveAttribute('aria-checked', 'false')
  await toggle.click()
  await expect(page.getByText('Shared App', { exact: true })).toHaveCount(0)
  await page.getByRole('button', { name: 'Отметить все', exact: true }).click()
  await expect.poll(() => selection.filter?.mine).toBe(true)
})

for (const state of [
  { public: false, sharedLibrary: false, service: false },
  { public: true, sharedLibrary: false, service: false },
  { public: true, sharedLibrary: true, service: true },
]) {
  test(`only mine switch is hidden outside shared user library: ${JSON.stringify(state)}`, async ({
    page,
  }) => {
    if (state.public) await publicPreview(page)
    const api = await fakeApi(page)
    api.setState({ mode: state })
    await page.goto(state.public ? 'https://appleseum.test/downloads' : '/downloads')
    await page.getByRole('button', { name: 'Ещё', exact: true }).click()
    await expect(page.getByRole('switch', { name: 'Только мои', exact: true })).toHaveCount(0)
  })
}

for (const width of [375, 1_280]) {
  for (const resource of ['catalog', 'library', 'shares', 'debug'] as const) {
    test(`${resource} selects all filtered IDs across pages without fetching rows at ${width}px`, async ({
      page,
    }) => {
      await page.setViewportSize({ width, height: 900 })
      await page.clock.install()
      await page.addInitScript(() => {
        for (const resource of ['catalog', 'library', 'shares', 'debug'])
          localStorage.setItem('appleseum.pageSize.' + resource, '2')
      })
      const control = await fakeApi(page, true, resource === 'debug')
      if (resource === 'shares') control.setState({ shareCount: 27, mode: { service: true } })
      const items = Array.from({ length: 27 }, (_, i) => ({
        id:
          resource === 'debug'
            ? i + 1
            : resource === 'catalog'
              ? String(i + 1)
              : `01900000-0000-7000-8000-${String(i + 1).padStart(12, '0')}`,
        appId: String(i + 1),
        name: `App ${String(i + 1).padStart(2, '0')}`,
        icon: '',
        status: resource === 'debug' ? 200 : 'done',
        canManage: true,
        received: 100,
        total: 100,
        speed: 0,
        size: 100,
        version: '1.0',
        minimumOs: '',
        error: '',
        createdAt: '2026-11-10T12:00:00Z',
        delisted: true,
        inLibrary: resource === 'catalog' && i === 1,
        purchasedAt: '',
        owner: '',
        count: 1,
        url: 'about:blank#fixture',
        at: '2026-11-10T12:00:00Z',
        source: 'API',
        method: 'GET',
        target: `/fixture/App ${i + 1}`,
        durationMs: 1,
        detail: '',
        hasResponse: i % 2 === 0,
        responseBytes: 10,
        responseTruncated: false,
      }))
      const eligible = items.filter((item) =>
        resource === 'debug' ? item.hasResponse : !item.inLibrary,
      )
      let rejectSelection = false
      const reads: string[] = [],
        selections: unknown[] = []
      await page.route(`**/api/${resource}?*`, (route) => {
        const query = new URL(route.request().url()).searchParams
        reads.push(query.get('cursor') ?? '')
        const boundary = Number(query.get('cursor') || 0)
        const start =
          query.get('move') === 'prev'
            ? Math.max(0, boundary - 1 - Number(query.get('limit')))
            : boundary
        const limit = Number(query.get('limit'))
        return route.fulfill({
          json: {
            items: items
              .slice(start, start + limit)
              .map((row, i) => ({ ...row, cursor: String(start + i + 1) })),
            total: items.length,
            next: start + limit < items.length,
            previous: start > 0,
          },
        })
      })
      if (resource === 'debug') {
        await page.route('**/api/debug/info', (route) =>
          route.fulfill({
            json: {
              executable: '/fixture/appleseum',
              arguments: [],
              startedAt: '',
              uptimeSeconds: 1,
              version: '',
              runtime: '',
              heapBytes: 100,
              goroutines: 1,
              capacity: 1_000,
            },
          }),
        )
        await page.route('**/api/debug/selection?*', (route) => {
          selections.push(new URL(route.request().url()).searchParams.get('q'))
          if (rejectSelection)
            return route.fulfill({ status: 400, json: { code: 'selection_too_large' } })
          return route.fulfill({ json: { ids: eligible.map((item) => item.id) } })
        })
      } else {
        await page.route(`**/api/${resource}/selection`, (route) => {
          const input = route.request().postDataJSON()
          if (input.ids?.length) {
            const ids = eligible
              .filter((item) => input.ids.includes(item.id))
              .map((item) => item.id)
            return route.fulfill({ json: { ids, readyIds: ids, manageIds: ids } })
          }
          selections.push(input)
          if (rejectSelection)
            return route.fulfill({ status: 400, json: { code: 'selection_too_large' } })
          const ids = eligible.map((item) => item.id)
          return route.fulfill({
            json: { ids, ...(resource === 'library' ? { readyIds: ids, manageIds: ids } : {}) },
          })
        })
      }
      await page.goto(
        resource === 'catalog' ? '/available' : resource === 'debug' ? '/debug' : '/downloads',
      )
      if (resource === 'shares') await page.getByRole('button', { name: /^Раздачи/ }).click()
      const table =
        resource === 'catalog'
          ? page.locator('.data-table').first()
          : page
              .locator('.data-table')
              .filter({
                has: page.locator(
                  resource === 'library'
                    ? '.downloads-table'
                    : resource === 'shares'
                      ? '.shares-table'
                      : '.debug-table',
                ),
              })
              .first()
      const searchLabel =
        resource === 'shares'
          ? 'Поиск раздач'
          : resource === 'debug'
            ? 'Поиск событий'
            : 'Поиск приложений'
      await page.getByRole('searchbox', { name: searchLabel, exact: true }).fill('App')
      await page.clock.runFor(350)
      await expect(table.locator('tbody tr')).toHaveCount(2)
      const header = table.locator('thead [role=checkbox]')

      async function chooseAll(clear = false) {
        if (width > 600) await header.click()
        else {
          await table.getByRole('button', { name: 'Ещё', exact: true }).click()
          await page
            .getByRole('dialog')
            .getByRole('button', {
              name: clear
                ? 'Снять все'
                : resource === 'debug'
                  ? 'Отметить все'
                  : 'Отметить найденные',
              exact: true,
            })
            .click()
        }
      }

      await chooseAll()
      await expect(header).toHaveAttribute('aria-checked', 'true')
      expect(selections).toHaveLength(1)
      if (resource === 'debug') expect(selections[0]).toBe('App')
      else
        expect(selections[0]).toMatchObject({
          filter: {
            query: 'App',
            ...(resource === 'shares'
              ? {}
              : { delisted: resource === 'catalog', mine: resource === 'library' }),
          },
        })
      const action =
        resource === 'debug'
          ? table.getByRole('button', { name: /^Скачать HAR/ })
          : resource === 'shares'
            ? table.getByRole('button', { name: 'Остановить', exact: true })
            : table.getByRole('button', { name: resource === 'catalog' ? /^Скачать/ : /^Раздать/ })
      await expect(action).toBeEnabled()
      if (resource !== 'shares') await expect(action).toContainText(`(${eligible.length})`)
      // All records fit many pages, but selection reads only IDs and no distant page.
      expect(reads.every((cursor) => cursor === '' || cursor === '2')).toBe(true)
      await table.locator('tbody [role=checkbox]').first().click()
      await expect(header).toHaveAttribute('aria-checked', 'mixed')
      await table.getByRole('button', { name: 'Вперёд', exact: true }).click()
      await expect(table.locator('tbody [role=checkbox]').first()).toHaveAttribute(
        'aria-checked',
        'true',
      )
      await expect(header).toHaveAttribute('aria-checked', 'mixed')
      await chooseAll()
      await expect(header).toHaveAttribute('aria-checked', 'true')
      expect(selections).toHaveLength(2)
      await chooseAll(true)
      await expect(header).toHaveAttribute('aria-checked', 'false')
      await expect(action).toBeDisabled()
      await table.getByRole('button', { name: 'Назад', exact: true }).click()
      await expect(table.locator('tbody [role=checkbox]').first()).toHaveAttribute(
        'aria-checked',
        'false',
      )
      expect(reads.every((cursor) => ['', '0', '2', '3', '4'].includes(cursor))).toBe(true)
      expect(selections).toHaveLength(2)
      await table.locator('tbody [role=checkbox]').first().click()
      rejectSelection = true
      await chooseAll()
      await expect(page.getByRole('alert')).toContainText('512')
      await expect(table.locator('tbody [role=checkbox]').first()).toHaveAttribute(
        'aria-checked',
        'true',
      )
      expect(selections).toHaveLength(3)
      expect(reads.every((cursor) => ['', '0', '2', '3', '4'].includes(cursor))).toBe(true)
    })
  }
}
