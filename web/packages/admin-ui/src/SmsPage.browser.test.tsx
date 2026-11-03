/**
 * SmsPage in a real browser: the SMS template table at the sm/md/lg
 * floor widths with fixed data.
 */
import { describe, it } from 'vitest'
import { screen } from '@testing-library/react'
import { page } from 'vitest/browser'
import { SmsPage } from './SmsPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import { createAdminOperations, makeAdminBackend } from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import type { AdminSmsTemplate } from './operations.js'

const FLOOR_WIDTHS = [600, 900, 1200] as const

const TEMPLATES: readonly AdminSmsTemplate[] = [
  {
    name: 'authn-login-code',
    carrier: 'review',
    kind: 'notification',
    source: 'runtime',
    params: ['code'],
    locales: ['zh-CN', 'en-US'],
    bodies: {},
    attributes: {},
    approvals: {
      'zh-CN': { state: 'approved' },
      'en-US': { state: 'approved' },
    },
    enabled: true,
    version: 1,
    created_at: '2026-10-15T12:00:00Z',
    updated_at: '2026-10-15T12:00:00Z',
  },
]

describe('SmsPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the SMS template table inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)

      const operations = createAdminOperations(
        makeAdminBackend({
          listSmsTemplates: () =>
            Promise.resolve({ templates: [...TEMPLATES], next_cursor: '' }),
        }),
      )
      renderWithProviders(<SmsPage />, { operations, language: 'en-US' })

      await screen.findByText('authn-login-code')
      await expectNoHorizontalOverflow()
    })
  }
})
