// @vitest-environment jsdom

import { act, cleanup, renderHook } from '@testing-library/react'
import { createElement, useState } from 'react'
import type { ReactNode } from 'react'
import { renderToString } from 'react-dom/server'
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  authenticatedCompletion,
  completeFlow,
  LOGIN_ANSWER,
  LOGIN_START,
  LOGOUT,
  makeHarness,
  principal,
  PROFILE_GET,
} from '@speed/test-utils/session-harness'
import type { Harness } from '@speed/test-utils/session-harness'
import {
  attachSession,
  TenantProvider,
  useAuthState,
  useCurrentTenant,
  usePermission,
} from './hooks'

afterEach(() => {
  cleanup()
})

const scripts = {
  [LOGIN_START]: () => completeFlow(),
  [LOGIN_ANSWER]: () => authenticatedCompletion(),
  [PROFILE_GET]: () => principal(),
}

async function signIn(harness: Harness): Promise<void> {
  await harness.session.startFlow({ method: 'password' })
  await harness.session.answerFlow({ seq: 2, step: 'complete' })
}

describe('before any session is attached', () => {
  it('useAuthState fails closed on the stable anonymous snapshot', () => {
    const { result, rerender } = renderHook(() => useAuthState())
    expect(result.current).toEqual({
      state: 'anonymous',
      principal: null,
      impersonating: false,
      permissionSets: { tenant: null, system: null },
      features: { state: 'none' },
    })
    const first = result.current
    rerender()
    expect(result.current).toBe(first)
  })

  it('usePermission fails closed', () => {
    const { result: tenantPerm } = renderHook(() =>
      usePermission('tenant', 'notes:read'),
    )
    const { result: systemPerm } = renderHook(() =>
      usePermission('system', 'users:manage'),
    )
    expect(tenantPerm.current).toBe(false)
    expect(systemPerm.current).toBe(false)
  })

  it('useCurrentTenant fails closed without a TenantProvider', () => {
    const { result } = renderHook(() => useCurrentTenant())
    expect(result.current).toBeNull()
  })
})

describe('the tenant context', () => {
  let setTenant: (tenantId: string | null) => void
  function TenantRoot({ children }: { children: ReactNode }) {
    const [tenantId, set] = useState<string | null>('tenant-1')
    setTenant = set
    return createElement(TenantProvider, { tenantId, children })
  }

  it('reads the host-supplied tenant and tracks its change', () => {
    const { result } = renderHook(() => useCurrentTenant(), {
      wrapper: TenantRoot,
    })
    expect(result.current).toEqual({ tenantId: 'tenant-1' })
    act(() => {
      setTenant('tenant-2')
    })
    expect(result.current).toEqual({ tenantId: 'tenant-2' })
  })

  it('reads null when the host passes null (no tenant in the route)', () => {
    const { result } = renderHook(() => useCurrentTenant(), {
      wrapper: TenantRoot,
    })
    act(() => {
      setTenant(null)
    })
    expect(result.current).toBeNull()
  })

  it('keeps one referentially stable tenant object for the whole stay in a tenant', () => {
    const { result, rerender } = renderHook(() => useCurrentTenant(), {
      wrapper: TenantRoot,
    })
    const first = result.current
    expect(first).toEqual({ tenantId: 'tenant-1' })
    rerender()
    expect(result.current).toBe(first)
    act(() => {
      setTenant('tenant-2')
    })
    expect(result.current).not.toBe(first)
    expect(result.current).toEqual({ tenantId: 'tenant-2' })
  })
})

