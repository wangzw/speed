import { act, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import {
  getCurrentTenant,
  setCurrentTenant,
  setTenantNames,
} from './chrome.js'
import { createHarnessClient, renderDemoApp } from './test-utils/render.js'
import {
  ANONYMOUS_SNAPSHOT,
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from './test-utils/session.js'

const ROSTER_PATH = '/api/v1/system/tenants'
const SELF_PATH = '/api/v1/auth/me/tenants'
const UNREAD_PATH = '/api/v1/notifications/messages/unread-count'

const PERSONAL_NAME = 'notice-member'

const PROFILE = {
  account_id: 'account-1',
  kind: 'user',
  display_name: 'Test User',
  mfa_required: false,
  password_set: true,
  created_at: '2026-01-01T00:00:00Z',
  attributes: {},
  locale: 'en-US',
  time_zone: 'UTC',
  identifiers: [],
  links: [],
  verification: { state: 'none', email: '', since: null },
} as const
const STREAM_FEATURES = [
  'tenant.admin',
  'org.personal',
  'notification.stream',
  'notification.inbox',
] as const

beforeEach(() => {
  setCurrentTenant('acme')
  setTenantNames(new Map([['acme', 'acme']]))
})

describe('the live subscription and the tenant context', () => {
  it('waits for the caller tenant list before opening the stream or reading the count', async () => {
    const calls: string[] = []
    const closes: string[] = []
    const openStream = vi.fn(() => ({
      close: () => {
        closes.push('close')
      },
    }))
    let resolveSelf: (value: unknown) => void = () => undefined
    const selfPending = new Promise((resolve) => {
      resolveSelf = resolve
    })
    bindRequestFn((async (path: string, options?: RequestOptions) => {
      const method = options?.method ?? 'GET'
      calls.push(`${method} ${path}`)
      if (method === 'GET' && path === SELF_PATH) {
        return selfPending
      }
      if (method === 'GET' && path === ROSTER_PATH) {
        throw Object.assign(new Error('the roster read was refused'), {
          status: 403,
        })
      }
      if (method === 'GET' && path === '/api/v1/auth/me') {
        return PROFILE
      }
      throw new Error(`unexpected ${method} ${path}`)
    }) as RequestFn)
    const api = Object.assign(createHarnessClient(), {
      openEventStream: openStream,
    })
    const session = createTestSession(ANONYMOUS_SNAPSHOT)
    renderDemoApp({ api, session })

    act(() =>
      session.set(
        authenticatedSnapshot({ features: readyFeatures(...STREAM_FEATURES) }),
      ),
    )

    await waitFor(() => expect(calls).toContain(`GET ${SELF_PATH}`))
    expect(getCurrentTenant()).toBeNull()
    expect(openStream).not.toHaveBeenCalled()
    expect(calls).not.toContain(`GET ${UNREAD_PATH}`)

    resolveSelf({
      tenants: [{ name: PERSONAL_NAME, kind: 'personal' }],
    })

    await waitFor(() => expect(openStream).toHaveBeenCalledTimes(2))
    expect(closes).toHaveLength(1)
    expect(openStream.mock.calls.length - closes.length).toBe(1)
    expect(getCurrentTenant()).toBe(PERSONAL_NAME)
    await waitFor(() => expect(calls).toContain(`GET ${UNREAD_PATH}`))
  })
})
