import { beforeEach, describe, expect, it, vi } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
import { act, fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { ImpersonationsPage } from './ImpersonationsPage.js'
import { AdminImpersonationChangeProvider } from './internal/impersonation-change.js'
import { AdminImpersonationStartingProvider } from './internal/impersonation-starting.js'
import type { AdminAccount, AdminImpersonation } from './operations.js'
import { renderWithProviders, createAdminUiI18n } from '../test-utils/render.js'
import { ADMIN_UI_NAMESPACE } from './resources.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  columnVisibility,
  containerMaxWidthOf,
} from '../test-utils/container-visibility.js'
import {
  accountFixture,
  createAdminOperations,
  makeAdminBackend,
  tenantFixture,
  type AdminBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { createStepUpSession } from '../test-utils/session.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

const TENANT = tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })

const END_REASON_LABELS = { revoked: 'end-reason-revoked' } as const

function fill(
  template: string,
  values: Readonly<Record<string, string>>,
): string {
  return Object.entries(values).reduce(
    (text, [name, value]) => text.replace(`{{${name}}}`, value),
    template,
  )
}

function byName(name: string): string {
  return fill(zhCN.common.accountReference.name, { name })
}

function byIdentifier(identifier: string): string {
  return fill(zhCN.common.accountReference.identifier, { identifier })
}

function byNameAndIdentifier(name: string, identifier: string): string {
  return fill(zhCN.common.accountReference.nameWithIdentifier, {
    name,
    identifier,
  })
}

function targetLine(name: string): string {
  return `${zhCN.impersonations.current.target}${name}`
}

const RECORDS_UNAVAILABLE = zhCN.impersonations.recordsUnavailable

interface GrantFixture {
  readonly id: string
  readonly target: string
  readonly tenant: string
  readonly starts_at: string
  readonly ends_at: string
}

function makeCallerSessionBackend() {
  let grant: GrantFixture | undefined
  const started: unknown[] = []
  const ended: unknown[] = []
  let statusReads = 0
  const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
    const method = options?.method ?? 'GET'
    if (path !== '/api/v1/auth/impersonation') {
      return Promise.resolve(undefined as T)
    }
    if (method === 'GET') {
      statusReads += 1
      if (grant === undefined) {
        return Promise.reject({ status: 404, code: 'client.http.404' })
      }
      return Promise.resolve({ grant } as T)
    }
    if (method === 'POST') {
      started.push(options?.body)
      grant = {
        id: 'imp-1',
        target: 'acc-1',
        tenant: 'acme',
        starts_at: '2026-10-15T12:00:00Z',
        ends_at: '2026-10-15T13:00:00Z',
      }
      return Promise.resolve({ grant } as T)
    }
    if (method === 'DELETE') {
      ended.push(true)
      grant = undefined
      return Promise.resolve(undefined as T)
    }
    return Promise.resolve(undefined as T)
  }
  return {
    fn,
    started,
    ended,
    readGrant: () => grant,
    setGrant: (value: GrantFixture | undefined) => {
      grant = value
    },
    statusReads: () => statusReads,
  }
}

const idleCallerSession: RequestFn = <T,>(
  _path: string,
  options?: RequestOptions,
) => {
  const method = options?.method ?? 'GET'
  if (_path === '/api/v1/auth/impersonation' && method === 'GET') {
    return Promise.reject({ status: 404, code: 'client.http.404' })
  }
  return Promise.resolve(undefined as T)
}

function record(overrides: Partial<AdminImpersonation> = {}): AdminImpersonation {
  return {
    id: 'imp-1',
    actor: 'staff-1',
    target: 'acc-1',
    tenant: 'acme',
    session_id: 'sess-1',
    started_at: '2026-10-15T12:00:00Z',
    ends_at: '2026-10-15T13:00:00Z',
    ended_at: null,
    ...overrides,
  }
}

