/**
 * InvitationsPage in a real browser: the invitation and membership
 * tables at the sm/md/lg floor widths with fixed data. The tenant is
 * seeded through the query parameter the page opens on.
 */
import { afterEach, describe, it } from 'vitest'
import { screen } from '@testing-library/react'
import { page } from 'vitest/browser'
import { InvitationsPage } from './InvitationsPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import { createAdminOperations, makeAdminBackend } from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import { clearTenant, seedTenant } from '../test-utils/tenant-url.js'
import type { AdminInvitation, AdminMember } from './operations.js'

const FLOOR_WIDTHS = [600, 900, 1200] as const

const TENANTS = [{ name: 'acme', displayName: 'Acme', kind: 'business' as const, status: 'active' as const }]

const INVITATIONS: readonly AdminInvitation[] = [
  {
    id: 'inv-1',
    tenant: 'acme',
    email: 'ada.lovelace@example.com',
    status: 'pending',
    invited_by: 'staff-1',
    expires_at: '2099-01-01T00:00:00Z',
    accepted_at: '0001-01-01T00:00:00Z',
    created_at: '2026-10-15T12:00:00Z',
  },
]

const MEMBERS: readonly AdminMember[] = [
  {
    account: 'account-identifier-with-a-long-value-0001',
    tenant: 'acme',
    active: true,
    display_name: 'Member With A Long Display Name',
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
