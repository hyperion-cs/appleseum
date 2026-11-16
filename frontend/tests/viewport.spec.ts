import type { Page } from '@playwright/test'
import { test, expect } from './fixtures.ts'

const bar = (page: Page) => page.locator('.persistent-scrollbar.vertical').first()

async function overflow(page: Page) {
  await page.goto('/downloads?demo')
  await page.locator('#app').evaluate((app) => {
    app.style.height = '300px'
  })
  await expect(bar(page)).toBeVisible()
}

async function metrics(page: Page) {
  return bar(page).evaluate((element) => {
    const rect = element.getBoundingClientRect(),
      view = visualViewport!
    return {
      width: rect.width * view.scale,
      right: (view.offsetLeft + view.width - rect.right) * view.scale,
      scale: view.scale,
      left: view.offsetLeft,
    }
  })
}

for (const width of [390, 1_280]) {
  test(`pinch policy preserves scrolling in both themes and languages at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 844 })
    await overflow(page)
    for (const russian of [false, true]) {
      if (russian) await page.getByRole('button', { name: 'Русский', exact: true }).click()
      for (const light of [false, true]) {
        const theme = light ? 'light' : 'dark'
        if ((await page.locator('html').getAttribute('data-theme')) !== theme)
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
        expect(
          await page.evaluate(() =>
            ['gesturestart', 'gesturechange'].every((type) => {
              const event = new Event(type, { cancelable: true, bubbles: true })
              document.querySelector('main')!.dispatchEvent(event)
              return event.defaultPrevented
            }),
          ),
        ).toBe(true)
        await expect(page.locator('#app')).toHaveCSS('touch-action', 'pan-x pan-y')
        await page.locator('#app').evaluate((app) => {
          app.scrollTop = 0
        })
        await page.mouse.move(20, 200)
        await page.mouse.wheel(0, 150)
        await expect
          .poll(() => page.locator('#app').evaluate((app) => app.scrollTop))
          .toBeGreaterThan(0)
      }
    }
  })
}

test('scrollbar follows a zoomed visual viewport after horizontal and vertical panning', async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await overflow(page)
  await page.evaluate(() => {
    const viewport = Object.assign(new EventTarget(), {
      scale: 2,
      offsetLeft: 0,
      offsetTop: 0,
      width: 195,
      height: 250,
    })
    // Forward events to the already installed viewport listeners.
    const original = window.visualViewport!
    viewport.addEventListener('scroll', () => original.dispatchEvent(new Event('scroll')))
    viewport.addEventListener('resize', () => original.dispatchEvent(new Event('resize')))
    Object.defineProperty(window, 'visualViewport', { configurable: true, value: viewport })
    viewport.dispatchEvent(new Event('resize'))
  })
  for (const [left, top] of [
    [0, 0],
    [80, 40],
    [150, 20],
    [0, 0],
  ]) {
    await page.evaluate(
      ([offsetLeft, offsetTop]) => {
        Object.assign(visualViewport!, { offsetLeft, offsetTop })
        visualViewport!.dispatchEvent(new Event('scroll'))
      },
      [left, top],
    )
    await expect.poll(async () => Math.abs((await metrics(page)).right - 1)).toBeLessThan(0.1)
    expect((await metrics(page)).width).toBeCloseTo(10, 1)
  }
})

test.describe('native mobile pinch', () => {
  test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true })
  test('pinch is blocked and externally forced zoom keeps the scrollbar at the visible edge', async ({
    page,
    browserName,
  }) => {
    test.skip(browserName !== 'chromium', 'CDP controls native zoom only in Chromium')
    await overflow(page)
    const client = await page.context().newCDPSession(page)
    await client.send('Input.synthesizePinchGesture', {
      x: 180,
      y: 200,
      scaleFactor: 2,
      gestureSourceType: 'touch',
    })
    expect((await metrics(page)).scale).toBe(1)
    // Force zoom independently of the page's gesture policy to exercise the layout fix.
    await client.send('Emulation.setPageScaleFactor', { pageScaleFactor: 2 })
    await expect.poll(async () => (await metrics(page)).scale).toBe(2)
    for (const left of [330, 0, 330]) {
      await page.evaluate((left) => {
        document.getElementById('zoom-focus')?.remove()
        const input = document.createElement('input')
        input.id = 'zoom-focus'
        Object.assign(input.style, {
          position: 'absolute',
          left: left + 'px',
          top: '250px',
          width: '30px',
          height: '20px',
          fontSize: '16px',
        })
        document.body.append(input)
        input.focus()
      }, left)
      await page.locator('#zoom-focus').scrollIntoViewIfNeeded()
      await expect.poll(async () => (await metrics(page)).left).toBe(left ? 195 : 0)
      await expect.poll(async () => Math.abs((await metrics(page)).right - 1)).toBeLessThan(0.1)
      expect((await metrics(page)).width).toBeCloseTo(10, 1)
    }
  })
})
