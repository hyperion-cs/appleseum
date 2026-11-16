import { defineConfig } from '@playwright/test'
import { defaultHttpPort } from './src/api/types.ts'

const defaultPreviewUrl = `http://127.0.0.1:${defaultHttpPort}`

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  use: {
    baseURL: process.env.APPLESEUM_PREVIEW_URL ?? defaultPreviewUrl,
    screenshot: 'off',
    trace: 'off',
  },
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
    {
      name: 'webkit',
      use: {
        browserName: 'webkit',
        launchOptions: { executablePath: process.env.APPLESEUM_WEBKIT_PATH },
      },
    },
  ],
})
