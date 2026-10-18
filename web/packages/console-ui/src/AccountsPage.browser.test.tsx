/**
 * AccountsPage in a real browser: the account table at the sm/md/lg
 * floor widths with fixed data. The columns shed by priority and cap the
 * identity cell, so the table must stay inside both the page and its own
 * scroll container.
 */
import { describe, it } from 'vitest'
import { screen } from '@testing-library/react'
import { page } from 'vitest/browser'
import { AccountsPage } from './AccountsPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  accountFixture,
  createConsoleOperations,
  makeConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'

const FLOOR_WIDTHS = [600, 900, 1200] as const

const ACCOUNTS = [
  accountFixture({
    id: 'account-identifier-with-a-long-value-0001',
    display_name: 'Ada Lovelace',
  }),
  accountFixture({
    id: 'account-identifier-with-a-long-value-0002',
    display_name: 'Grace Hopper',
    status: 'disabled',
  }),
]

describe('AccountsPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the account table inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      const operations = createConsoleOperations(
        makeConsoleBackend({
          listAccounts: () =>
            Promise.resolve({ accounts: ACCOUNTS, next_cursor: '' }),
        }),
      )
      renderWithProviders(<AccountsPage />, { operations, language: 'en-US' })

      await screen.findByText('Ada Lovelace')
      await expectNoHorizontalOverflow()
    })
  }
})
