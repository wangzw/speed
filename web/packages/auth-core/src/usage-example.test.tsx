// @vitest-environment jsdom

import { act, cleanup, fireEvent, render } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { useState } from 'react'
import type { ReactNode } from 'react'
import { QueryClient } from '@tanstack/react-query'
import type { AuthSession } from './session'
import {
  attachSession,
  TenantProvider,
  useAuthState,
  useCurrentTenant,
  usePermission,
} from './hooks'
import {
  evictTenantQueries,
  tenantQueryKey,
  useTenantQueryKey,
} from './query-key'
import {
  authenticatedCompletion,
  completeFlow,
  LOGIN_ANSWER,
  LOGIN_START,
  LOGOUT,
  makeHarness,
  principal,
  PROFILE_GET,
  snapshotLog,
  TOKEN_ISSUE,
  tokenResponse,
} from '@speed/test-utils/session-harness'

afterEach(() => {
  cleanup()
})

async function signInWithPassword(session: AuthSession): Promise<void> {
  await session.startFlow({ method: 'password', purpose: 'login' })
  await session.answerFlow({
    seq: 2,
    step: 'complete',
    input: { identifier: 'ada@example.com', password: 'pw' },
  })
}

function SessionPanel({ session }: { readonly session: AuthSession }) {
  const snapshot = useAuthState()
  const tenant = useCurrentTenant()
  const canCreateNotes = usePermission('tenant', 'notes:write')
  const canManageUsers = usePermission('system', 'users:manage')
  return (
    <div>
      <p data-testid="state">{snapshot.state}</p>
      <p data-testid="tenant">{tenant === null ? 'none' : tenant.tenantId}</p>
      <p data-testid="notes-write">{String(canCreateNotes)}</p>
      <p data-testid="users-manage">{String(canManageUsers)}</p>
      <button type="button" onClick={() => void signInWithPassword(session)}>
        Sign in
      </button>
      <button type="button" onClick={() => void session.logout()}>
        Sign out
      </button>
    </div>
  )
}

function TenantHost({ children }: { children: ReactNode }) {
  const [tenantId] = useState<string | null>('tenant-1')
  return <TenantProvider tenantId={tenantId}>{children}</TenantProvider>
}

const scripts = {
  [LOGIN_START]: () => completeFlow(),
  [LOGIN_ANSWER]: () => authenticatedCompletion(),
  [PROFILE_GET]: () => principal(),
}

describe('README usage example', () => {
  it('runs the quick-start journey: login, host-attached sets, logout', async () => {
    const harness = makeHarness({ ...scripts, [LOGOUT]: () => undefined })
    attachSession(harness.session)
    const transitions = snapshotLog(harness.session)
    const utils = render(
      <TenantHost>
        <SessionPanel session={harness.session} />
      </TenantHost>,
    )
    const reads = () => ({
      state: utils.getByTestId('state').textContent,
      tenant: utils.getByTestId('tenant').textContent,
      notesWrite: utils.getByTestId('notes-write').textContent,
      usersManage: utils.getByTestId('users-manage').textContent,
    })

    expect(reads()).toEqual({
      state: 'anonymous',
      tenant: 'tenant-1',
      notesWrite: 'false',
      usersManage: 'false',
    })

    await act(async () => {
      fireEvent.click(utils.getByRole('button', { name: 'Sign in' }))
    })
    expect(harness.store.get()).toBe('access-1')
    expect(reads()).toEqual({
      state: 'authenticated',
      tenant: 'tenant-1',
      notesWrite: 'false',
      usersManage: 'false',
    })

    act(() => {
      harness.session.setPermissionSet('tenant', ['notes:write'])
    })
    act(() => {
      harness.session.setPermissionSet('system', ['users:manage'])
    })
    expect(reads()).toEqual({
      state: 'authenticated',
      tenant: 'tenant-1',
      notesWrite: 'true',
      usersManage: 'true',
    })

    await act(async () => {
      fireEvent.click(utils.getByRole('button', { name: 'Sign out' }))
    })
    expect(harness.store.get()).toBeNull()
    expect(reads()).toEqual({
      state: 'anonymous',
      tenant: 'tenant-1',
      notesWrite: 'false',
      usersManage: 'false',
    })
    expect(transitions.map((snapshot) => snapshot.state)).toEqual([
      'authenticated',
      'authenticated',
      'authenticated',
      'anonymous',
    ])
  })

  it('runs the README tenant-scoped query-key flow', async () => {
    const harness = makeHarness({
      ...scripts,
      [TOKEN_ISSUE]: () => tokenResponse({ access_token: 'access-2' }),
    })
    attachSession(harness.session)
    const queryClient = new QueryClient()
    queryClient.setQueryData(tenantQueryKey('tenant-1', 'notes'), { rows: 1 })
    queryClient.setQueryData(tenantQueryKey('tenant-2', 'notes'), { rows: 2 })

    let setTenant!: (tenantId: string | null) => void
    function KeyHost({ children }: { children: ReactNode }) {
      const [tenantId, set] = useState<string | null>(null)
      setTenant = set
      return <TenantProvider tenantId={tenantId}>{children}</TenantProvider>
    }
    function NotesKeyProbe() {
      const { tenantId, queryKey } = useTenantQueryKey(['notes'])
      return <p data-testid="key">{JSON.stringify([tenantId, queryKey])}</p>
    }
    const utils = render(
      <KeyHost>
        <NotesKeyProbe />
      </KeyHost>,
    )
    expect(utils.getByTestId('key').textContent).toBe(
      JSON.stringify([null, [null, 'notes']]),
    )

    act(() => {
      setTenant('tenant-1')
    })
    expect(utils.getByTestId('key').textContent).toBe(
      JSON.stringify(['tenant-1', ['tenant-1', 'notes']]),
    )

    const departingTenantId = 'tenant-1'
    act(() => {
      setTenant('tenant-2')
    })
    expect(utils.getByTestId('key').textContent).toBe(
      JSON.stringify(['tenant-2', ['tenant-2', 'notes']]),
    )
    evictTenantQueries(queryClient, departingTenantId)
    expect(
      queryClient.getQueryData(tenantQueryKey('tenant-1', 'notes')),
    ).toBeUndefined()
    expect(queryClient.getQueryData(tenantQueryKey('tenant-2', 'notes'))).toEqual(
      { rows: 2 },
    )
  })

  it('runs the README login-flow journey: start, answer, commit', async () => {
    const harness = makeHarness(scripts)
    const transitions = snapshotLog(harness.session)

    const first = await harness.session.startFlow({
      method: 'password',
      purpose: 'login',
    })
    expect(first).toMatchObject({ state: 'ready', next: { kind: 'complete' } })
    expect(harness.store.get()).toBeNull()
    expect(harness.session.getSnapshot().state).toBe('anonymous')

    const completion = await harness.session.answerFlow({
      seq: 2,
      step: 'complete',
      input: { identifier: 'ada@example.com', password: 'pw' },
    })
    expect(completion.state).toBe('authenticated')
    expect(harness.store.get()).toBe('access-1')
    expect(harness.session.getSnapshot().state).toBe('authenticated')
    expect(transitions.map((snapshot) => snapshot.state)).toEqual([
      'authenticated',
    ])
  })
})
