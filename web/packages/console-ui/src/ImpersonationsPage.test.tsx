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
import { act, fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { ImpersonationsPage } from './ImpersonationsPage.js'
import { ConsoleImpersonationChangeProvider } from './internal/impersonation-change.js'
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
import { createStepUpSession } from '../test-utils/session.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const ZERO = '0001-01-01T00:00:00Z'
const TENANT = { id: 'acme', alias: 'Acme', status: 'active' as const }

/** The line that replaces the record half while an impersonation is in force. */
const RECORDS_UNAVAILABLE =
  '冒充期间不能读取或终止冒充记录；结束冒充后可在此查看。'

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
    // The terminate control exists only on the record half, which renders
    // while no impersonation is in force; the end control exists only while
    // one is.
    const user = userEvent.setup()
    const idle = renderWithProviders(
      <ImpersonationsPage />,
      {
        operations: withPage({
          listImpersonations: () => Promise.resolve([record()]),
        }),
      },
    )
    const refresh = screen.getByRole('button', { name: '刷新' })
    await pickTenant(user)
    const terminate = await screen.findByRole('button', { name: '终止' })
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
    const end = await screen.findByRole('button', { name: '结束冒充' })
    expect(hasCoarsePointerTouchTarget(end)).toBe(true)
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
    // Every account identifier the page shows keeps the recorded id,
    // labelled unresolved rather than passed off as a resolved identity.
    // The two surfaces do not render together -- the record half is closed
    // while an impersonation is in force -- so each is checked on its own.
    const user = userEvent.setup()
    const recordHalf = renderWithProviders(<ImpersonationsPage />, {
      operations: withPage({
        listImpersonations: () => Promise.resolve([record()]),
        getAccount: () => Promise.reject(new Error('forbidden')),
      }),
    })
    await pickTenant(user)
    const recordIds = await screen.findAllByLabelText(/身份未解析/)
    expect(recordIds.map((cell) => cell.textContent)).toEqual(
      expect.arrayContaining(['staff-1', 'acc-1']),
    )
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
    const grantIds = await screen.findAllByLabelText(/身份未解析/)
    expect(grantIds.map((cell) => cell.textContent)).toEqual(['acc-7'])
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

  it('terminates an in-force impersonation and re-reads the session status', async () => {
    const user = userEvent.setup()
    const ended: string[] = []
    let statusReads = 0
    const countingIdle: RequestFn = <T,>(
      path: string,
      options?: RequestOptions,
    ) => {
      if (path === '/auth/impersonation' && (options?.method ?? 'GET') === 'GET') {
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
    await user.click(screen.getByRole('button', { name: '终止' }))
    await vi.waitFor(() => {
      expect(ended).toEqual(['imp-1'])
    })
    // A termination can change what the operator's own status read names,
    // so it is re-read alongside the record list the bundle invalidates.
    await vi.waitFor(() => expect(statusReads).toBeGreaterThan(1))
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
    await screen.findByText('被冒充账户：Target Seven')
    await expectNoAxeViolations()
  })

  // --- The caller's own impersonation session (/auth/impersonation) -------

  it('shows the not-impersonating state and the start form when no impersonation is in force', async () => {
    renderWithProviders(<ImpersonationsPage />, { operations: withPage() })
    expect(
      await screen.findByText('当前未冒充任何账户。'),
    ).toBeInTheDocument()
    expect(screen.getByLabelText('目标账户')).toBeInTheDocument()
    // The start form states its precondition beside the controls: the
    // The server's refusal cannot tell an unmet factor-age requirement from a
    // weak session, so the page states the requirement instead of naming a
    // cause; only this text warns that signing in again will not help.
    expect(
      screen.getByText(
        '发起冒充需要你本人的账户有已启用满 authn.mfa.sensitive-min-age（缺省 24 小时，以本部署设置为准）的非短信因素；刚登记的因素不算，重新登录也不能缩短这段等待。',
      ),
    ).toBeInTheDocument()
    // The start form takes its target tenant from the page's one tenant
    // selector; it carries no second, free-text tenant input of its own.
    expect(screen.getByLabelText('租户')).toBeInTheDocument()
    expect(screen.queryByLabelText('租户标识')).toBeNull()
    expect(
      screen.getByText('请先选择一个租户；被冒充账户必须属于该租户。'),
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

  it('reports a settled start to the host without waiting for it', async () => {
    const backend = makeCallerSessionBackend()
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    // The host's action never settles: the page must not wait for it.
    const onChange = vi.fn(() => new Promise<void>(() => undefined))
    renderWithProviders(
      <ConsoleImpersonationChangeProvider onImpersonationChange={onChange}>
        <ImpersonationsPage />
      </ConsoleImpersonationChangeProvider>,
      { operations: withPage() },
    )

    await screen.findByText('当前未冒充任何账户。')
    await pickTenant(user)
    await user.type(screen.getByLabelText('目标账户'), 'acc-1')
    await user.click(screen.getByRole('button', { name: '发起冒充' }))

    await vi.waitFor(() => expect(onChange).toHaveBeenCalledWith('started'))
    expect(onChange).toHaveBeenCalledTimes(1)
    // The success copy rendered while the host action is still pending.
    expect(
      await screen.findByText('已开始冒充账户 acc-1。'),
    ).toBeInTheDocument()
  })

  it("wraps the host's rejection, leaving the settled start a success", async () => {
    const backend = makeCallerSessionBackend()
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    const onChange = vi.fn(() => Promise.reject(new Error('host action failed')))
    renderWithProviders(
      <ConsoleImpersonationChangeProvider onImpersonationChange={onChange}>
        <ImpersonationsPage />
      </ConsoleImpersonationChangeProvider>,
      { operations: withPage() },
    )

    await screen.findByText('当前未冒充任何账户。')
    await pickTenant(user)
    await user.type(screen.getByLabelText('目标账户'), 'acc-1')
    await user.click(screen.getByRole('button', { name: '发起冒充' }))

    await vi.waitFor(() => expect(onChange).toHaveBeenCalledWith('started'))
    // No failure rendering: the start landed, and the host's own failure is
    // not the page's to report.
    expect(
      await screen.findByText('已开始冒充账户 acc-1。'),
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
      <ConsoleImpersonationChangeProvider onImpersonationChange={onChange}>
        <ImpersonationsPage />
      </ConsoleImpersonationChangeProvider>,
      { operations: withPage() },
    )

    expect(
      await screen.findByText('被冒充账户：Target Seven'),
    ).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '结束冒充' }))

    await vi.waitFor(() => expect(onChange).toHaveBeenCalledWith('ended'))
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(
      await screen.findByText('已结束冒充账户 acc-7。'),
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
      if (path !== '/auth/impersonation') {
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
        // Already ended elsewhere: the end answers the bare 404, and the
        // status read that follows sees nothing in force.
        inForce = false
        return Promise.reject({ status: 404, code: 'client.http.404' })
      }
      return Promise.resolve(undefined as T)
    }
    bindRequestFn(fn)
    const user = userEvent.setup()
    const onChange = vi.fn()
    renderWithProviders(
      <ConsoleImpersonationChangeProvider onImpersonationChange={onChange}>
        <ImpersonationsPage />
      </ConsoleImpersonationChangeProvider>,
      { operations: withPage() },
    )

    expect(
      await screen.findByText('被冒充账户：Target Seven'),
    ).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '结束冒充' }))

    await vi.waitFor(() => expect(onChange).toHaveBeenCalledWith('ended'))
    expect(deletes).toBe(1)
    // The same closing path a successful end takes: the success copy, no
    // failure banner, and the section lands on "not impersonating".
    expect(
      await screen.findByText('已结束冒充账户 acc-7。'),
    ).toBeInTheDocument()
    expect(
      await screen.findByText('当前未冒充任何账户。'),
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

    expect(await screen.findByText('被冒充账户：Target Seven')).toBeInTheDocument()
    expect(screen.getByText('租户：acme')).toBeInTheDocument()
    // The start form is offered only while none is in force.
    expect(screen.queryByLabelText('目标账户')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '结束冒充' }))
    await vi.waitFor(() => {
      expect(backend.ended).toHaveLength(1)
    })
  })

  it('treats the status read\'s bare 404 as a settled "not impersonating" answer', async () => {
    let reads = 0
    const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      if (path !== '/auth/impersonation') {
        return Promise.resolve(undefined as T)
      }
      if ((options?.method ?? 'GET') !== 'GET') {
        return Promise.resolve(undefined as T)
      }
      reads += 1
      // The first read names a grant; a later read answers the bare 404
      // the endpoint writes when nothing is in force.
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

    expect(await screen.findByText('被冒充账户：Target One')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '刷新' }))

    // The 404 is the settled answer "not impersonating": the cached grant
    // is discarded, and nothing renders as a failure or a stale marker.
    expect(await screen.findByText('当前未冒充任何账户。')).toBeInTheDocument()
    expect(screen.queryByText('被冒充账户：Target One')).toBeNull()
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
    // The first status read names the grant; after the end, the re-read is
    // held open so the test can see whether the cached grant was dropped
    // before the next answer arrived.
    let phase: 'in-force' | 'ended' = 'in-force'
    let releaseRead: () => void = () => undefined
    const endRead = new Promise<void>((resolve) => {
      releaseRead = resolve
    })
    let reads = 0
    let ended = 0
    const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      if (path !== '/auth/impersonation') {
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

    expect(await screen.findByText('被冒充账户：Target Seven')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '结束冒充' }))
    await vi.waitFor(() => expect(ended).toBe(1))

    // The end resets the status cache and re-reads it: the retired grant is
    // gone from the screen while the new read is still in flight (an
    // invalidate would keep it there until the refetch settled).
    await vi.waitFor(() => {
      expect(screen.queryByText('被冒充账户：Target Seven')).toBeNull()
    })
    expect(reads).toBeGreaterThan(1)

    releaseRead()
    // The re-read answers the bare 404: the settled "not impersonating"
    // state, not an error.
    expect(await screen.findByText('当前未冒充任何账户。')).toBeInTheDocument()

    // The confirmation names the account the end changed, and it lives in
    // the current-impersonation section.
    const section = screen
      .getByRole('heading', { name: '当前冒充' })
      .closest('section')
    expect(section).not.toBeNull()
    expect(
      within(section as HTMLElement).getByText('已结束冒充账户 acc-7。'),
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

    await screen.findByText('当前未冒充任何账户。')
    await pickTenant(user)
    await user.type(screen.getByLabelText('目标账户'), 'acc-9')
    await user.click(screen.getByRole('button', { name: '发起冒充' }))
    // A start writes a record into the tenant's list as well, so the list
    // is re-read (the read ran once when the tenant was selected).
    await vi.waitFor(() =>
      expect(listImpersonations.mock.calls.length).toBeGreaterThan(1),
    )

    // The landed start replaces the form with the in-force panel; the
    // confirmation is not unmounted with the form that produced it.
    expect(
      await screen.findByText('已开始冒充账户 acc-1。'),
    ).toBeInTheDocument()
    expect(screen.queryByLabelText('目标账户')).not.toBeInTheDocument()
    const section = screen
      .getByRole('heading', { name: '当前冒充' })
      .closest('section')
    expect(section).not.toBeNull()
    expect(
      within(section as HTMLElement).getByText('已开始冒充账户 acc-1。'),
    ).toBeInTheDocument()
  })

  it('hands a step-up-refused start to the elevation gate and reports the replay', async () => {
    const posts: unknown[] = []
    const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      const method = options?.method ?? 'GET'
      if (path === '/auth/factors') {
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
      if (path === '/auth/impersonation' && method === 'GET') {
        return Promise.reject({ status: 404, code: 'client.http.404' })
      }
      if (path === '/auth/impersonation' && method === 'POST') {
        posts.push(options?.body)
        // The first refusal is the strength shortfall the gate takes over;
        // the replayed write is answered with an ordinary refusal, which is
        // the only one the section reports.
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

    await screen.findByText('当前未冒充任何账户。')
    await pickTenant(user)
    await user.type(screen.getByLabelText('目标账户'), 'acc-9')
    await user.click(screen.getByRole('button', { name: '发起冒充' }))

    // The first refusal settled without touching the slot (no banner for
    // it), the dialog elevated, and the replay re-fired the same body as a
    // new run whose refusal is what the section shows.
    expect(
      await screen.findByText(zhCN.errors.authn.forbidden),
    ).toBeInTheDocument()
    await vi.waitFor(() => expect(posts).toHaveLength(2))
    expect(posts[1]).toEqual(posts[0])
    expect(posts[0]).toEqual({ account: 'acc-9', tenant: 'acme' })
  })

  // --- The refusal of an impersonation-restricted write ------------------

  it('renders the impersonation-restricted refusal without naming one cause', async () => {
    // The code answers several refusals -- the common one is a target that
    // is not a member of the tenant -- so the text states the requirements
    // side by side instead of naming the rarest cause.
    const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      const method = options?.method ?? 'GET'
      if (path === '/auth/impersonation' && method === 'GET') {
        return Promise.reject({ status: 404, code: 'client.http.404' })
      }
      if (path === '/auth/impersonation' && method === 'POST') {
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

    await screen.findByText('当前未冒充任何账户。')
    await pickTenant(user)
    await user.type(screen.getByLabelText('目标账户'), 'acc-9')
    await user.click(screen.getByRole('button', { name: '发起冒充' }))

    const alert = await screen.findByRole('alert')
    const rendered = alert.textContent ?? ''
    // Every requirement is named as a peer; none is picked out as the
    // cause, and the "during an impersonation" reading of the old text is
    // gone in both languages.
    expect(rendered).toContain('成员')
    expect(rendered).toContain('已启用')
    expect(rendered).toContain('未受保护')
    expect(rendered).toContain('没有进行中的冒充')
    expect(rendered).not.toContain('模拟登录期间')
    expect(rendered).not.toContain('during an impersonation')
    expect(rendered).not.toContain('while impersonating')
  })

  it('states the same requirements in the English refusal', async () => {
    const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      const method = options?.method ?? 'GET'
      if (path === '/auth/impersonation' && method === 'GET') {
        return Promise.reject({ status: 404, code: 'client.http.404' })
      }
      if (path === '/auth/impersonation' && method === 'POST') {
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
    await user.click(screen.getByLabelText('Tenant'))
    await user.click(await screen.findByRole('option', { name: 'Acme' }))
    await user.type(screen.getByLabelText('Target account'), 'acc-9')
    await user.click(screen.getByRole('button', { name: 'Start impersonation' }))

    const alert = await screen.findByRole('alert')
    const rendered = alert.textContent ?? ''
    expect(rendered).toContain('member account')
    expect(rendered).toContain('enabled')
    expect(rendered).toContain('unprotected')
    expect(rendered).toContain('no impersonation in progress')
    expect(rendered).not.toContain('during an impersonation')
    expect(rendered).not.toContain('while impersonating')
  })

  // --- The record half while an impersonation is in force ----------------

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
    // The tenant is already chosen, so only the status read stands between
    // the page and a record read.
    window.history.replaceState({}, '', '/console/impersonations?tenant=acme')
    try {
      renderWithProviders(<ImpersonationsPage />, {
        operations: withPage({ listImpersonations }),
      })

      expect(
        await screen.findByText('被冒充账户：Target One'),
      ).toBeInTheDocument()
      // The one line replaces the selector, the list and its controls.
      expect(screen.getByText(RECORDS_UNAVAILABLE)).toBeInTheDocument()
      expect(screen.queryByLabelText('租户')).toBeNull()
      expect(screen.queryByText('暂无冒充记录')).toBeNull()
      expect(screen.queryByRole('button', { name: '终止' })).toBeNull()
      // The reads would be refused through the impersonated identity, so
      // they are not issued at all.
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
      if (path === '/auth/impersonation' && (options?.method ?? 'GET') === 'GET') {
        // Never settles: the status stays unknown for the whole test.
        return new Promise<T>(() => undefined)
      }
      return Promise.resolve(undefined as T)
    }
    bindRequestFn(fn)
    window.history.replaceState({}, '', '/console/impersonations?tenant=acme')
    try {
      renderWithProviders(<ImpersonationsPage />, { operations: withPage() })
      // "Status unknown" is not "impersonating": the record surface renders
      // as it always has.
      expect(screen.getByLabelText('租户')).toBeInTheDocument()
      expect(
        screen.getByRole('status', { name: '正在加载冒充记录' }),
      ).toBeInTheDocument()
      expect(screen.queryByText(RECORDS_UNAVAILABLE)).toBeNull()
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('renders the record half when the status read fails with a non-404 answer', async () => {
    const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      if (path === '/auth/impersonation' && (options?.method ?? 'GET') === 'GET') {
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

    expect(await screen.findByText('冒充状态读取失败')).toBeInTheDocument()
    expect(screen.getByLabelText('租户')).toBeInTheDocument()
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
      // The read the returning surface issues is held open, so the test can
      // see whether the pre-impersonation list leaks back as current.
      return held.then(() => [record({ target: 'acc-7' })])
    })
    window.history.replaceState({}, '', '/console/impersonations?tenant=acme')
    try {
      const user = userEvent.setup()
      renderWithProviders(<ImpersonationsPage />, {
        operations: withPage({ listImpersonations }),
      })

      // The list is read before any impersonation starts.
      expect(await screen.findByText('Staff One')).toBeInTheDocument()

      await user.type(screen.getByLabelText('目标账户'), 'acc-9')
      await user.click(screen.getByRole('button', { name: '发起冒充' }))
      // Once the status read lands on the grant, the record half is closed.
      expect(await screen.findByText(RECORDS_UNAVAILABLE)).toBeInTheDocument()

      await user.click(screen.getByRole('button', { name: '结束冒充' }))
      await vi.waitFor(() => expect(backend.ended).toHaveLength(1))

      // The surface returns without a refresh and issues a new read. The
      // cached pre-impersonation list is gone while that read is in flight:
      // it describes a state the end just changed.
      await vi.waitFor(() => expect(reads).toBe(3))
      expect(screen.queryByText(RECORDS_UNAVAILABLE)).toBeNull()
      expect(screen.getByLabelText('租户')).toBeInTheDocument()
      expect(screen.queryByText('Staff One')).toBeNull()
      expect(
        screen.getByRole('status', { name: '正在加载冒充记录' }),
      ).toBeInTheDocument()

      release()
      expect(await screen.findByText('Target Seven')).toBeInTheDocument()
      // The old record's target is gone: the list is the one the returning
      // surface read, not the pre-impersonation page it replaced.
      expect(screen.queryByText('Target One')).toBeNull()
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('renders an unseasoned-factor refusal in the current scope without a step-up dialog', async () => {
    // The 403 is not the step-up's 401: no re-authentication clears it, so the
    // attempt settles as an ordinary write failure in the section's own
    // region and the dialog must not open.
    const fn: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      const method = options?.method ?? 'GET'
      if (path === '/auth/factors') {
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
      if (path === '/auth/impersonation' && method === 'GET') {
        return Promise.reject({ status: 404, code: 'client.http.404' })
      }
      if (path === '/auth/impersonation' && method === 'POST') {
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

    await screen.findByText('当前未冒充任何账户。')
    await pickTenant(user)
    await user.type(screen.getByLabelText('目标账户'), 'acc-9')
    await user.click(screen.getByRole('button', { name: '发起冒充' }))

    expect(
      await screen.findByText(zhCN.errors.authn.factor_not_seasoned),
    ).toBeInTheDocument()
    // The refusal stays in the current-impersonation section's region: the
    // section that owns the start form, not the record table's.
    const section = screen
      .getByRole('heading', { name: '当前冒充' })
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
