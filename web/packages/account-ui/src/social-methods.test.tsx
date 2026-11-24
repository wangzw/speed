/**
 * useSocialMethodsAvailable checks the deployment's social sign-in availability.
 */

import { renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { AuthSession, AuthnMethodList } from '@speed/auth-core'
import { useSocialMethodsAvailable } from './social-methods.js'

function methods(...kinds: string[]): AuthnMethodList {
  return {
    methods: kinds.map((kind, index) => ({
      name: `method-${index}`,
      kind,
    })),
    signup: { enabled: false, password: { enabled: false } },
    mfa: { factors: [] },
    human: {
      phone: { required: false, provider: 'off' },
      other: { required: false, provider: 'off' },
    },
    password_reset: { enabled: false, methods: [] },
  }
}

function sessionReading(
  listMethods: () => Promise<AuthnMethodList>,
): AuthSession {
  return { listMethods } as unknown as AuthSession
}

describe('useSocialMethodsAvailable', () => {
  it('reports a deployment that serves a redirect method', async () => {
    const { result } = renderHook(() =>
      useSocialMethodsAvailable(
        sessionReading(() => Promise.resolve(methods('form', 'redirect'))),
      ),
    )
    await waitFor(() => expect(result.current.pending).toBe(false))
    expect(result.current.available).toBe(true)
  })

  it('reports no availability when the deployment serves form methods only', async () => {
    const { result } = renderHook(() =>
      useSocialMethodsAvailable(
        sessionReading(() => Promise.resolve(methods('form'))),
      ),
    )
    await waitFor(() => expect(result.current.pending).toBe(false))
    expect(result.current.available).toBe(false)
  })

  it('reads a failed discovery as no availability', async () => {
    const { result } = renderHook(() =>
      useSocialMethodsAvailable(
        sessionReading(() => Promise.reject(new Error('unreachable'))),
      ),
    )
    await waitFor(() => expect(result.current.pending).toBe(false))
    expect(result.current.available).toBe(false)
  })
})
