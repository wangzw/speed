/**
 * The demo's roster read (src/app.tsx): once a viewer is signed in, the app
 * reads the tenant roster and announces each tenant's CURRENT alias as X-Tenant,
 * rather than the id the switcher keys on. The roster here carries a tenant
 * whose alias a rename has moved away from its id -- the case the demo's own
 * seeded tenant (whose id equals its alias) does not exhibit -- so the test
 * only passes when the announced value is the alias and not the id.
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

describe('the announced tenant, once the roster is read', () => {
  it('announces the roster alias, not the id the switcher keys on', async () => {
    const request: RequestFn = async <T>(
      path: string,
    ): Promise<T> => {
      if (path === '/tenant/admin/tenants') {
        return {
          tenants: [{ id: 'acme', alias: 'acme-inc', status: 'active' }],
        } as T
      }
      throw new Error(`the suite binds only the roster read: ${path}`)
    }
    bindRequestFn(request)

    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })

    // The roster settles the current tenant to the first active one; its id is
    // the switcher's identity, its alias is what the request surface carries.
    await waitFor(() => {
      expect(getCurrentTenantId()).toBe('acme')
    })
    expect(getCurrentTenantAlias()).toBe('acme-inc')
  })
})
