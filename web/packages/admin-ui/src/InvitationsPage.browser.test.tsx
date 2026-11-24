import { afterEach, describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { page } from 'vitest/browser'
import { InvitationsPage } from './InvitationsPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createAdminOperations,
  makeAdminBackend,
  tenantFixture,
} from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import { clearTenant, seedTenant } from '../test-utils/tenant-url.js'
import type { AdminInvitation, AdminMember } from './operations.js'
import enUS from './locales/en-US.json' with { type: 'json' }

const FLOOR_WIDTHS = [600, 900, 1200] as const

const TENANTS = [tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })]

const INVITATIONS: readonly AdminInvitation[] = [
  {
    id: 'inv-1',
    tenant: 'acme',
    email: 'ada.lovelace@example.com',
    status: 'pending',
    state: 'pending',
    invited_by: 'staff-1',
    expires_at: '2099-01-01T00:00:00Z',
    accepted_at: null,
    revoked_at: null,
    last_sent_at: '2026-10-15T12:00:00Z',
    send_count: 1,
    created_at: '2026-10-15T12:00:00Z',
  },
]

const MEMBERS: readonly AdminMember[] = [
  {
    account: 'account-identifier-with-a-long-value-0001',
    tenant: 'acme',
    active: true,
    display_name: 'Member With A Long Display Name',
    username: 'ada',
    sso_exempt: false,
    source: 'direct',
    roles: [],
    joined_at: '2026-10-15T12:00:00Z',
  },
]

afterEach(clearTenant)

describe('InvitationsPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the invitation and member tables inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      seedTenant('acme')

      const operations = createAdminOperations(
        makeAdminBackend({
          listTenants: () => Promise.resolve(TENANTS),
          listInvitations: () =>
            Promise.resolve({
              invitations: [...INVITATIONS],
              next_cursor: '',
            }),
          listMembers: () => Promise.resolve(MEMBERS),
        }),
      )
      renderWithProviders(<InvitationsPage />, { operations, language: 'en-US' })

      await screen.findByText('ada.lovelace@example.com')
      await expectNoHorizontalOverflow()
    })
  }
})

describe('InvitationsPage select focus in Chromium', () => {
  it('returns focus to the lifetime select after an option is chosen', async () => {
    await page.viewport(1200, 900)
    seedTenant('acme')

    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.resolve(TENANTS),
        listInvitations: () =>
          Promise.resolve({
            invitations: [...INVITATIONS],
            next_cursor: '',
          }),
        listMembers: () => Promise.resolve(MEMBERS),
      }),
    )
    renderWithProviders(<InvitationsPage />, { operations, language: 'en-US' })

    await screen.findByText('ada.lovelace@example.com')
    const lifetime = screen.getByLabelText(enUS.invitations.issue.quickLabel)
    lifetime.focus()
    await userEvent.keyboard('{Enter}')
    await screen.findByRole('option', { name: enUS.invitations.issue.oneDay })
    await userEvent.keyboard('{Enter}')
    await waitFor(() => expect(lifetime).toHaveFocus())
  })
})