async function pickTenant(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(screen.getByLabelText(zhCN.common.tenant))
  await user.click(await screen.findByRole('option', { name: 'Acme' }))
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

async function expectRefreshInert(
  countReads: () => readonly [number, number],
  readsBefore: readonly [number, number],
): Promise<void> {
  const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
  await waitForCondition(() => {
    expect(refresh).toHaveAttribute('aria-disabled', 'true')
  })
  expect(refresh).not.toHaveAttribute('aria-describedby')
  fireEvent.click(refresh)
  await act(async () => {
    await Promise.resolve()
  })
  expect(countReads()).toEqual(readsBefore)
}

const ACCOUNT_NAMES: Record<string, string> = {
  'staff-1': 'Staff One',
  'acc-1': 'Target One',
  'acc-7': 'Target Seven',
  'acc-9': 'Target Nine',
}

const START_ACCOUNTS: readonly AdminAccount[] = [
  accountFixture({
    id: 'acc-1',
    display_name: 'Target One',
    identifiers: [
      { kind: 'username', value: 'target-one', proven: false, sign_in: true, contact: false, default_contact: false },
    ],
  }),
  accountFixture({
    id: 'acc-9',
    display_name: 'Target Nine',
    identifiers: [
      { kind: 'username', value: 'target-nine', proven: false, sign_in: true, contact: false, default_contact: false },
    ],
  }),
]

async function pickStartAccount(
  user: ReturnType<typeof userEvent.setup>,
  id: string,
): Promise<void> {
  const account = START_ACCOUNTS.find((row) => row.id === id)
  if (account === undefined) {
    throw new Error(`no start account ${id}`)
  }
  const label = screen.getByRole('combobox', {
    name: zhCN.impersonations.start.accountLabel,
  })
  await user.click(label)
  await user.type(label, account.display_name)
  await user.click(
    await screen.findByRole('option', {
      name: `${account.display_name} (${
        account.identifiers.find((identifier) => identifier.kind === 'username')
          ?.value ?? ''
      })`,
    }),
  )
}

function withPage(
  overrides: Partial<AdminBackend> = {},
): ReturnType<typeof createAdminOperations> {
  return createAdminOperations(
    makeAdminBackend({
      listTenants: () => Promise.resolve([TENANT]),
      listAccounts: () =>
        Promise.resolve({ accounts: [...START_ACCOUNTS], next_cursor: '' }),
      getAccount: ({ id }) => {
        const accountId = id ?? ''
        return Promise.resolve(
          accountFixture({
            id: accountId,
            display_name: ACCOUNT_NAMES[accountId] ?? accountId,
          }),
        )
      },
      ...overrides,
    }),
  )
}

beforeEach(() => {
  bindRequestFn(idleCallerSession)
})

describe('ImpersonationsPage', () => {
  it('raises its compact actions to the 44px floor under a coarse pointer', async () => {
    const user = userEvent.setup()
    const idle = renderWithProviders(
      <ImpersonationsPage />,
      {
        operations: withPage({
          listImpersonations: () => Promise.resolve([record()]),
        }),
      },
    )
    const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
    await pickTenant(user)
    const terminate = await screen.findByRole('button', { name: zhCN.impersonations.terminate })
    expect(hasCoarsePointerTouchTarget(refresh)).toBe(true)
    expect(hasCoarsePointerTouchTarget(terminate)).toBe(true)
    idle.unmount()

    const backend = makeCallerSessionBackend()
    backend.setGrant({
      id: 'imp-1',
      target: 'acc-1',
      tenant: 'acme',
      starts_at: '2026-10-15T12:00:00Z',
      ends_at: '2026-10-15T13:00:00Z',
    })
    bindRequestFn(backend.fn)
    renderWithProviders(<ImpersonationsPage />, { operations: withPage() })
    const end = await screen.findByRole('button', { name: zhCN.impersonations.current.end })
    expect(hasCoarsePointerTouchTarget(end)).toBe(true)
  })

  it('prompts for a tenant before listing', () => {
    const operations = withPage()
    renderWithProviders(<ImpersonationsPage />, { operations })
    expect(screen.getByText(zhCN.impersonations.needTenant.title)).toBeInTheDocument()
  })

  it('opens on the tenant named by the query string', async () => {
    window.history.replaceState({}, '', '/admin/impersonations?tenant=acme')
    try {
      const operations = withPage()
      renderWithProviders(<ImpersonationsPage />, { operations })
      expect(await screen.findByText(zhCN.impersonations.empty.title)).toBeInTheDocument()
      expect(screen.queryByText(zhCN.impersonations.needTenant.title)).toBeNull()
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('renders the empty state when there is no record', async () => {
    const user = userEvent.setup()
    const operations = withPage()
    renderWithProviders(<ImpersonationsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText(zhCN.impersonations.empty.title)).toBeInTheDocument()
  })

  it('lists records and marks an in-force one', async () => {
    const user = userEvent.setup()
    const operations = withPage({
      listImpersonations: () => Promise.resolve([record()]),
    })
    renderWithProviders(<ImpersonationsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText('Staff One')).toBeInTheDocument()
    expect(screen.getByText('Target One')).toBeInTheDocument()
    expect(screen.getByText(zhCN.impersonations.inForce)).toBeInTheDocument()
  })

  it('declares the designed column priorities and caps the account identities', async () => {
    const user = userEvent.setup()
    const operations = withPage({
      listImpersonations: () => Promise.resolve([record()]),
    })
    renderWithProviders(<ImpersonationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Target One')

    const header = (label: string): Element =>
      screen.getByRole('columnheader', { name: label })
    expect(columnVisibility(header(zhCN.impersonations.column.actor))).toEqual({
      hiddenAt: 0,
      visibleFrom: 900,
    })
    expect(columnVisibility(header(zhCN.impersonations.column.target))).toBeNull()
    expect(columnVisibility(header(zhCN.impersonations.column.tenant))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
    })
    expect(columnVisibility(header(zhCN.impersonations.column.started))).toBeNull()
    expect(columnVisibility(header(zhCN.impersonations.column.ends))).toEqual({
      hiddenAt: 0,
      visibleFrom: 1200,
    })
    expect(columnVisibility(header(zhCN.impersonations.column.state))).toEqual({
      hiddenAt: 0,
      visibleFrom: 900,
    })
    expect(columnVisibility(header(zhCN.impersonations.column.actions))).toBeNull()

    const targetCell = screen.getByText('Target One').closest('td')
    expect(targetCell).not.toBeNull()
    expect(containerMaxWidthOf(targetCell!.firstElementChild as Element)).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )
    const actorCell = screen.getByText('Staff One').closest('td')
    expect(actorCell).not.toBeNull()
    expect(actorCell!.firstElementChild).toHaveStyle({ maxWidth: '160px' })
    expect(
      within(actorCell as HTMLElement).queryByRole('button'),
    ).toBeNull()
  })

  it('keeps the recorded ids marked unresolved when the account read is refused', async () => {
    const user = userEvent.setup()
    const recordHalf = renderWithProviders(<ImpersonationsPage />, {
      operations: withPage({
        listImpersonations: () => Promise.resolve([record()]),
        getAccount: () => Promise.reject(new Error('forbidden')),
      }),
    })
    await pickTenant(user)
    await waitForCondition(() =>
      expect(
        screen.getAllByTitle(zhCN.common.accountUnresolved),
      ).toHaveLength(2),
    )
    const recordCells = screen.getAllByTitle(zhCN.common.accountUnresolved)
    for (const cell of recordCells) {
      expect(cell).toHaveTextContent(zhCN.common.accountUnavailable)
    }
    expect(screen.queryByText('staff-1')).toBeNull()
    expect(screen.queryByText('acc-1')).toBeNull()
    recordHalf.unmount()

    const backend = makeCallerSessionBackend()
    backend.setGrant({
      id: 'imp-1',
      target: 'acc-7',
      tenant: 'acme',
      starts_at: '2026-10-15T12:00:00Z',
      ends_at: '2026-10-15T13:00:00Z',
    })
    bindRequestFn(backend.fn)
    renderWithProviders(<ImpersonationsPage />, {
      operations: withPage({ getAccount: () => Promise.reject(new Error('forbidden')) }),
    })
    await waitForCondition(() =>
      expect(screen.getAllByTitle(zhCN.common.accountUnresolved)).toHaveLength(
        1,
      ),
    )
    expect(
      screen.getByTitle(zhCN.common.accountUnresolved),
    ).toHaveTextContent(zhCN.common.accountUnavailable)
    expect(screen.queryByText('acc-7')).toBeNull()
  })

  it('marks an ended record with its reason and disables termination', async () => {
    const user = userEvent.setup()
    const operations = withPage({
      listImpersonations: () =>
        Promise.resolve([
          record({ ended_at: '2026-10-15T12:30:00Z', end_reason: 'revoked' }),
        ]),
    })
    renderWithProviders(<ImpersonationsPage />, { operations })
    await pickTenant(user)
    expect(
      await screen.findByText(
        fill(zhCN.impersonations.ended, {
          reason: zhCN.impersonations.endReason.revoked,
        }),
      ),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: zhCN.impersonations.terminate })).toHaveAttribute('aria-disabled', 'true')
  })

  it('keeps the ended record’s terminate control focusable and explains why', async () => {
    const user = userEvent.setup()
    const revokeImpersonation = vi.fn(() => Promise.resolve())
    const operations = withPage({
      listImpersonations: () =>
        Promise.resolve([
          record({ ended_at: '2026-10-15T12:30:00Z', end_reason: 'revoked' }),
        ]),
      revokeImpersonation,
    })
    renderWithProviders(<ImpersonationsPage />, { operations })
    await pickTenant(user)
    const terminate = await screen.findByRole('button', {
      name: zhCN.impersonations.terminate,
    })

    terminate.focus()
    expect(terminate).toHaveFocus()
    expect(terminate).toHaveAttribute('aria-disabled', 'true')
    expect(terminate).toHaveAccessibleDescription(
      zhCN.impersonations.terminateEnded,
    )

    await user.click(terminate)
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    await act(async () => {
      await Promise.resolve()
    })
    expect(revokeImpersonation).not.toHaveBeenCalled()
  })

  it('keeps the start submit focusable while its precondition is unmet', async () => {
    const user = userEvent.setup()
    renderWithProviders(<ImpersonationsPage />, { operations: withPage() })
    await screen.findByText(zhCN.impersonations.current.none)

    const submit = screen.getByRole('button', {
      name: zhCN.impersonations.start.submit,
    })
    submit.focus()
    expect(submit).toHaveFocus()
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    expect(submit).toHaveAccessibleDescription(
      zhCN.impersonations.start.tenantHint,
    )

    await pickTenant(user)
    const withTenant = screen.getByRole('button', {
      name: zhCN.impersonations.start.submit,
    })
    expect(withTenant).toHaveAttribute('aria-disabled', 'true')
    expect(withTenant).toHaveAccessibleDescription(
      zhCN.impersonations.start.accountRequired,
    )
    withTenant.focus()
    expect(withTenant).toHaveFocus()
  })

  it('terminates an in-force impersonation and re-reads the session status', async () => {
    const user = userEvent.setup()
    const ended: string[] = []
    let statusReads = 0
    const countingIdle: RequestFn = <T,>(
      path: string,
      options?: RequestOptions,
    ) => {
      if (path === '/api/v1/auth/impersonation' && (options?.method ?? 'GET') === 'GET') {
        statusReads += 1
        return Promise.reject<T>({ status: 404, code: 'client.http.404' })
      }
      return Promise.resolve(undefined as T)
    }
    bindRequestFn(countingIdle)
    const operations = withPage({
      listImpersonations: () => Promise.resolve([record()]),
      revokeImpersonation: (input) => {
        ended.push(input.id)
        return Promise.resolve()
      },
    })
    renderWithProviders(<ImpersonationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Staff One')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.terminate }))
    await waitForCondition(() => {
      expect(ended).toEqual(['imp-1'])
    })
    await waitForCondition(() => expect(statusReads).toBeGreaterThan(1))
  })

  const NOTICE_IDENTIFIERS: AdminAccount['identifiers'] = [
    { kind: 'username', value: 'ada', proven: false, sign_in: true, contact: false, default_contact: false },
  ]

  function identityBackend(
    displayName: string,
    identifiers: AdminAccount['identifiers'],
  ): (input: { id?: string }) => Promise<AdminAccount> {
    return ({ id }) =>
      Promise.resolve(
        accountFixture({
          id: id ?? '',
          display_name: id === 'staff-1' ? 'Staff One' : displayName,
          identifiers: id === 'staff-1' ? [] : identifiers,
        }),
      )
  }

  async function settleIdentity(
    getAccount: ReturnType<typeof vi.fn>,
    account: string,
  ): Promise<void> {
    await waitForCondition(() =>
      expect(getAccount).toHaveBeenCalledWith({ id: account }),
    )
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
  }

  async function driveStartedNotice(
    user: ReturnType<typeof userEvent.setup>,
    displayName: string,
    identifiers: AdminAccount['identifiers'],
    expected: string,
  ): Promise<HTMLElement> {
    const backend = makeCallerSessionBackend()
    bindRequestFn(backend.fn)
    const getAccount = vi.fn(identityBackend(displayName, identifiers))
    renderWithProviders(<ImpersonationsPage />, {
      operations: withPage({ getAccount }),
    })
    await screen.findByText(zhCN.impersonations.current.none)
    await pickTenant(user)
    await pickStartAccount(user, 'acc-1')
    await settleIdentity(getAccount, 'acc-1')
    await user.click(
      screen.getByRole('button', { name: zhCN.impersonations.start.submit }),
    )
    return screen.findByText(expected)
  }

  async function driveEndedNotice(
    user: ReturnType<typeof userEvent.setup>,
    displayName: string,
    identifiers: AdminAccount['identifiers'],
    expected: string,
  ): Promise<HTMLElement> {
    const backend = makeCallerSessionBackend()
    backend.setGrant({
      id: 'imp-1',
      target: 'acc-7',
      tenant: 'acme',
      starts_at: '2026-10-15T12:00:00Z',
      ends_at: '2026-10-15T13:00:00Z',
    })
    bindRequestFn(backend.fn)
    const getAccount = vi.fn(identityBackend(displayName, identifiers))
    renderWithProviders(<ImpersonationsPage />, {
      operations: withPage({ getAccount }),
    })
    await screen.findByRole('button', {
      name: zhCN.impersonations.current.end,
    })
    await settleIdentity(getAccount, 'acc-7')
    await user.click(
      screen.getByRole('button', { name: zhCN.impersonations.current.end }),
    )
    return screen.findByText(expected)
  }

  async function driveTerminatedNotice(
    user: ReturnType<typeof userEvent.setup>,
    displayName: string,
    identifiers: AdminAccount['identifiers'],
    expected: string,
  ): Promise<HTMLElement> {
    const getAccount = vi.fn(identityBackend(displayName, identifiers))
    const operations = withPage({
      listImpersonations: () => Promise.resolve([record()]),
      getAccount,
      revokeImpersonation: () => Promise.resolve(),
    })
    renderWithProviders(<ImpersonationsPage />, { operations })
    await pickTenant(user)
    await screen.findByRole('button', { name: zhCN.impersonations.terminate })
    await settleIdentity(getAccount, 'acc-1')
    await user.click(
      screen.getByRole('button', { name: zhCN.impersonations.terminate }),
    )
    return screen.findByText(expected)
  }

  describe.each([
    {
      label: 'started',
      named: zhCN.impersonations.current.started,
      unnamed: zhCN.impersonations.current.startedUnnamed,
      displayName: 'Target One',
      drive: driveStartedNotice,
    },
    {
      label: 'ended',
      named: zhCN.impersonations.current.ended,
      unnamed: zhCN.impersonations.current.endedUnnamed,
      displayName: 'Target Seven',
      drive: driveEndedNotice,
    },
    {
      label: 'terminated',
      named: zhCN.impersonations.terminated,
      unnamed: zhCN.impersonations.terminatedUnnamed,
      displayName: 'Target One',
      drive: driveTerminatedNotice,
    },
  ])('$label notice', ({ named, unnamed, displayName, drive }) => {
    it('names the account by display name and first identifier, never the id', async () => {
      const user = userEvent.setup()
      const notice = await drive(
        user,
        displayName,
        NOTICE_IDENTIFIERS,
        fill(named, {
          account: byNameAndIdentifier(displayName, 'ada'),
        }),
      )
      expect(notice).not.toHaveTextContent('acc-1')
      expect(notice).not.toHaveTextContent('acc-7')
    })

    it('names the account by its first identifier when the display name is empty, never the id', async () => {
      const user = userEvent.setup()
      const notice = await drive(
        user,
        '',
        NOTICE_IDENTIFIERS,
        fill(named, { account: byIdentifier('ada') }),
      )
      expect(notice).not.toHaveTextContent('acc-1')
      expect(notice).not.toHaveTextContent('acc-7')
    })

    it('uses the neutral sentence when neither resolves, never the id', async () => {
      const user = userEvent.setup()
      const notice = await drive(user, '', [], unnamed)
      expect(notice).not.toHaveTextContent('acc-1')
      expect(notice).not.toHaveTextContent('acc-7')
    })
  })

  it('disables the refresh while a record termination is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    let statusReads = 0
    const countingIdle: RequestFn = <T,>(
      path: string,
      options?: RequestOptions,
    ) => {
      if (path === '/api/v1/auth/impersonation' && (options?.method ?? 'GET') === 'GET') {
        statusReads += 1
        return Promise.reject<T>({ status: 404, code: 'client.http.404' })
      }
      return Promise.resolve(undefined as T)
    }
    bindRequestFn(countingIdle)
    const listImpersonations = vi.fn(() => Promise.resolve([record()]))
    const operations = withPage({
      listImpersonations,
      revokeImpersonation: () => pending.promise,
    })
    renderWithProviders(<ImpersonationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Staff One')
    const readCounts = () =>
      [statusReads, listImpersonations.mock.calls.length] as const
    const readsBefore = readCounts()

    await user.click(screen.getByRole('button', { name: zhCN.impersonations.terminate }))
    await expectRefreshInert(readCounts, readsBefore)

    await act(async () => {
      pending.resolve()
    })
  })

  it('holds sibling terminate controls back while one record is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const revokeImpersonation = vi.fn(() => pending.promise)
    const operations = withPage({
      listImpersonations: () =>
        Promise.resolve([
          record({ id: 'imp-1', actor: 'staff-1', target: 'acc-1' }),
          record({ id: 'imp-2', actor: 'staff-2', target: 'acc-7' }),
        ]),
      revokeImpersonation,
    })
    renderWithProviders(<ImpersonationsPage />, { operations })
    await pickTenant(user)

    const first = (await screen.findByText('Target One')).closest('tr')
    const second = (await screen.findByText('Target Seven')).closest('tr')
    expect(first).not.toBeNull()
    expect(second).not.toBeNull()
    const terminate = (row: Element): HTMLElement =>
      within(row as HTMLElement).getByRole('button', {
        name: zhCN.impersonations.terminate,
      })

    await user.click(terminate(first as Element))
    expect(revokeImpersonation).toHaveBeenCalledTimes(1)

    const sibling = terminate(second as Element)
    expect(sibling).toHaveAttribute('aria-disabled', 'true')
    expect(sibling).not.toHaveAttribute('aria-busy', 'true')
    expect(sibling).not.toHaveAccessibleDescription()

    await user.click(sibling)
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(revokeImpersonation).toHaveBeenCalledTimes(1)

    await act(async () => {
      pending.resolve()
    })
  })

  it('disables the refresh while an end attempt is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    let statusReads = 0
    const ending: RequestFn = <T,>(
      path: string,
      options?: RequestOptions,
    ) => {
      const method = options?.method ?? 'GET'
      if (path === '/api/v1/auth/impersonation') {
        if (method === 'GET') {
          statusReads += 1
          return Promise.resolve({
            grant: {
              id: 'imp-1',
              target: 'acc-1',
              tenant: 'acme',
              starts_at: '2026-10-15T12:00:00Z',
              ends_at: '2026-10-15T13:00:00Z',
            },
          } as T)
        }
        if (method === 'DELETE') {
          return pending.promise.then(() => undefined as T)
        }
      }
      return Promise.resolve(undefined as T)
    }
    bindRequestFn(ending)
    renderWithProviders(<ImpersonationsPage />, { operations: withPage() })
    const end = await screen.findByRole('button', { name: zhCN.impersonations.current.end })
    const readCounts = () => [statusReads, 0] as const
    const readsBefore = readCounts()

    await user.click(end)
    await expectRefreshInert(readCounts, readsBefore)

    await act(async () => {
      pending.resolve()
    })
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const user = userEvent.setup()
    const operations = withPage({
      listImpersonations: () => Promise.reject(new Error('boom')),
    })
    renderWithProviders(<ImpersonationsPage />, { operations })
    await pickTenant(user)
    expect(
      await screen.findByText(zhCN.impersonations.error.title),
    ).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.impersonations.error.description),
    ).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the read is refused with 403', async () => {
    const user = userEvent.setup()
    const operations = withPage({
      listImpersonations: () =>
        Promise.reject({ status: 403, code: 'client.http.403' }),
    })
    renderWithProviders(<ImpersonationsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText(zhCN.forbidden.title)).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.impersonations.retry }),
    ).toBeNull()
  })

  it('re-reads the session status and the record list on refresh', async () => {
    const user = userEvent.setup()
    const backend = makeCallerSessionBackend()
    bindRequestFn(backend.fn)
    const listImpersonations = vi.fn(() => Promise.resolve([record()]))
    const operations = withPage({ listImpersonations })
    renderWithProviders(<ImpersonationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Staff One')

    const statusBefore = backend.statusReads()
    const recordsBefore = listImpersonations.mock.calls.length
    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )
    await waitForCondition(() =>
      expect(backend.statusReads()).toBe(statusBefore + 1),
    )
    await waitForCondition(() =>
      expect(listImpersonations.mock.calls.length).toBe(recordsBefore + 1),
    )
  })

  it('locks the record scope to a controlled tenant and hides the selector', async () => {
    const listImpersonations = vi.fn(() => Promise.resolve([record()]))
    renderWithProviders(<ImpersonationsPage tenant="acme" />, {
      operations: withPage({ listImpersonations }),
    })

    expect(await screen.findByText('Staff One')).toBeInTheDocument()
    expect(listImpersonations).toHaveBeenCalledWith({ tenant: 'acme' })
    expect(screen.queryByLabelText(zhCN.common.tenant)).toBeNull()
  })

  it('seeds from defaultTenant ahead of the URL and reports a re-selection', async () => {
    window.history.replaceState({}, '', '/admin/impersonations?tenant=globex')
    try {
      const user = userEvent.setup()
      const listImpersonations = vi.fn(() => Promise.resolve([record()]))
      const onTenantChange = vi.fn()
      renderWithProviders(
        <ImpersonationsPage
          defaultTenant="acme"
          onTenantChange={onTenantChange}
        />,
        {
          operations: withPage({
            listTenants: () =>
              Promise.resolve([
                TENANT,
                tenantFixture({ name: 'globex', display_name: 'Globex', status: 'active' }),
              ]),
            listImpersonations,
          }),
        },
      )

      await waitForCondition(() => {
        expect(listImpersonations).toHaveBeenCalledWith({ tenant: 'acme' })
      })
      await user.click(screen.getByLabelText(zhCN.common.tenant))
      await user.click(await screen.findByRole('option', { name: 'Globex' }))
      expect(onTenantChange).toHaveBeenCalledWith('globex')
      await waitForCondition(() => {
        expect(listImpersonations).toHaveBeenCalledWith({ tenant: 'globex' })
      })
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('leaves the caller-session surface ungated by the feature judgement', async () => {
    const user = userEvent.setup()
    const operations = withPage({
      listImpersonations: () => Promise.resolve([record()]),
    })
    renderWithProviders(<ImpersonationsPage />, {
      operations,
      hasFeature: (name) => name !== 'authn.impersonation',
    })

    expect(
      await screen.findByText(zhCN.impersonations.current.none),
    ).toBeInTheDocument()
    expect(
      screen.getByLabelText(zhCN.impersonations.start.accountLabel),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.impersonations.start.submit }),
    ).toBeInTheDocument()

    await pickTenant(user)
    expect(await screen.findByText('Staff One')).toBeInTheDocument()
  })

  it('translates a reason it knows and leaves an unknown one as recorded', async () => {
    const user = userEvent.setup()
    const i18n = createAdminUiI18n()
    i18n.addResourceBundle(
      'zh-CN',
      ADMIN_UI_NAMESPACE,
      { impersonations: { endReason: END_REASON_LABELS } },
      true,
      true,
    )
    const operations = withPage({
      listImpersonations: () =>
        Promise.resolve([
          record({ ended_at: '2026-10-15T12:30:00Z', end_reason: 'revoked' }),
          record({
            id: 'imp-2',
            ended_at: '2026-10-15T12:40:00Z',
            end_reason: 'mystery',
          }),
        ]),
    })
    renderWithProviders(<ImpersonationsPage />, { operations, i18n })
    await pickTenant(user)

    expect(
      await screen.findByText(
        fill(zhCN.impersonations.ended, {
          reason: END_REASON_LABELS.revoked,
        }),
      ),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        fill(zhCN.impersonations.ended, { reason: 'mystery' }),
      ),
    ).toBeInTheDocument()
  })

  it('has no axe violations', async () => {
    const user = userEvent.setup()
    const operations = withPage({
      listImpersonations: () => Promise.resolve([record()]),
    })
    renderWithProviders(<ImpersonationsPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Staff One')
    await expectNoAxeViolations()
  })

  it('has no axe violations with an impersonation in force', async () => {
    const backend = makeCallerSessionBackend()
    backend.setGrant({
      id: 'imp-1',
      target: 'acc-7',
      tenant: 'acme',
      starts_at: '2026-10-15T12:00:00Z',
      ends_at: '2026-10-15T13:00:00Z',
    })
    bindRequestFn(backend.fn)
    renderWithProviders(<ImpersonationsPage />, { operations: withPage() })
    await screen.findByText(targetLine('Target Seven'))
    await expectNoAxeViolations()
  })


  it('shows the not-impersonating state and the start form when no impersonation is in force', async () => {
    renderWithProviders(<ImpersonationsPage />, { operations: withPage() })
    expect(
      await screen.findByText(zhCN.impersonations.current.none),
    ).toBeInTheDocument()
    expect(screen.getByLabelText(zhCN.impersonations.start.accountLabel)).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.impersonations.start.precondition),
    ).toBeInTheDocument()
    expect(screen.getByLabelText(zhCN.common.tenant)).toBeInTheDocument()
    expect(screen.queryByLabelText('Tenant identifier')).toBeNull()
    expect(
      screen.getByText(zhCN.impersonations.start.tenantHint),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.impersonations.start.submit }),
    ).toHaveAttribute('aria-disabled', 'true')
  })

  it('states the impersonation precondition without the configuration key or a fixed wait', async () => {
    const cases = [
      { language: 'zh-CN', copy: zhCN.impersonations.start.precondition },
      { language: 'en-US', copy: enUS.impersonations.start.precondition },
    ] as const
    for (const { language, copy } of cases) {
      const { unmount } = renderWithProviders(<ImpersonationsPage />, {
        language,
        operations: withPage(),
      })
      const rendered = await screen.findByText(copy)
      const text = rendered.textContent ?? ''
      expect(text).not.toContain('authn.mfa')
      expect(text).not.toContain('sensitive-min-age')
      expect(text).not.toMatch(/\d+\s*(?:hours?|hrs?|h\b|小时)/i)
      unmount()
    }
  })

  it('withholds the start until a tenant is selected', async () => {
    const backend = makeCallerSessionBackend()
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    renderWithProviders(<ImpersonationsPage />, { operations: withPage() })

    await screen.findByText(zhCN.impersonations.current.none)
    await pickStartAccount(user, 'acc-9')
    const submit = screen.getByRole('button', { name: zhCN.impersonations.start.submit })
    expect(submit).toHaveAttribute('aria-disabled', 'true')
    fireEvent.click(submit)
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(backend.started).toEqual([])

    await pickTenant(user)
    expect(submit).not.toHaveAttribute('aria-disabled', 'true')
  })

  it("starts an impersonation, sending the endpoint's request body", async () => {
    const backend = makeCallerSessionBackend()
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    renderWithProviders(<ImpersonationsPage />, { operations: withPage() })

    await screen.findByText(zhCN.impersonations.current.none)
    await pickTenant(user)
    await pickStartAccount(user, 'acc-9')
    await user.type(screen.getByLabelText(zhCN.impersonations.start.reasonLabel), 'investigate ticket')
    await user.type(
      screen.getByLabelText(zhCN.impersonations.start.ttlLabel),
      '3600',
    )
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

    await waitForCondition(() => {
      expect(backend.started).toEqual([
        {
          account: 'acc-9',
          tenant: 'acme',
          reason: 'investigate ticket',
          ttl_seconds: 3600,
        },
      ])
    })
    expect(await screen.findByText(targetLine('Target One'))).toBeInTheDocument()
    expect(
      screen.queryByLabelText(zhCN.impersonations.start.accountLabel),
    ).not.toBeInTheDocument()
  })

  it('sends the selected tenant and omits the other optional fields when they are blank', async () => {
    const backend = makeCallerSessionBackend()
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    renderWithProviders(<ImpersonationsPage />, { operations: withPage() })

    await screen.findByText(zhCN.impersonations.current.none)
    await pickTenant(user)
    await pickStartAccount(user, 'acc-9')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

    await waitForCondition(() => {
      expect(backend.started).toEqual([{ account: 'acc-9', tenant: 'acme' }])
    })
  })

  it('reports a settled start to the host without waiting for it', async () => {
    const backend = makeCallerSessionBackend()
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    const onChange = vi.fn(() => new Promise<void>(() => undefined))
    renderWithProviders(
      <AdminImpersonationChangeProvider onImpersonationChange={onChange}>
        <ImpersonationsPage />
      </AdminImpersonationChangeProvider>,
      { operations: withPage() },
    )

    await screen.findByText(zhCN.impersonations.current.none)
    await pickTenant(user)
    await pickStartAccount(user, 'acc-1')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

    await waitForCondition(() => expect(onChange).toHaveBeenCalledWith('started'))
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(
      await screen.findByText(fill(zhCN.impersonations.current.started, { account: byName('Target One') })),
    ).toBeInTheDocument()
  })

  it("wraps the host's rejection, leaving the settled start a success", async () => {
    const backend = makeCallerSessionBackend()
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    const onChange = vi.fn(() => Promise.reject(new Error('host action failed')))
    renderWithProviders(
      <AdminImpersonationChangeProvider onImpersonationChange={onChange}>
        <ImpersonationsPage />
      </AdminImpersonationChangeProvider>,
      { operations: withPage() },
    )

    await screen.findByText(zhCN.impersonations.current.none)
    await pickTenant(user)
    await pickStartAccount(user, 'acc-1')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

    await waitForCondition(() => expect(onChange).toHaveBeenCalledWith('started'))
    expect(
      await screen.findByText(fill(zhCN.impersonations.current.started, { account: byName('Target One') })),
    ).toBeInTheDocument()
    expect(screen.queryByText('host action failed')).toBeNull()
  })

  it('reports a settled end to the host', async () => {
    const backend = makeCallerSessionBackend()
    backend.setGrant({
      id: 'imp-1',
      target: 'acc-7',
      tenant: 'acme',
      starts_at: '2026-10-15T12:00:00Z',
      ends_at: '2026-10-15T13:00:00Z',
    })
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderWithProviders(
      <AdminImpersonationChangeProvider onImpersonationChange={onChange}>
        <ImpersonationsPage />
      </AdminImpersonationChangeProvider>,
      { operations: withPage() },
    )

    expect(
      await screen.findByText(targetLine('Target Seven')),
    ).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.current.end }))

    await waitForCondition(() => expect(onChange).toHaveBeenCalledWith('ended'))
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(
      await screen.findByText(fill(zhCN.impersonations.current.ended, { account: byName('Target Seven') })),
    ).toBeInTheDocument()
  })

  it('closes a bare 404 end as a settled end and reports it', async () => {
    let inForce = true
    let deletes = 0
    const grant = {
      id: 'imp-1',
      target: 'acc-7',
      tenant: 'acme',
      starts_at: '2026-10-15T12:00:00Z',
      ends_at: '2026-10-15T13:00:00Z',
    }
    const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      if (path !== '/api/v1/auth/impersonation') {
        return Promise.resolve(undefined as T)
      }
      const method = options?.method ?? 'GET'
      if (method === 'GET') {
        return inForce
          ? Promise.resolve({ grant } as T)
          : Promise.reject({ status: 404, code: 'client.http.404' })
      }
      if (method === 'DELETE') {
        deletes += 1
        inForce = false
        return Promise.reject({ status: 404, code: 'client.http.404' })
      }
      return Promise.resolve(undefined as T)
    }
    bindRequestFn(fn)
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderWithProviders(
      <AdminImpersonationChangeProvider onImpersonationChange={onChange}>
        <ImpersonationsPage />
      </AdminImpersonationChangeProvider>,
      { operations: withPage() },
    )

    expect(
      await screen.findByText(targetLine('Target Seven')),
    ).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.current.end }))

    await waitForCondition(() => expect(onChange).toHaveBeenCalledWith('ended'))
    expect(deletes).toBe(1)
    expect(
      await screen.findByText(fill(zhCN.impersonations.current.ended, { account: byName('Target Seven') })),
    ).toBeInTheDocument()
    expect(
      await screen.findByText(zhCN.impersonations.current.none),
    ).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('shows the impersonation in force and ends it', async () => {
    const backend = makeCallerSessionBackend()
    backend.setGrant({
      id: 'imp-1',
      target: 'acc-7',
      tenant: 'acme',
      starts_at: '2026-10-15T12:00:00Z',
      ends_at: '2026-10-15T13:00:00Z',
    })
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    renderWithProviders(<ImpersonationsPage />, { operations: withPage() })

    expect(await screen.findByText(targetLine('Target Seven'))).toBeInTheDocument()
    expect(
      screen.getByText(
        fill(zhCN.impersonations.current.tenant, { tenant: 'acme' }),
      ),
    ).toBeInTheDocument()
    expect(screen.queryByLabelText(zhCN.impersonations.start.accountLabel)).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: zhCN.impersonations.current.end }))
    await waitForCondition(() => {
      expect(backend.ended).toHaveLength(1)
    })
  })

  it('treats the status read\'s bare 404 as a settled "not impersonating" answer', async () => {
    let reads = 0
    const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      if (path !== '/api/v1/auth/impersonation') {
        return Promise.resolve(undefined as T)
      }
      if ((options?.method ?? 'GET') !== 'GET') {
        return Promise.resolve(undefined as T)
      }
      reads += 1
      return reads === 1
        ? Promise.resolve({
            grant: {
              id: 'imp-1',
              target: 'acc-1',
              tenant: 'acme',
              starts_at: '2026-10-15T12:00:00Z',
              ends_at: '2026-10-15T13:00:00Z',
            },
          } as T)
        : Promise.reject({ status: 404, code: 'client.http.404' })
    }
    bindRequestFn(fn)
    const user = userEvent.setup()
    renderWithProviders(<ImpersonationsPage />, { operations: withPage() })

    expect(await screen.findByText(targetLine('Target One'))).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: zhCN.common.refresh }))

    expect(await screen.findByText(zhCN.impersonations.current.none)).toBeInTheDocument()
    expect(screen.queryByText(targetLine('Target One'))).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('drops the cached grant and re-reads the status after a successful end', async () => {
    const grant = {
      id: 'imp-1',
      target: 'acc-7',
      tenant: 'acme',
      starts_at: '2026-10-15T12:00:00Z',
      ends_at: '2026-10-15T13:00:00Z',
    }
    let phase: 'in-force' | 'ended' = 'in-force'
    let releaseRead: () => void = () => undefined
    const endRead = new Promise<void>((resolve) => {
      releaseRead = resolve
    })
    let reads = 0
    let ended = 0
    const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      if (path !== '/api/v1/auth/impersonation') {
        return Promise.resolve(undefined as T)
      }
      const method = options?.method ?? 'GET'
      if (method === 'GET') {
        reads += 1
        if (phase === 'in-force') {
          return Promise.resolve({ grant } as T)
        }
        return endRead.then(() =>
          Promise.reject<T>({ status: 404, code: 'client.http.404' }),
        )
      }
      if (method === 'DELETE') {
        ended += 1
        phase = 'ended'
        return Promise.resolve(undefined as T)
      }
      return Promise.resolve(undefined as T)
    }
    bindRequestFn(fn)
    const user = userEvent.setup()
    renderWithProviders(<ImpersonationsPage />, { operations: withPage() })

    expect(await screen.findByText(targetLine('Target Seven'))).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.current.end }))
    await waitForCondition(() => expect(ended).toBe(1))

    await waitForCondition(() => {
      expect(screen.queryByText(targetLine('Target Seven'))).toBeNull()
    })
    expect(reads).toBeGreaterThan(1)

    releaseRead()
    expect(await screen.findByText(zhCN.impersonations.current.none)).toBeInTheDocument()

    const section = screen
      .getByRole('heading', { name: zhCN.impersonations.current.title })
      .closest('section')
    expect(section).not.toBeNull()
    expect(
      within(section as HTMLElement).getByText(fill(zhCN.impersonations.current.ended, { account: byName('Target Seven') })),
    ).toBeInTheDocument()
  })

  it('keeps the start confirmation in the section after the start form unmounts', async () => {
    const backend = makeCallerSessionBackend()
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    const listImpersonations = vi.fn(() => Promise.resolve([]))
    renderWithProviders(<ImpersonationsPage />, {
      operations: withPage({ listImpersonations }),
    })

    await screen.findByText(zhCN.impersonations.current.none)
    await pickTenant(user)
    await pickStartAccount(user, 'acc-9')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))
    await waitForCondition(() =>
      expect(listImpersonations.mock.calls.length).toBeGreaterThan(1),
    )

    const startedNotice = await screen.findByText(
      zhCN.impersonations.current.startedUnnamed,
    )
    expect(startedNotice).not.toHaveTextContent('acc-9')
    expect(startedNotice).not.toHaveTextContent('acc-1')
    expect(screen.queryByLabelText(zhCN.impersonations.start.accountLabel)).not.toBeInTheDocument()
    const section = screen
      .getByRole('heading', { name: zhCN.impersonations.current.title })
      .closest('section')
    expect(section).not.toBeNull()
    expect(
      within(section as HTMLElement).getByText(zhCN.impersonations.current.startedUnnamed),
    ).toBeInTheDocument()
  })

  it('hands a step-up-refused start to the elevation gate and reports the replay', async () => {
    const posts: unknown[] = []
    const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      const method = options?.method ?? 'GET'
      if (path === '/api/v1/auth/factors') {
        return Promise.resolve({
          factors: [
            {
              id: 'factor-1',
              kind: 'totp',
              label: 'Authenticator app',
              status: 'active',
              created_at: '2026-01-01T00:00:00Z',
            },
          ],
        } as T)
      }
      if (path === '/api/v1/auth/impersonation' && method === 'GET') {
        return Promise.reject({ status: 404, code: 'client.http.404' })
      }
      if (path === '/api/v1/auth/impersonation' && method === 'POST') {
        posts.push(options?.body)
        return posts.length === 1
          ? Promise.reject({
              status: 401,
              code: 'authn.insufficient_user_authentication',
              message: 'step up',
              auth: true,
            })
          : Promise.reject({
              status: 403,
              code: 'authn.forbidden',
              message: 'denied',
              auth: true,
            })
      }
      return Promise.resolve(undefined as T)
    }
    bindRequestFn(fn)
    const user = userEvent.setup()
    renderWithProviders(<ImpersonationsPage />, {
      operations: withPage(),
      session: createStepUpSession(),
    })

    await screen.findByText(zhCN.impersonations.current.none)
    await pickTenant(user)
    await pickStartAccount(user, 'acc-9')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

    expect(
      await screen.findByText(zhCN.errors.authn.forbidden),
    ).toBeInTheDocument()
    await waitForCondition(() => expect(posts).toHaveLength(2))
    expect(posts[1]).toEqual(posts[0])
    expect(posts[0]).toEqual({ account: 'acc-9', tenant: 'acme' })
  })


  it('fires the starting hook before the request and leaves a landed start to the host', async () => {
    const pending = deferred<unknown>()
    const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      const method = options?.method ?? 'GET'
      if (path === '/api/v1/auth/impersonation' && method === 'GET') {
        return Promise.reject({ status: 404, code: 'client.http.404' })
      }
      if (path === '/api/v1/auth/impersonation' && method === 'POST') {
        return pending.promise as Promise<T>
      }
      return Promise.resolve(undefined as T)
    }
    bindRequestFn(fn)
    const user = userEvent.setup()
    const release = vi.fn()
    const starting = vi.fn(() => release)
    const onChange = vi.fn()
    renderWithProviders(
      <AdminImpersonationChangeProvider onImpersonationChange={onChange}>
        <AdminImpersonationStartingProvider
          onImpersonationStarting={starting}
        >
          <ImpersonationsPage />
        </AdminImpersonationStartingProvider>
      </AdminImpersonationChangeProvider>,
      { operations: withPage() },
    )

    await screen.findByText(zhCN.impersonations.current.none)
    await pickTenant(user)
    await pickStartAccount(user, 'acc-1')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

    await waitForCondition(() => expect(starting).toHaveBeenCalledTimes(1))
    expect(release).not.toHaveBeenCalled()

    await act(async () => {
      pending.resolve({
        grant: {
          id: 'imp-1',
          target: 'acc-1',
          tenant: 'acme',
          starts_at: '2026-10-15T12:00:00Z',
          ends_at: '2026-10-15T13:00:00Z',
        },
      })
    })

    await waitForCondition(() => expect(onChange).toHaveBeenCalledWith('started'))
    expect(release).not.toHaveBeenCalled()
  })

  it('releases the starting hook when the write is refused', async () => {
    const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      const method = options?.method ?? 'GET'
      if (path === '/api/v1/auth/impersonation' && method === 'GET') {
        return Promise.reject({ status: 404, code: 'client.http.404' })
      }
      if (path === '/api/v1/auth/impersonation' && method === 'POST') {
        return Promise.reject({
          status: 403,
          code: 'authn.forbidden',
          message: 'denied',
          auth: true,
        })
      }
      return Promise.resolve(undefined as T)
    }
    bindRequestFn(fn)
    const user = userEvent.setup()
    const release = vi.fn()
    const starting = vi.fn(() => release)
    renderWithProviders(
      <AdminImpersonationStartingProvider onImpersonationStarting={starting}>
        <ImpersonationsPage />
      </AdminImpersonationStartingProvider>,
      { operations: withPage() },
    )

    await screen.findByText(zhCN.impersonations.current.none)
    await pickTenant(user)
    await pickStartAccount(user, 'acc-9')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

    expect(await screen.findByText(zhCN.errors.authn.forbidden)).toBeInTheDocument()
    await waitForCondition(() => expect(release).toHaveBeenCalledTimes(1))
  })

  it('releases the blocked attempt and fires the hook again for the step-up replay', async () => {
    const posts: unknown[] = []
    const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      const method = options?.method ?? 'GET'
      if (path === '/api/v1/auth/factors') {
        return Promise.resolve({
          factors: [
            {
              id: 'factor-1',
              kind: 'totp',
              label: 'Authenticator app',
              status: 'active',
              created_at: '2026-01-01T00:00:00Z',
            },
          ],
        } as T)
      }
      if (path === '/api/v1/auth/impersonation' && method === 'GET') {
        return Promise.reject({ status: 404, code: 'client.http.404' })
      }
      if (path === '/api/v1/auth/impersonation' && method === 'POST') {
        posts.push(options?.body)
        return posts.length === 1
          ? Promise.reject({
              status: 401,
              code: 'authn.insufficient_user_authentication',
              message: 'step up',
              auth: true,
            })
          : Promise.resolve({
              grant: {
                id: 'imp-1',
                target: 'acc-9',
                tenant: 'acme',
                starts_at: '2026-10-15T12:00:00Z',
                ends_at: '2026-10-15T13:00:00Z',
              },
            } as T)
      }
      return Promise.resolve(undefined as T)
    }
    bindRequestFn(fn)
    const user = userEvent.setup()
    const releases: (() => void)[] = []
    const starting = vi.fn(() => {
      const release = vi.fn()
      releases.push(release)
      return release
    })
    renderWithProviders(
      <AdminImpersonationStartingProvider onImpersonationStarting={starting}>
        <ImpersonationsPage />
      </AdminImpersonationStartingProvider>,
      { operations: withPage(), session: createStepUpSession() },
    )

    await screen.findByText(zhCN.impersonations.current.none)
    await pickTenant(user)
    await pickStartAccount(user, 'acc-9')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

    await waitForCondition(() => expect(starting).toHaveBeenCalledTimes(2))
    expect(releases[0]).toHaveBeenCalledTimes(1)
    expect(releases[1]).not.toHaveBeenCalled()
    expect(posts[1]).toEqual(posts[0])
  })


  it('renders the impersonation-restricted refusal without naming one cause', async () => {
    const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      const method = options?.method ?? 'GET'
      if (path === '/api/v1/auth/impersonation' && method === 'GET') {
        return Promise.reject({ status: 404, code: 'client.http.404' })
      }
      if (path === '/api/v1/auth/impersonation' && method === 'POST') {
        return Promise.reject({
          status: 403,
          code: 'authn.impersonation_restricted',
          message: 'restricted',
          auth: true,
        })
      }
      return Promise.resolve(undefined as T)
    }
    bindRequestFn(fn)
    const user = userEvent.setup()
    renderWithProviders(<ImpersonationsPage />, { operations: withPage() })

    await screen.findByText(zhCN.impersonations.current.none)
    await pickTenant(user)
    await pickStartAccount(user, 'acc-9')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

    const alert = await screen.findByRole('alert')
    const rendered = alert.textContent ?? ''
    expect(rendered).toContain(zhCN.errors.authn.impersonation_restricted)
    expect(rendered).not.toContain('during an impersonation')
    expect(rendered).not.toContain('while impersonating')
  })

  it('states the same requirements in the English refusal', async () => {
    const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      const method = options?.method ?? 'GET'
      if (path === '/api/v1/auth/impersonation' && method === 'GET') {
        return Promise.reject({ status: 404, code: 'client.http.404' })
      }
      if (path === '/api/v1/auth/impersonation' && method === 'POST') {
        return Promise.reject({
          status: 403,
          code: 'authn.impersonation_restricted',
          message: 'restricted',
          auth: true,
        })
      }
      return Promise.resolve(undefined as T)
    }
    bindRequestFn(fn)
    const user = userEvent.setup()
    renderWithProviders(<ImpersonationsPage />, {
      language: 'en-US',
      operations: withPage(),
    })

    await screen.findByText('You are not impersonating any account.')
    await user.click(screen.getByLabelText('Organization'))
    await user.click(await screen.findByRole('option', { name: 'Acme' }))
    const target = screen.getByRole('combobox', { name: 'Target account' })
    await user.click(target)
    await user.type(target, 'Target Nine')
    await user.click(
      await screen.findByRole('option', { name: 'Target Nine (target-nine)' }),
    )
    await user.click(screen.getByRole('button', { name: 'Start impersonation' }))

    const alert = await screen.findByRole('alert')
    const rendered = alert.textContent ?? ''
    expect(rendered).toContain('member of the organization')
    expect(rendered).toContain('enabled')
    expect(rendered).toContain('unprotected')
    expect(rendered).toContain('not be impersonating another account')
    expect(rendered).not.toContain('during an impersonation')
    expect(rendered).not.toContain('while impersonating')
  })


  it('collapses the record half and issues no record read when a grant is in force', async () => {
    const backend = makeCallerSessionBackend()
    backend.setGrant({
      id: 'imp-1',
      target: 'acc-1',
      tenant: 'acme',
      starts_at: '2026-10-15T12:00:00Z',
      ends_at: '2026-10-15T13:00:00Z',
    })
    bindRequestFn(backend.fn)
    const listImpersonations = vi.fn(() => Promise.resolve([record()]))
    window.history.replaceState({}, '', '/admin/impersonations?tenant=acme')
    try {
      renderWithProviders(<ImpersonationsPage />, {
        operations: withPage({ listImpersonations }),
      })

      expect(
        await screen.findByText(targetLine('Target One')),
      ).toBeInTheDocument()
      expect(screen.getByText(RECORDS_UNAVAILABLE)).toBeInTheDocument()
      expect(screen.queryByLabelText(zhCN.common.tenant)).toBeNull()
      expect(screen.queryByText(zhCN.impersonations.empty.title)).toBeNull()
      expect(screen.queryByRole('button', { name: zhCN.impersonations.terminate })).toBeNull()
      expect(listImpersonations).not.toHaveBeenCalled()
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('renders the record half while the status read is still pending', async () => {
    const fn: RequestFn = <T,>(
      path: string,
      options?: RequestOptions,
    ) => {
      if (path === '/api/v1/auth/impersonation' && (options?.method ?? 'GET') === 'GET') {
        return new Promise<T>(() => undefined)
      }
      return Promise.resolve(undefined as T)
    }
    bindRequestFn(fn)
    window.history.replaceState({}, '', '/admin/impersonations?tenant=acme')
    try {
      renderWithProviders(<ImpersonationsPage />, { operations: withPage() })
      expect(screen.getByLabelText(zhCN.common.tenant)).toBeInTheDocument()
      expect(
        screen.getByRole('status', { name: zhCN.impersonations.loading }),
      ).toBeInTheDocument()
      expect(screen.queryByText(RECORDS_UNAVAILABLE)).toBeNull()
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('renders the record half when the status read fails with a non-404 answer', async () => {
    const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      if (path === '/api/v1/auth/impersonation' && (options?.method ?? 'GET') === 'GET') {
        return Promise.reject({ status: 500, code: 'client.http.500' })
      }
      return Promise.resolve(undefined as T)
    }
    bindRequestFn(fn)
    const user = userEvent.setup()
    const listImpersonations = vi.fn(() => Promise.resolve([record()]))
    renderWithProviders(<ImpersonationsPage />, {
      operations: withPage({ listImpersonations }),
    })

    expect(
      await screen.findByText(zhCN.impersonations.current.error.title),
    ).toBeInTheDocument()
    expect(screen.getByLabelText(zhCN.common.tenant)).toBeInTheDocument()
    await pickTenant(user)
    expect(await screen.findByText('Staff One')).toBeInTheDocument()
    expect(listImpersonations).toHaveBeenCalled()
    expect(screen.queryByText(RECORDS_UNAVAILABLE)).toBeNull()
  })

  it('returns the record half after an end and reads again instead of the cached list', async () => {
    const backend = makeCallerSessionBackend()
    bindRequestFn(backend.fn)
    let reads = 0
    let release: () => void = () => undefined
    const held = new Promise<void>((resolve) => {
      release = resolve
    })
    const listImpersonations = vi.fn(() => {
      reads += 1
      if (reads <= 2) {
        return Promise.resolve([record()])
      }
      return held.then(() => [record({ target: 'acc-7' })])
    })
    window.history.replaceState({}, '', '/admin/impersonations?tenant=acme')
    try {
      const user = userEvent.setup()
      renderWithProviders(<ImpersonationsPage />, {
        operations: withPage({ listImpersonations }),
      })

      expect(await screen.findByText('Staff One')).toBeInTheDocument()

      await pickStartAccount(user, 'acc-9')
      await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))
      expect(await screen.findByText(RECORDS_UNAVAILABLE)).toBeInTheDocument()

      await user.click(screen.getByRole('button', { name: zhCN.impersonations.current.end }))
      await waitForCondition(() => expect(backend.ended).toHaveLength(1))

      await waitForCondition(() => expect(reads).toBe(3))
      expect(screen.queryByText(RECORDS_UNAVAILABLE)).toBeNull()
      expect(screen.getByLabelText(zhCN.common.tenant)).toBeInTheDocument()
      expect(screen.queryByText('Staff One')).toBeNull()
      expect(
        screen.getByRole('status', { name: zhCN.impersonations.loading }),
      ).toBeInTheDocument()

      release()
      expect(await screen.findByText('Target Seven')).toBeInTheDocument()
      expect(screen.queryByText('Target One')).toBeNull()
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('renders an unseasoned-factor refusal in the current scope without a step-up dialog', async () => {
    const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      const method = options?.method ?? 'GET'
      if (path === '/api/v1/auth/factors') {
        return Promise.resolve({
          factors: [
            {
              id: 'factor-1',
              kind: 'totp',
              label: 'Authenticator app',
              status: 'active',
              created_at: '2026-01-01T00:00:00Z',
            },
          ],
        } as T)
      }
      if (path === '/api/v1/auth/impersonation' && method === 'GET') {
        return Promise.reject({ status: 404, code: 'client.http.404' })
      }
      if (path === '/api/v1/auth/impersonation' && method === 'POST') {
        return Promise.reject({
          status: 403,
          code: 'authn.factor_not_seasoned',
          message: 'unseasoned',
          auth: true,
        })
      }
      return Promise.resolve(undefined as T)
    }
    bindRequestFn(fn)
    const user = userEvent.setup()
    renderWithProviders(<ImpersonationsPage />, {
      operations: withPage(),
      session: createStepUpSession(),
    })

    await screen.findByText(zhCN.impersonations.current.none)
    await pickTenant(user)
    await pickStartAccount(user, 'acc-9')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

    expect(
      await screen.findByText(zhCN.errors.authn.factor_not_seasoned),
    ).toBeInTheDocument()
    const section = screen
      .getByRole('heading', { name: zhCN.impersonations.current.title })
      .closest('section')
    expect(section).not.toBeNull()
    expect(
      within(section as HTMLElement).getByText(
        zhCN.errors.authn.factor_not_seasoned,
      ),
    ).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.queryByText(zhCN.stepUp.title)).not.toBeInTheDocument()
  })
})