describe('attached to a scripted session', () => {
  it('useAuthState tracks anonymous -> login -> logout', async () => {
    const harness = makeHarness({ ...scripts, [LOGOUT]: () => undefined })
    attachSession(harness.session)
    const { result } = renderHook(() => useAuthState())
    expect(result.current.state).toBe('anonymous')
    await act(async () => {
      await signIn(harness)
    })
    expect(result.current.state).toBe('authenticated')
    expect(result.current.principal?.account_id).toBe('user-1')
    await act(async () => {
      await harness.session.logout()
    })
    expect(result.current.state).toBe('anonymous')
    expect(result.current.principal).toBeNull()
  })

  it('usePermission fails closed until the domain set holds the permission', async () => {
    const harness = makeHarness(scripts)
    attachSession(harness.session)
    const { result } = renderHook(() => usePermission('tenant', 'notes:read'))
    expect(result.current).toBe(false)
    await act(async () => {
      await signIn(harness)
    })
    expect(result.current).toBe(false)
    act(() => {
      harness.session.setPermissionSet('tenant', ['notes:write'])
    })
    expect(result.current).toBe(false)
    act(() => {
      harness.session.setPermissionSet('tenant', ['notes:read'])
    })
    expect(result.current).toBe(true)
  })

  it("a domain's set never satisfies a check in the other domain", async () => {
    const harness = makeHarness(scripts)
    attachSession(harness.session)
    await act(async () => {
      await signIn(harness)
    })
    const { result: tenantRead } = renderHook(() =>
      usePermission('tenant', 'notes:read'),
    )
    const { result: systemRead } = renderHook(() =>
      usePermission('system', 'notes:read'),
    )
    act(() => {
      harness.session.setPermissionSet('tenant', ['notes:read'])
    })
    expect(tenantRead.current).toBe(true)
    expect(systemRead.current).toBe(false)
    act(() => {
      harness.session.setPermissionSet('system', ['users:manage'])
    })
    expect(systemRead.current).toBe(false)
  })

  it('set changes re-render the selectors through the shared snapshot', async () => {
    const harness = makeHarness({ ...scripts, [LOGOUT]: () => undefined })
    attachSession(harness.session)
    await act(async () => {
      await signIn(harness)
    })
    const { result: state } = renderHook(() => useAuthState())
    const { result: can } = renderHook(() =>
      usePermission('tenant', 'notes:read'),
    )
    act(() => {
      harness.session.setPermissionSet('tenant', ['notes:read'])
    })
    expect(state.current.permissionSets).toEqual({
      tenant: ['notes:read'],
      system: null,
    })
    expect(can.current).toBe(true)
    act(() => {
      harness.session.setPermissionSet('tenant', null)
    })
    expect(state.current.permissionSets).toEqual({
      tenant: null,
      system: null,
    })
    expect(can.current).toBe(false)
    await act(async () => {
      await harness.session.logout()
    })
    expect(state.current.state).toBe('anonymous')
    expect(can.current).toBe(false)
  })

  it('attachSession rebinds: the previous session stops reaching the hooks', async () => {
    const first = makeHarness(scripts)
    const second = makeHarness(scripts)
    attachSession(first.session)
    await act(async () => {
      await signIn(first)
    })
    const { result } = renderHook(() => useAuthState())
    expect(result.current.state).toBe('authenticated')

    act(() => {
      attachSession(second.session)
    })
    expect(result.current.state).toBe('anonymous')

    await act(async () => {
      await signIn(first)
    })
    expect(result.current.state).toBe('anonymous')

    await act(async () => {
      await signIn(second)
    })
    expect(result.current.state).toBe('authenticated')
  })
})

describe('server rendering', () => {
  function ServerProbe() {
    const snapshot = useAuthState()
    return createElement('div', null, snapshot.state)
  }

  it('renders the anonymous snapshot on the server, even with an authenticated session attached', async () => {
    const harness = makeHarness(scripts)
    attachSession(harness.session)
    await act(async () => {
      await signIn(harness)
    })
    expect(harness.session.getSnapshot().state).toBe('authenticated')
    const errorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    try {
      const html = renderToString(createElement(ServerProbe))
      expect(html).toBe('<div>anonymous</div>')
      expect(errorSpy).not.toHaveBeenCalled()
    } finally {
      errorSpy.mockRestore()
    }
  })
})
