/**
 * ImpersonationsPage: the impersonation administration page.
 *
 * The page carries two surfaces over one page: the operator's own
 * impersonation session on /auth/impersonation (the status read, the start
 * form and the end action -- the caller-session operations the page calls
 * directly), and the tenant's record list on /auth/admin/impersonations
 * (through the scripted operations bundle).
 *
 * The caller-session half is exercised over the api-sdk runtime seam: the
 * suite binds a scripted request function (bindRequestFn) that answers the
 * three /auth/impersonation calls, so the real generated hooks run against
 * responses the test controls -- the "no impersonation" 404, a grant in
 * force, a started record. The administration half runs on the scripted
 * operations bundle, as the other console suites do.
 */

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { ImpersonationsPage } from './ImpersonationsPage.js'
import type { ConsoleImpersonation } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  accountFixture,
  createConsoleOperations,
  makeConsoleBackend,
  type ConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'

const ZERO = '0001-01-01T00:00:00Z'
const TENANT = { id: 'acme', alias: 'Acme', status: 'active' as const }

/** The caller-session grant the status read answers with when one is in force. */
interface GrantFixture {
  readonly id: string
  readonly target: string
  readonly tenant: string
  readonly starts_at: string
  readonly ends_at: string
}

/**
 * A scripted /auth/impersonation endpoint over the api-sdk runtime seam.
 * The status read answers a bare 404 ("no impersonation in force") until the
 * backend holds a grant; a start records its body and puts a grant in force;
 * an end clears it. The test drives the page and asserts the requests it made
 * by reading `started`, `ended` and `statusReads`.
 */
