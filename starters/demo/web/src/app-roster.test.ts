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

import { waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import type { RequestFn } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import {
  getCurrentTenantAlias,
  getCurrentTenantId,
  setCurrentTenantId,
  setTenantAliases,
} from './chrome.js'
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'

afterEach(() => {
  setTenantAliases(new Map())
  setCurrentTenantId(null)
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

  it('settles the caller list first row that is not personal, whatever the roster holds', async () => {
    // The roster is ordered by identifier, so its first row can be a tenant
    // the caller has nothing to do with. The settle never consults it: a
    // personal row is skipped, and the list's first business row wins even
    // when the roster names another tenant first.
    const request: RequestFn = async <T>(path: string): Promise<T> => {
      if (path === '/api/v1/auth/me/tenants') {
        return {
          tenants: [
            { id: 'personal-other', alias: 'personal-other' },
            { id: 'accept-tenant', alias: 'accept-tenant' },
            { id: 'zzz', alias: 'zzz' },
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
      expect(getCurrentTenantId()).toBe('accept-tenant')
    })
    expect(getCurrentTenantAlias()).toBe('accept-tenant')
  })
})
