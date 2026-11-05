/**
 * The platform roster read (src/app.tsx): a caller whose platform permission
 * set holds tenant.read reads the roster for the CURRENT aliases of tenants
 * outside its own memberships, and the request surface announces that alias as
 * X-Tenant rather than the id the switcher keys on. The roster decides no
 * tenant: the current tenant is settled from the caller's own tenant list. The
 * alias differ from the id, so a run that announced the id instead would fail.
 *
 * No caller without tenant.read spends the request at all: an ordinary member
 * is covered by the app suites' zero-request assertions.
 */

import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it } from 'vitest'
import type { RequestFn } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import {
  getCurrentTenantAlias,
  getCurrentTenantId,
  setCurrentTenantId,
  setTenantAliases,
} from './chrome.js'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { renderDemoApp } from './test-utils/render.js'
import { clearSelfTenants } from './tenant-self.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'

afterEach(() => {
  setTenantAliases(new Map())
  setCurrentTenantId(null)
  clearSelfTenants()
})

describe('who spends the platform roster read', () => {
  /** Binds the self and roster reads, recording every path. */
  function bindReads(requested: string[]): void {
    const request: RequestFn = async <T>(path: string): Promise<T> => {
      requested.push(path)
      if (path === '/api/v1/auth/me/tenants') {
        return { tenants: [{ id: 'acme', alias: 'acme' }] } as T
      }
      if (path === '/api/v1/system/tenants') {
        return {
          tenants: [{ id: 'acme', alias: 'acme-inc', status: 'active' }],
        } as T
      }
      throw new Error(`the suite binds only the tenant reads: ${path}`)
    }
    bindRequestFn(request)
  }

  it('spends no roster read for a member', async () => {
    const requested: string[] = []
    bindReads(requested)

    renderDemoApp({
      session: createTestSession(authenticatedSnapshot({ system: [] })),
    })

    await waitFor(() => expect(getCurrentTenantId()).toBe('acme'))
    expect(requested).not.toContain('/api/v1/system/tenants')
  })

  it('spends no roster read while the platform set is unknown', async () => {
    const requested: string[] = []
    bindReads(requested)

    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    await waitFor(() => expect(getCurrentTenantId()).toBe('acme'))
    expect(requested).not.toContain('/api/v1/system/tenants')
  })

  it('spends exactly one roster read for a platform staff caller', async () => {
    const requested: string[] = []
    bindReads(requested)

    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ system: ['tenant.read'] }),
      ),
    })

    await waitFor(() =>
      expect(getCurrentTenantAlias()).toBe('acme-inc'),
    )
    expect(
      requested.filter((path) => path === '/api/v1/system/tenants'),
    ).toHaveLength(1)
  })
})

describe('the announced tenant, once the roster is read', () => {
  it('announces the roster alias, not the id the switcher keys on', async () => {
    const request: RequestFn = async <T>(path: string): Promise<T> => {
      if (path === '/api/v1/auth/me/tenants') {
        return { tenants: [{ id: 'acme', alias: 'acme' }] } as T
      }
      if (path === '/api/v1/system/tenants') {
        return {
          tenants: [{ id: 'acme', alias: 'acme-inc', status: 'active' }],
        } as T
      }
      throw new Error(`the suite binds only the tenant reads: ${path}`)
    }
    bindRequestFn(request)

    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ system: ['tenant.read'] }),
      ),
    })

    // The caller list settles the tenant; the roster names it.
    await waitFor(() => {
      expect(getCurrentTenantId()).toBe('acme')
    })
    await waitFor(() => {
      expect(getCurrentTenantAlias()).toBe('acme-inc')
    })
  })

  it('settles the caller list first row whose kind is not personal, whatever the roster holds', async () => {
    // The roster is ordered by identifier, so its first row can be a tenant
    // the caller has nothing to do with. The settle never consults it: a row
    // the read answers as personal is skipped, and the list's first business
    // row wins even when the roster names another tenant first. Another
    // account's personal tenant is a business row to the read (the host's
    // resolver compares the caller's own derived identifier exactly), so the
    // settle may choose it; a prefix test would skip it and choose later.
    const request: RequestFn = async <T>(path: string): Promise<T> => {
      if (path === '/api/v1/auth/me/tenants') {
        return {
          tenants: [
            { id: 'personal-account-2', alias: 'personal-account-2', kind: 'business' },
            { id: 'my-space', alias: 'my-space', kind: 'personal' },
            { id: 'zzz', alias: 'zzz', kind: 'business' },
          ],
        } as T
      }
      if (path === '/api/v1/system/tenants') {
        return {
          tenants: [{ id: 'aaa', alias: 'aaa', status: 'active' }],
        } as T
      }
      throw new Error(`the suite binds only the tenant reads: ${path}`)
    }
    bindRequestFn(request)

    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ system: ['tenant.read'] }),
      ),
    })

    await waitFor(() => {
      expect(getCurrentTenantId()).toBe('personal-account-2')
    })
    expect(getCurrentTenantAlias()).toBe('personal-account-2')
  })
})

