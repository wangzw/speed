/**
 * SessionsPage in a real browser: the session table at the sm/md/lg
 * floor widths with fixed data. The page lists sessions only for a
 * chosen account, so the case drives the account picker the way an
 * operator does -- type, wait for the option, choose it -- rather than
 * reaching into page state.
 */
import { describe, it } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import { SessionsPage } from './SessionsPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import { chooseAccount } from '../test-utils/account-selector.js'
import {
  accountFixture,
  createAdminOperations,
  makeAdminBackend,
} from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import type { AdminAccountSession } from './operations.js'
import enUS from './locales/en-US.json' with { type: 'json' }

const FLOOR_WIDTHS = [600, 900, 1200] as const

const ACCOUNT = accountFixture({
  id: 'acc-9',
  display_name: 'Ada',
})

const SESSIONS: readonly AdminAccountSession[] = [
  {
    id: 'session-identifier-with-a-long-value-0001',
    kind: 'refresh',
    assurance: 2,
    auth_time: '2026-10-15T12:00:00Z',
    created_at: '2026-10-15T12:00:00Z',
    last_seen_at: '2026-10-15T12:30:00Z',
    expires_at: '2026-10-16T12:00:00Z',
    revoked: false,
    current: false,
  },
]

describe('SessionsPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the session table inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)

      const operations = createAdminOperations(
        makeAdminBackend({
          listAccounts: () =>
            Promise.resolve({ accounts: [ACCOUNT], next_cursor: '' }),
          listAccountSessions: () => Promise.resolve(SESSIONS),
        }),
      )
      renderWithProviders(<SessionsPage />, { operations, language: 'en-US' })

      const user = userEvent.setup()
      await chooseAccount(
        user,
        enUS.sessions.accountLabel,
        'Ada',
        'Ada (acc-9)',
      )

      await expectNoHorizontalOverflow()
    })
  }
})
