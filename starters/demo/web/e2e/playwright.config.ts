import { defineConfig } from '@playwright/test'

import { ARTIFACT_ROOT } from './fixtures/paths'
import { scaledTimeout } from './fixtures/time-scale'

export default defineConfig({
  testDir: './specs',
  globalSetup: './global-setup.ts',
  timeout: scaledTimeout(180_000),
  expect: {
    timeout: scaledTimeout(30_000),
  },
  fullyParallel: false,
  outputDir: `${ARTIFACT_ROOT}/artifacts`,
  reporter: [
    ['list'],
    [
      'html',
      { outputFolder: `${ARTIFACT_ROOT}/report`, open: 'never' },
    ],
    // Last so every reporter's onEnd (the HTML report's included) has finished; its work is in onExit.
    ['./fixtures/retention-reporter.ts'],
  ],
  use: {
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    locale: 'en-US',
    timezoneId: 'UTC',
    // The first paint follows the device scheme; pin light so it is the same on every machine.
    colorScheme: 'light',
  },
  projects: [
    { name: 'chromium', use: { browserName: 'chromium' } },
    {
      name: 'webkit',
      use: { browserName: 'webkit' },
      testMatch: /journey-password-reset-email\.spec\.ts$/,
    },
  ],
})