describe('a failed roster read', () => {
  it('explains the fault beside the switcher, leaves the tenant, and retries', async () => {
    // The roster is a staff alias source: a failed read decides no tenant and
    // infers no membership, so the switcher keeps the tenant the caller's own
    // list settled and shows what could not be named with a retry. The notice
    // sits under the organization row and needs no expansion to be read.
    setCurrentTenantId(null)
    setTenantAliases(new Map())
    clearSelfTenants()
    let rosterReads = 0
    const request: RequestFn = async <T>(path: string): Promise<T> => {
      if (path === '/api/v1/auth/me/tenants') {
        return {
          tenants: [
            { id: 'acme', alias: 'acme' },
            { id: 'globex', alias: 'globex' },
          ],
        } as T
      }
      if (path === '/api/v1/system/tenants') {
        rosterReads += 1
        if (rosterReads === 1) {
          throw new Error('the roster service is unavailable')
        }
        return {
          tenants: [{ id: 'acme', alias: 'acme-inc', status: 'active' }],
        } as T
      }
      throw new Error(`the suite binds only the tenant reads: ${path}`)
    }
    bindRequestFn(request)

    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ system: ['tenant.read'] }),
      ),
    })

    // The current tenant settled from the caller's own list and does not
    // change because the roster could not be read.
    await waitFor(() => expect(getCurrentTenantId()).toBe('acme'))

    const user = userEvent.setup()
    await user.click(await screen.findByTestId('user-menu-trigger'))
    expect(
      await screen.findByText(demoEnUS.tenantRoster.failed),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: demoEnUS.tenantRoster.retry }),
    )

    // The retry issues the read again; its answer clears the notice and names
    // the tenant from the roster's alias.
    await waitFor(() =>
      expect(screen.queryByText(demoEnUS.tenantRoster.failed)).toBeNull(),
    )
    expect(rosterReads).toBe(2)
    expect(getCurrentTenantId()).toBe('acme')
    await waitFor(() => expect(getCurrentTenantAlias()).toBe('acme-inc'))
  })

  it('reports the fault under a lone organization, whose row never expands', async () => {
    // One membership means no expansion: the row is context, not a control.
    // The failed read still has to be reported, so the notice renders under
    // the row itself.
    setCurrentTenantId(null)
    setTenantAliases(new Map())
    clearSelfTenants()
    bindRequestFn((async <T>(path: string): Promise<T> => {
      if (path === '/api/v1/auth/me/tenants') {
        return { tenants: [{ id: 'acme', alias: 'acme' }] } as T
      }
      if (path === '/api/v1/system/tenants') {
        throw new Error('the roster service is unavailable')
      }
      throw new Error(`the suite binds only the tenant reads: ${path}`)
    }) as RequestFn)

    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({ system: ['tenant.read'] }),
      ),
    })

    await waitFor(() => expect(getCurrentTenantId()).toBe('acme'))
    await userEvent.click(await screen.findByTestId('user-menu-trigger'))
    expect(
      await screen.findByText(demoEnUS.tenantRoster.failed),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: demoEnUS.tenantRoster.retry }),
    ).toBeInTheDocument()
  })
})
