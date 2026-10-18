/**
 * SettingsPage in a real browser: the settings table at the sm/md/lg
 * floor widths with fixed data (keys and values of representative
 * length, including a long dotted key).
 */
import { describe, it } from 'vitest'
import { screen } from '@testing-library/react'
import { page } from 'vitest/browser'
import { SettingsPage } from './SettingsPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import { createConsoleOperations, makeConsoleBackend } from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import type { ConsoleSetting } from './operations.js'

const FLOOR_WIDTHS = [600, 900, 1200] as const

const SETTINGS: readonly ConsoleSetting[] = [
  {
    key: 'mail.retry',
    namespace: 'mail',
    type: 'int',
    description: 'Retry count',
    domain: '',
    sensitive: false,
    default: '3',
    written: '5',
    has_row: true,
    value: '5',
  },
  {
    key: 'authn.session.absolute-lifetime',
    namespace: 'authn',
    type: 'duration',
    description: 'Absolute session lifetime',
    domain: '',
    sensitive: false,
    default: '24h',
    written: '',
    has_row: false,
    value: '24h',
  },
]

describe('SettingsPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the settings table inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      const operations = createConsoleOperations(
        makeConsoleBackend({
          listSettings: () => Promise.resolve(SETTINGS),
        }),
      )
      renderWithProviders(<SettingsPage />, { operations, language: 'en-US' })

      await screen.findByText('mail.retry')
      await expectNoHorizontalOverflow()
    })
  }
})