function makeCallerSessionBackend() {
  let grant: GrantFixture | undefined
  const started: unknown[] = []
  const ended: unknown[] = []
  let statusReads = 0
  const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
    const method = options?.method ?? 'GET'
    if (path !== '/auth/impersonation') {
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

/** The idle caller-session endpoint: no impersonation in force. */
const idleCallerSession: RequestFn = <T,>(
  _path: string,
  options?: RequestOptions,
) => {
  const method = options?.method ?? 'GET'
  if (_path === '/auth/impersonation' && method === 'GET') {
    return Promise.reject({ status: 404, code: 'client.http.404' })
  }
  return Promise.resolve(undefined as T)
}

function record(overrides: Partial<ConsoleImpersonation> = {}): ConsoleImpersonation {
  return {
    id: 'imp-1',
    actor: 'staff-1',
    target: 'acc-1',
    tenant: 'acme',
    session_id: 'sess-1',
    started_at: '2026-10-15T12:00:00Z',
    ends_at: '2026-10-15T13:00:00Z',
    ended_at: ZERO,
    ...overrides,
  }
}

async function pickTenant(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(screen.getByLabelText('租户'))
  await user.click(await screen.findByRole('option', { name: 'Acme' }))
}

/**
 * The display name the account read answers for the identifiers the page
 * records, so a test can tell a resolved identity from the raw id.
 */
const ACCOUNT_NAMES: Record<string, string> = {
  'staff-1': 'Staff One',
  'acc-1': 'Target One',
  'acc-7': 'Target Seven',
}

function withPage(
  overrides: Partial<ConsoleBackend> = {},
): ReturnType<typeof createConsoleOperations> {
  return createConsoleOperations(
    makeConsoleBackend({
      listTenants: () => Promise.resolve([TENANT]),
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
  // Every test starts with the caller-session endpoint answering "no
  // impersonation"; a test that scripts a grant rebinds below.
  bindRequestFn(idleCallerSession)
})

describe('ImpersonationsPage', () => {
  it('raises its compact actions to the 44px floor under a coarse pointer', async () => {
    const backend = makeCallerSessionBackend()
    backend.setGrant({
      id: 'imp-1',
      target: 'acc-1',
      tenant: 'acme',
      starts_at: '2026-10-15T12:00:00Z',
      ends_at: '2026-10-15T13:00:00Z',
    })
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    const operations = withPage({
      listImpersonations: () => Promise.resolve([record()]),
    })
    renderWithProviders(<ImpersonationsPage />, { operations })
    const refresh = screen.getByRole('button', { name: '刷新' })
    const end = await screen.findByRole('button', { name: '结束冒充' })
    await pickTenant(user)
    const terminate = await screen.findByRole('button', { name: '终止' })
    for (const control of [refresh, end, terminate]) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
  })

  it('prompts for a tenant before listing', () => {
    const operations = withPage()
    renderWithProviders(<ImpersonationsPage />, { operations })
    expect(screen.getByText('请选择租户')).toBeInTheDocument()
  })

  it('opens on the tenant named by the query string', async () => {
    window.history.replaceState({}, '', '/console/impersonations?tenant=acme')
    try {
      const operations = withPage()
      renderWithProviders(<ImpersonationsPage />, { operations })
      // The link that carried the tenant arrives with a selection already
      // made: no "choose a tenant" prompt, and the list read runs for it.
      expect(await screen.findByText('暂无冒充记录')).toBeInTheDocument()
      expect(screen.queryByText('请选择租户')).toBeNull()
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('renders the empty state when there is no record', async () => {
    const user = userEvent.setup()
    const operations = withPage()
    renderWithProviders(<ImpersonationsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText('暂无冒充记录')).toBeInTheDocument()
  })

  it('lists records and marks an in-force one', async () => {
    const user = userEvent.setup()
    const operations = withPage({
      listImpersonations: () => Promise.resolve([record()]),
    })
    renderWithProviders(<ImpersonationsPage />, { operations })
    await pickTenant(user)
    // The actor and target columns show the readable identities, not the
    // recorded ids.
    expect(await screen.findByText('Staff One')).toBeInTheDocument()
    expect(screen.getByText('Target One')).toBeInTheDocument()
    expect(screen.getByText('生效中')).toBeInTheDocument()
  })

  it('keeps the recorded ids marked unresolved when the account read is refused', async () => {
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
    const operations = withPage({
      listImpersonations: () => Promise.resolve([record()]),
      getAccount: () => Promise.reject(new Error('forbidden')),
    })
    renderWithProviders(<ImpersonationsPage />, { operations })
    await pickTenant(user)
    // Every account identifier the page shows -- the in-force panel's
    // target and the record's actor and target -- keeps the recorded id,
    // labelled unresolved rather than passed off as a resolved identity.
    const unresolved = await screen.findAllByLabelText(/身份未解析/)
    expect(unresolved.map((cell) => cell.textContent)).toEqual(
      expect.arrayContaining(['acc-7', 'staff-1', 'acc-1']),
    )
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
    expect(await screen.findByText('已结束（revoked）')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '终止' })).toBeDisabled()
  })

  it('terminates an in-force impersonation', async () => {
    const user = userEvent.setup()
    const ended: string[] = []
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
    await user.click(screen.getByRole('button', { name: '终止' }))
    await vi.waitFor(() => {
      expect(ended).toEqual(['imp-1'])
    })
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const user = userEvent.setup()
    const operations = withPage({
      listImpersonations: () => Promise.reject(new Error('boom')),
    })
    renderWithProviders(<ImpersonationsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText('冒充记录加载失败')).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the read is refused with 403', async () => {
    const user = userEvent.setup()
    const operations = withPage({
      listImpersonations: () =>
        Promise.reject({ status: 403, code: 'client.http.403' }),
    })
    renderWithProviders(<ImpersonationsPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText('权限不足')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
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

  // --- The caller's own impersonation session (/auth/impersonation) -------

  it('shows the not-impersonating state and the start form when no impersonation is in force', async () => {
    renderWithProviders(<ImpersonationsPage />, { operations: withPage() })
    expect(
      await screen.findByText('当前未冒充任何账户。'),
    ).toBeInTheDocument()
    expect(screen.getByLabelText('目标账户')).toBeInTheDocument()
    // The start form takes its target tenant from the page's one tenant
    // selector; it carries no second, free-text tenant input of its own.
    expect(screen.getByLabelText('租户')).toBeInTheDocument()
    expect(screen.queryByLabelText('租户标识')).toBeNull()
    expect(
      screen.getByText('请先在上方选择一个租户；被冒充账户必须属于该租户。'),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: '发起冒充' }),
    ).toBeDisabled()
  })

  it('withholds the start until a tenant is selected', async () => {
    const backend = makeCallerSessionBackend()
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    renderWithProviders(<ImpersonationsPage />, { operations: withPage() })

    await screen.findByText('当前未冒充任何账户。')
    await user.type(screen.getByLabelText('目标账户'), 'acc-9')
    const submit = screen.getByRole('button', { name: '发起冒充' })
    expect(submit).toBeDisabled()
    fireEvent.click(submit)
    // A disabled control dispatches nothing; flush the queue so an
    // accidental request would have landed before the assertion.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(backend.started).toEqual([])

    await pickTenant(user)
    expect(submit).toBeEnabled()
  })

  it("starts an impersonation, sending the endpoint's request body", async () => {
    const backend = makeCallerSessionBackend()
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    renderWithProviders(<ImpersonationsPage />, { operations: withPage() })

    await screen.findByText('当前未冒充任何账户。')
    await pickTenant(user)
    await user.type(screen.getByLabelText('目标账户'), 'acc-9')
    await user.type(screen.getByLabelText('理由'), 'investigate ticket')
    await user.type(screen.getByLabelText('有效期（秒）'), '3600')
    await user.click(screen.getByRole('button', { name: '发起冒充' }))

    await vi.waitFor(() => {
      expect(backend.started).toEqual([
        {
          account: 'acc-9',
          tenant: 'acme',
          reason: 'investigate ticket',
          ttl_seconds: 3600,
        },
      ])
    })
    // The landed start invalidates the status read, so the page converges on
    // the record the server just wrote: the in-force panel replaces the form.
    expect(await screen.findByText('被冒充账户：Target One')).toBeInTheDocument()
    expect(
      screen.queryByLabelText('目标账户'),
    ).not.toBeInTheDocument()
  })

  it('sends the selected tenant and omits the other optional fields when they are blank', async () => {
    const backend = makeCallerSessionBackend()
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    renderWithProviders(<ImpersonationsPage />, { operations: withPage() })

    await screen.findByText('当前未冒充任何账户。')
    await pickTenant(user)
    await user.type(screen.getByLabelText('目标账户'), 'acc-9')
    await user.click(screen.getByRole('button', { name: '发起冒充' }))

    await vi.waitFor(() => {
      expect(backend.started).toEqual([{ account: 'acc-9', tenant: 'acme' }])
    })
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

    expect(await screen.findByText('被冒充账户：Target Seven')).toBeInTheDocument()
    expect(screen.getByText('租户：acme')).toBeInTheDocument()
    // The start form is offered only while none is in force.
    expect(screen.queryByLabelText('目标账户')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '结束冒充' }))
    await vi.waitFor(() => {
      expect(backend.ended).toHaveLength(1)
    })
  })
})
