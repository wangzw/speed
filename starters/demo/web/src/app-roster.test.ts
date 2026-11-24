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

    await waitFor(() => {
      expect(getCurrentTenant()).toBe('acme')
    })
    expect(getCurrentTenantName()).toBe('acme')
    expect(getRecordedTenantDisplayName('acme')).toBe('Acme Inc.')
  })

  it('settles the caller list first row whose kind is not personal, whatever the roster holds', async () => {
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

    await waitFor(() => expect(getCurrentTenant()).toBe('acme'))

    const user = userEvent.setup()
    await user.click(await screen.findByTestId('user-menu-trigger'))
    expect(
      await screen.findByText(demoEnUS.tenantRoster.failed),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: demoEnUS.tenantRoster.retry }),
    )

    await waitFor(() =>
      expect(screen.queryByText(demoEnUS.tenantRoster.failed)).toBeNull(),
    )
    expect(rosterReads).toBe(2)
    expect(getCurrentTenant()).toBe('acme')
    expect(getCurrentTenantName()).toBe('acme')
  })

  it('reports the fault under a lone organization, whose row never expands', async () => {
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
