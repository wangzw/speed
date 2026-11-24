import { describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
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
import type {
  AdminAccountSession,
  AdminRevokeSessionInput,
} from './operations.js'
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

function deferred<T>(): {
  readonly promise: Promise<T>
  readonly resolve: (value: T) => void
} {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

function sessionRow(id: string): AdminAccountSession {
  return {
    id,
    kind: 'refresh',
    assurance: 2,
    auth_time: '2026-10-15T12:00:00Z',
    created_at: '2026-10-15T12:00:00Z',
    last_seen_at: '2026-10-15T12:30:00Z',
    expires_at: '2026-10-16T12:00:00Z',
    revoked: false,
    current: false,
  }
}

function revokeName(id: string): string {
  return enUS.sessions.revokeAriaWithId.replace('{{id}}', id)
}

async function revokeRow(
  ids: readonly string[],
  target: string,
  gate?: Promise<void>,
): Promise<void> {
  let rows = ids.map(sessionRow)
  const operations = createAdminOperations(
    makeAdminBackend({
      listAccounts: () =>
        Promise.resolve({ accounts: [ACCOUNT], next_cursor: '' }),
      listAccountSessions: () => Promise.resolve(rows),
      revokeSession: (input: AdminRevokeSessionInput) => {
        rows = rows.filter((row) => row.id !== input.sessionId)
        return gate ?? Promise.resolve()
      },
    }),
  )
  renderWithProviders(<SessionsPage />, { operations, language: 'en-US' })
  const user = userEvent.setup()
  await chooseAccount(user, enUS.sessions.accountLabel, 'Ada', 'Ada')
  await screen.findByText(ids[0]!)
  await user.click(screen.getByRole('button', { name: revokeName(target) }))
  const dialog = await screen.findByRole('dialog')
  await user.click(
    within(dialog).getByRole('button', { name: enUS.common.confirm }),
  )
  await waitFor(() => {
    expect(screen.queryByRole('dialog')).toBeNull()
  })
}

describe('SessionsPage revoke focus in Chromium', () => {
  it('keeps the activating control focused, then moves to the next row', async () => {
    await page.viewport(1200, 900)
    const pending = deferred<void>()
    await revokeRow(['sess-a', 'sess-b', 'sess-c'], 'sess-b', pending.promise)

    const revoke = screen.getByRole('button', { name: revokeName('sess-b') })
    await waitFor(() => expect(revoke).toHaveAttribute('aria-busy', 'true'))
    await waitFor(() => expect(revoke).toHaveFocus())

    pending.resolve()
    await waitFor(() => expect(screen.queryByText('sess-b')).toBeNull())
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: revokeName('sess-c') }),
      ).toHaveFocus(),
    )
    expect(document.activeElement).not.toBe(document.body)
  })

  it('moves focus to the previous row when the last row is revoked', async () => {
    await page.viewport(1200, 900)
    await revokeRow(['sess-a', 'sess-b', 'sess-c'], 'sess-c')

    await waitFor(() => expect(screen.queryByText('sess-c')).toBeNull())
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: revokeName('sess-b') }),
      ).toHaveFocus(),
    )
    expect(document.activeElement).not.toBe(document.body)
  })

  it('moves focus to the section heading when the only row is revoked', async () => {
    await page.viewport(1200, 900)
    await revokeRow(['sess-a'], 'sess-a')

    await waitFor(() => expect(screen.queryByText('sess-a')).toBeNull())
    await waitFor(() => expect(document.activeElement).not.toBe(document.body))
    const heading = screen.getByRole('heading', {
      name: enUS.sessions.section.title,
    })
    expect(heading).toHaveFocus()
    expect(heading).toHaveAttribute('tabindex', '-1')
  })
})

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
      await chooseAccount(user, enUS.sessions.accountLabel, 'Ada', 'Ada')

      await expectNoHorizontalOverflow()
    })
  }
})
