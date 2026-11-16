/**
 * The platform roster read (src/app.tsx): a caller whose platform permission
 * set holds tenant.read reads the roster for the names of tenants outside its
 * own memberships. A tenant is identified by its name, which is both the
 * switcher's key and the value the request surface announces as X-Tenant. The
 * roster
 * decides no tenant: the current tenant is settled from the caller's own tenant
 * list.
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
  getCurrentTenantName,
  getCurrentTenant,
  getRecordedTenantDisplayName,
  setCurrentTenant,
  setTenantNames,
} from './chrome.js'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { renderDemoApp } from './test-utils/render.js'
import { clearSelfTenants } from './tenant-self.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'

afterEach(() => {
  setTenantNames(new Map())
  setCurrentTenant(null)
  clearSelfTenants()
})

describe('who spends the platform roster read', () => {
  /** Binds the self and roster reads, recording every path. */
  function bindReads(requested: string[]): void {
    const request: RequestFn = async <T>(path: string): Promise<T> => {
      requested.push(path)
      if (path === '/api/v1/auth/me/tenants') {
        return { tenants: [{ name: 'acme', kind: 'business' }] } as T
      }
      if (path === '/api/v1/system/tenants') {
        return {
          tenants: [{ name: 'acme', display_name: 'Acme Inc.', kind: 'business', status: 'active' }],
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

    await waitFor(() => expect(getCurrentTenant()).toBe('acme'))
    expect(requested).not.toContain('/api/v1/system/tenants')
  })

  it('spends no roster read while the platform set is unknown', async () => {
    const requested: string[] = []
    bindReads(requested)

    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    await waitFor(() => expect(getCurrentTenant()).toBe('acme'))
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

    await waitFor(() => expect(getCurrentTenantName()).toBe('acme'))
    expect(
      requested.filter((path) => path === '/api/v1/system/tenants'),
    ).toHaveLength(1)
  })
})

describe('the announced tenant, once the roster is read', () => {
  it('announces the settled name, the same value the switcher keys on', async () => {
    const request: RequestFn = async <T>(path: string): Promise<T> => {
      if (path === '/api/v1/auth/me/tenants') {
        return { tenants: [{ name: 'acme', kind: 'business' }] } as T
      }
      if (path === '/api/v1/system/tenants') {
        return {
          tenants: [{ name: 'acme', display_name: 'Acme Inc.', kind: 'business', status: 'active' }],
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

    // The caller list settles the tenant; the roster does not rename it.
    await waitFor(() => {
      expect(getCurrentTenant()).toBe('acme')
    })
    expect(getCurrentTenantName()).toBe('acme')
    expect(getRecordedTenantDisplayName('acme')).toBe('Acme Inc.')
  })

  it('settles the caller list first row whose kind is not personal, whatever the roster holds', async () => {
    // The roster can name tenants the caller has nothing to do with. The
    // settle never consults it: a row the read answers as personal is skipped,
    // and the list's first business row wins even when the roster names another
    // tenant first. Another account's personal space is a business row to the
    // read (the host's resolver compares the caller's own derived name
    // exactly), so the settle may choose it; a prefix test would skip it and
    // choose later.
    const request: RequestFn = async <T>(path: string): Promise<T> => {
      if (path === '/api/v1/auth/me/tenants') {
        return {
          tenants: [
            { name: 'bob', kind: 'business' },
            { name: 'my-space', kind: 'personal' },
            { name: 'zzz', kind: 'business' },
          ],
        } as T
      }
      if (path === '/api/v1/system/tenants') {
        return {
          tenants: [{ name: 'aaa', display_name: 'AAA', kind: 'business', status: 'active' }],
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
      expect(getCurrentTenant()).toBe('bob')
    })
    expect(getCurrentTenantName()).toBe('bob')
  })
})

describe('a failed roster read', () => {
  it('explains the fault beside the switcher, leaves the tenant, and retries', async () => {
    // The roster is a staff name source: a failed read decides no tenant and
    // infers no membership, so the switcher keeps the tenant the caller's own
    // list settled and shows what could not be named with a retry. The notice
    // sits under the organization row and needs no expansion to be read.
    setCurrentTenant(null)
    setTenantNames(new Map())
    clearSelfTenants()
    let rosterReads = 0
    const request: RequestFn = async <T>(path: string): Promise<T> => {
      if (path === '/api/v1/auth/me/tenants') {
        return {
          tenants: [
            { name: 'acme', kind: 'business' },
            { name: 'globex', kind: 'business' },
          ],
        } as T
      }
      if (path === '/api/v1/system/tenants') {
        rosterReads += 1
        if (rosterReads === 1) {
          throw new Error('the roster service is unavailable')
        }
        return {
          tenants: [{ name: 'acme', display_name: 'Acme Inc.', kind: 'business', status: 'active' }],
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
    await waitFor(() => expect(getCurrentTenant()).toBe('acme'))

    const user = userEvent.setup()
    await user.click(await screen.findByTestId('user-menu-trigger'))
    expect(
      await screen.findByText(demoEnUS.tenantRoster.failed),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: demoEnUS.tenantRoster.retry }),
    )

    // The retry issues the read again; its answer clears the notice. The
    // settled tenant keeps its request-surface name.
    await waitFor(() =>
      expect(screen.queryByText(demoEnUS.tenantRoster.failed)).toBeNull(),
    )
    expect(rosterReads).toBe(2)
    expect(getCurrentTenant()).toBe('acme')
    expect(getCurrentTenantName()).toBe('acme')
  })

  it('reports the fault under a lone organization, whose row never expands', async () => {
    // One membership means no expansion: the row is context, not a control.
    // The failed read still has to be reported, so the notice renders under
    // the row itself.
    setCurrentTenant(null)
    setTenantNames(new Map())
    clearSelfTenants()
    bindRequestFn((async <T>(path: string): Promise<T> => {
      if (path === '/api/v1/auth/me/tenants') {
        return { tenants: [{ name: 'acme', kind: 'business' }] } as T
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

    await waitFor(() => expect(getCurrentTenant()).toBe('acme'))
    await userEvent.click(await screen.findByTestId('user-menu-trigger'))
    expect(
      await screen.findByText(demoEnUS.tenantRoster.failed),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: demoEnUS.tenantRoster.retry }),
    ).toBeInTheDocument()
  })
})
