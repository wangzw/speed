/**
 * The live notification subscription and the tenant context.
 *
 * The notification endpoints are tenant-scoped, and a new account's current
 * tenant arrives through the personal-tenant ensure. In one tab a session
 * ends and a new account signs in (the walkthrough's login-mode case): the
 * ended session's tenant must not stay behind, and the subscription must not
 * open -- nor the bell read its unread count -- against a tenant that has not
 * settled. A connection opened early carries a tenant the caller is not a
 * member of and is refused with a bare 404.
 */

import { act, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import {
  getCurrentTenantId,
  setCurrentTenantId,
  setTenantAliases,
} from './chrome.js'
import { createHarnessClient, renderDemoApp } from './test-utils/render.js'
import {
  ANONYMOUS_SNAPSHOT,
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from './test-utils/session.js'

const ROSTER_PATH = '/api/v1/system/tenants'
const ENSURE_PATH = '/api/v1/workspace/personal'
const UNREAD_PATH = '/api/v1/notifications/messages/unread-count'

const PERSONAL_ID = 'personal-account-1'
const STREAM_FEATURES = [
  'tenant.admin',
  'workspace.personal',
  'notification.stream',
  'notification.inbox',
] as const

beforeEach(() => {
  // The tenant a previous session in this tab left behind.
  setCurrentTenantId('acme')
  setTenantAliases(new Map([['acme', 'acme']]))
})

describe('the live subscription and the tenant context', () => {
  it('waits for the personal tenant before opening the stream or reading the count', async () => {
    const calls: string[] = []
    const closes: string[] = []
    const openStream = vi.fn(() => ({
      close: () => {
        closes.push('close')
      },
    }))
    let resolveEnsure: (value: unknown) => void = () => undefined
    const ensurePending = new Promise((resolve) => {
      resolveEnsure = resolve
    })
    bindRequestFn((async (path: string, options?: RequestOptions) => {
      const method = options?.method ?? 'GET'
      calls.push(`${method} ${path}`)
      if (method === 'GET' && path === ROSTER_PATH) {
        throw Object.assign(new Error('the roster read was refused'), {
          status: 403,
        })
      }
      if (method === 'POST' && path === ENSURE_PATH) {
        return ensurePending
      }
      throw new Error(`unexpected ${method} ${path}`)
    }) as RequestFn)
    // The assembled client's opener is observable; requests still go through
    // the scripted runtime seam above.
    const api = Object.assign(createHarnessClient(), {
      openEventStream: openStream,
    })
    const session = createTestSession(ANONYMOUS_SNAPSHOT)
    renderDemoApp({ api, session })

    // An authenticated session arrives in the same tab (a login between the
    // phases of the walkthrough).
    act(() =>
      session.set(
        authenticatedSnapshot({ features: readyFeatures(...STREAM_FEATURES) }),
      ),
    )

    // The roster refused and the ensure is in flight: the ended session's
    // tenant is gone and the new context has not settled, so neither
    // tenant-scoped surface may open.
    await waitFor(() => expect(calls).toContain(`POST ${ENSURE_PATH}`))
    expect(getCurrentTenantId()).toBeNull()
    expect(openStream).not.toHaveBeenCalled()
    expect(calls).not.toContain(`GET ${UNREAD_PATH}`)

    resolveEnsure({
      tenant: { id: PERSONAL_ID, alias: PERSONAL_ID, kind: 'personal' },
      created: true,
    })

    // The ensure settled the personal tenant, and both surfaces follow. The
    // harness renders the production stack, StrictMode included, so the
    // subscriber mounts, is torn down and mounts again: exactly one of the
    // two connections stays live.
    await waitFor(() => expect(openStream).toHaveBeenCalledTimes(2))
    expect(closes).toHaveLength(1)
    expect(openStream.mock.calls.length - closes.length).toBe(1)
    expect(getCurrentTenantId()).toBe(PERSONAL_ID)
    await waitFor(() => expect(calls).toContain(`GET ${UNREAD_PATH}`))
  })
})
