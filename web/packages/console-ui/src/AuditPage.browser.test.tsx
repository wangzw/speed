/**
 * AuditPage in a real browser: the audit table at the sm/md/lg floor
 * widths with fixed data. The columns carry priorities and caps; the
 * table and the page must stay inside their widths.
 */
import { describe, it } from 'vitest'
import { screen } from '@testing-library/react'
import { page } from 'vitest/browser'
import { AuditPage } from './AuditPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  accountFixture,
  createConsoleOperations,
  makeConsoleBackend,
  recordFixture,
} from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import type { ConsoleAuditRecord } from './operations.js'

const FLOOR_WIDTHS = [600, 900, 1200] as const

const RECORDS: readonly ConsoleAuditRecord[] = [
  recordFixture({
    id: 'record-identifier-with-a-long-value-0001',
    action: 'authn.account.identity.add',
    object: 'account/account-identifier-with-a-long-value-0001',
  }),
  recordFixture({
    id: 'record-identifier-with-a-long-value-0002',
    action: 'tenant.membership.remove',
    outcome: 'denied',
  }),
]

describe('AuditPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the audit table inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      const operations = createConsoleOperations(
        makeConsoleBackend({
          listRecords: () =>
            Promise.resolve({ entries: [...RECORDS], next: '' }),
          getAccount: () =>
            Promise.resolve(accountFixture({ display_name: 'Ada Lovelace' })),
        }),
      )
      renderWithProviders(<AuditPage />, { operations, language: 'en-US' })

      await screen.findAllByText('Ada Lovelace')
      await expectNoHorizontalOverflow()
    })
  }
})
