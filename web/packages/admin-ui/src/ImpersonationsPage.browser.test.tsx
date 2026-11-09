/**
 * ImpersonationsPage in a real browser: the tenant's impersonation
 * record table at the sm/md/lg floor widths with fixed data.
 *
 * The page also reads the caller's own impersonation status directly
 * over the api-sdk runtime seam; the case binds the idle answer ("none
 * in force", a bare 404) so the record half renders. The tenant is
 * seeded through the query parameter the page opens on.
 */
import { afterEach, describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { page } from 'vitest/browser'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { ImpersonationsPage } from './ImpersonationsPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import { createAdminOperations, makeAdminBackend } from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import { clearTenant, seedTenant } from '../test-utils/tenant-url.js'
import type { AdminImpersonation } from './operations.js'

const FLOOR_WIDTHS = [600, 900, 1200] as const

const TENANTS = [{ name: 'acme', displayName: 'Acme', kind: 'business' as const, status: 'active' as const }]

const RECORDS: readonly AdminImpersonation[] = [
  {
    id: 'imp-1',
    actor: 'staff-1',
    target: 'account-identifier-with-a-long-value-0001',
    tenant: 'acme',
    session_id: 'session-identifier-with-a-long-value-0001',
    started_at: '2026-10-15T12:00:00Z',
    ends_at: '2026-10-15T13:00:00Z',
    ended_at: '0001-01-01T00:00:00Z',
  },
]

/** The caller-session endpoint with no impersonation in force. */
const idleCallerSession: RequestFn = <T,>(
  path: string,
  options?: RequestOptions,
) => {
  const method = options?.method ?? 'GET'
  if (path === '/api/v1/auth/impersonation' && method === 'GET') {
    return Promise.reject({ status: 404, code: 'client.http.404' })
  }
  return Promise.resolve(undefined as T)
}

afterEach(clearTenant)

describe('ImpersonationsPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the record table inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      seedTenant('acme')
      bindRequestFn(idleCallerSession)

      const operations = createAdminOperations(
        makeAdminBackend({
          listTenants: () => Promise.resolve(TENANTS),
          listImpersonations: () => Promise.resolve(RECORDS),
        }),
      )
      renderWithProviders(<ImpersonationsPage />, { operations, language: 'en-US' })

      // The actor and the target are named by the account read's display
      // name, never by the recorded identifier.
      await screen.findAllByText('Operator')
      expect(screen.queryByText('staff-1')).toBeNull()
      await expectNoHorizontalOverflow()
    })
  }
})
