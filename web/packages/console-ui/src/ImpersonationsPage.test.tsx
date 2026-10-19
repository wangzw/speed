/**
 * ImpersonationsPage: the impersonation administration page.
 *
 * The page carries two surfaces over one page: the operator's own
 * impersonation session on /api/v1/auth/impersonation (the status read, the start
 * form and the end action -- the caller-session operations the page calls
 * directly), and the tenant's record list on /api/v1/tenants/{tenant}/impersonations
 * (through the scripted operations bundle).
 *
 * The caller-session half is exercised over the api-sdk runtime seam: the
 * suite binds a scripted request function (bindRequestFn) that answers the
 * three /api/v1/auth/impersonation calls, so the real generated hooks run against
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
import { ConsoleImpersonationStartingProvider } from './internal/impersonation-starting.js'
import type { ConsoleImpersonation } from './operations.js'
import { renderWithProviders, createConsoleUiI18n } from '../test-utils/render.js'
import { CONSOLE_UI_NAMESPACE } from './resources.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  columnVisibility,
  containerMaxWidthOf,
} from '../test-utils/container-visibility.js'
import {
  accountFixture,
  createConsoleOperations,
  makeConsoleBackend,
  type ConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { createStepUpSession } from '../test-utils/session.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import { uiKitResources } from '@speed/ui-kit'

const ZERO = '0001-01-01T00:00:00Z'
const TENANT = { id: 'acme', alias: 'Acme', status: 'active' as const }

/** The end-reason labels a test injects into the namespace. */
const END_REASON_LABELS = { revoked: 'end-reason-revoked' } as const

/** Fills a locale template's placeholders with the values under test. */
function fill(
  template: string,
  values: Readonly<Record<string, string>>,
): string {
  return Object.entries(values).reduce(
    (text, [name, value]) => text.replace(`{{${name}}}`, value),
    template,
  )
}

/** The ui-kit copy control's accessible name, from its zh-CN template. */
function copyLabel(value: string): string {
  const template = (
    uiKitResources['zh-CN'] as unknown as {
      readonly dataTable: { readonly copyValue: string }
    }
  ).dataTable.copyValue
  return template.replace('{{value}}', value)
}

/** Matches the account cell labelled with the unresolved-identity note. */
function unresolvedAccountLabel(content: string): boolean {
  return content.includes(zhCN.common.accountUnresolved)
}

/** The in-force panel's target line: its prefixed label beside the name. */
function targetLine(name: string): string {
  return `${zhCN.impersonations.current.target}${name}`
}

/** The line that replaces the record half while an impersonation is in force. */
const RECORDS_UNAVAILABLE = zhCN.impersonations.recordsUnavailable

/** The caller-session grant the status read answers with when one is in force. */
interface GrantFixture {
  readonly id: string
  readonly target: string
  readonly tenant: string
  readonly starts_at: string
  readonly ends_at: string
}

/**
 * A scripted /api/v1/auth/impersonation endpoint over the api-sdk runtime seam.
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

/** The idle caller-session endpoint: no impersonation in force. */
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
  await user.click(screen.getByLabelText(zhCN.common.tenant))
  await user.click(await screen.findByRole('option', { name: 'Acme' }))
}

/** A promise the test settles by hand, for observing an in-flight write. */
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

/**
 * Asserts the header refresh is inert while a write is in flight: disabled
 * without the empty-scope explanation, and a forced activation issues no
 * read (the status read and the record read are counted together).
 */
async function expectRefreshInert(
  countReads: () => readonly [number, number],
  readsBefore: readonly [number, number],
): Promise<void> {
  const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
  await vi.waitFor(() => {
    expect(refresh).toBeDisabled()
  })
  expect(refresh).not.toHaveAttribute('aria-describedby')
  fireEvent.click(refresh)
  await act(async () => {
    await Promise.resolve()
  })
  expect(countReads()).toEqual(readsBefore)
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
    window.history.replaceState({}, '', '/console/impersonations?tenant=acme')
    try {
      const operations = withPage()
      renderWithProviders(<ImpersonationsPage />, { operations })
      // The link that carried the tenant arrives with a selection already
      // made: no "choose a tenant" prompt, and the list read runs for it.
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
    // The actor and target columns show the readable identities, not the
    // recorded ids.
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
    // The target is the record's primary identity: no priority, shared cap.
    // The page is tenant-scoped, so the tenant is the first column shed;
    // the actor joins at md; the ends column and the tenant at lg; the
    // row actions never hide.
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

    // The primary identity carries the shared whole-cell cap (160px below
    // `md`, 200px from `md` up); the long-text actor carries the flat
    // 160px one. Both count the copy control, so the complete recorded id
    // stays reachable while the cell stays inside the tier budget.
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
      within(actorCell as HTMLElement).getByRole('button', { name: copyLabel('staff-1') }),
    ).toBeInTheDocument()
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
    const recordIds = await screen.findAllByLabelText(unresolvedAccountLabel)
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
    const grantIds = await screen.findAllByLabelText(unresolvedAccountLabel)
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
    expect(
      await screen.findByText(
        fill(zhCN.impersonations.ended, {
          reason: zhCN.impersonations.endReason.revoked,
        }),
      ),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: zhCN.impersonations.terminate })).toBeDisabled()
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
    await vi.waitFor(() => {
      expect(ended).toEqual(['imp-1'])
    })
    // A termination can change what the operator's own status read names,
    // so it is re-read alongside the record list the bundle invalidates.
    await vi.waitFor(() => expect(statusReads).toBeGreaterThan(1))
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
    await vi.waitFor(() =>
      expect(backend.statusReads()).toBe(statusBefore + 1),
    )
    await vi.waitFor(() =>
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
    window.history.replaceState({}, '', '/console/impersonations?tenant=globex')
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
                { id: 'globex', alias: 'Globex', status: 'active' as const },
              ]),
            listImpersonations,
          }),
        },
      )

      await vi.waitFor(() => {
        expect(listImpersonations).toHaveBeenCalledWith({ tenant: 'acme' })
      })
      await user.click(screen.getByLabelText(zhCN.common.tenant))
      await user.click(await screen.findByRole('option', { name: 'Globex' }))
      expect(onTenantChange).toHaveBeenCalledWith('globex')
      await vi.waitFor(() => {
        expect(listImpersonations).toHaveBeenCalledWith({ tenant: 'globex' })
      })
    } finally {
      window.history.replaceState({}, '', '/')
    }
  })

  it('leaves the caller-session surface ungated by the feature judgement', async () => {
    // The design's gating table is exhaustive, and the caller-session
    // surface is not on it. The impersonation declaration names
    // `authn.impersonation` in `requires`, so the host narrows the route
    // table before this page renders; a page-level gate would be a second,
    // half gate that leaves the status read and the end control running
    // beside a hidden start form.
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

    // The record half is a separate administration surface and still runs.
    await pickTenant(user)
    expect(await screen.findByText('Staff One')).toBeInTheDocument()
  })

  it('translates a reason it knows and leaves an unknown one as recorded', async () => {
    const user = userEvent.setup()
    const i18n = createConsoleUiI18n()
    i18n.addResourceBundle(
      'zh-CN',
      CONSOLE_UI_NAMESPACE,
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

  // --- The caller's own impersonation session (/api/v1/auth/impersonation) -------

  it('shows the not-impersonating state and the start form when no impersonation is in force', async () => {
    renderWithProviders(<ImpersonationsPage />, { operations: withPage() })
    expect(
      await screen.findByText(zhCN.impersonations.current.none),
    ).toBeInTheDocument()
    expect(screen.getByLabelText(zhCN.impersonations.start.accountLabel)).toBeInTheDocument()
    // The start form states its precondition beside the controls: the
    // The server's refusal cannot tell an unmet factor-age requirement from a
    // weak session, so the page states the requirement instead of naming a
    // cause; only this text warns that signing in again will not help.
    expect(
      screen.getByText(zhCN.impersonations.start.precondition),
    ).toBeInTheDocument()
    // The start form takes its target tenant from the page's one tenant
    // selector; it carries no second, free-text tenant input of its own.
    expect(screen.getByLabelText(zhCN.common.tenant)).toBeInTheDocument()
    expect(screen.queryByLabelText('Tenant identifier')).toBeNull()
    expect(
      screen.getByText(zhCN.impersonations.start.tenantHint),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.impersonations.start.submit }),
    ).toBeDisabled()
  })

  it('withholds the start until a tenant is selected', async () => {
    const backend = makeCallerSessionBackend()
    bindRequestFn(backend.fn)
    const user = userEvent.setup()
    renderWithProviders(<ImpersonationsPage />, { operations: withPage() })

    await screen.findByText(zhCN.impersonations.current.none)
    await user.type(screen.getByLabelText(zhCN.impersonations.start.accountLabel), 'acc-9')
    const submit = screen.getByRole('button', { name: zhCN.impersonations.start.submit })
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

    await screen.findByText(zhCN.impersonations.current.none)
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.impersonations.start.accountLabel), 'acc-9')
    await user.type(screen.getByLabelText(zhCN.impersonations.start.reasonLabel), 'investigate ticket')
    await user.type(
      screen.getByLabelText(zhCN.impersonations.start.ttlLabel),
      '3600',
    )
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

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
    await user.type(screen.getByLabelText(zhCN.impersonations.start.accountLabel), 'acc-9')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

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

    await screen.findByText(zhCN.impersonations.current.none)
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.impersonations.start.accountLabel), 'acc-1')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

    await vi.waitFor(() => expect(onChange).toHaveBeenCalledWith('started'))
    expect(onChange).toHaveBeenCalledTimes(1)
    // The success copy rendered while the host action is still pending.
    expect(
      await screen.findByText(fill(zhCN.impersonations.current.started, { account: 'acc-1' })),
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

    await screen.findByText(zhCN.impersonations.current.none)
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.impersonations.start.accountLabel), 'acc-1')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

    await vi.waitFor(() => expect(onChange).toHaveBeenCalledWith('started'))
    // No failure rendering: the start landed, and the host's own failure is
    // not the page's to report.
    expect(
      await screen.findByText(fill(zhCN.impersonations.current.started, { account: 'acc-1' })),
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
      await screen.findByText(targetLine('Target Seven')),
    ).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.current.end }))

    await vi.waitFor(() => expect(onChange).toHaveBeenCalledWith('ended'))
    expect(onChange).toHaveBeenCalledTimes(1)
    expect(
      await screen.findByText(fill(zhCN.impersonations.current.ended, { account: 'acc-7' })),
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
      await screen.findByText(targetLine('Target Seven')),
    ).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.current.end }))

    await vi.waitFor(() => expect(onChange).toHaveBeenCalledWith('ended'))
    expect(deletes).toBe(1)
    // The same closing path a successful end takes: the success copy, no
    // failure banner, and the section lands on "not impersonating".
    expect(
      await screen.findByText(fill(zhCN.impersonations.current.ended, { account: 'acc-7' })),
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
    // The start form is offered only while none is in force.
    expect(screen.queryByLabelText(zhCN.impersonations.start.accountLabel)).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: zhCN.impersonations.current.end }))
    await vi.waitFor(() => {
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

    expect(await screen.findByText(targetLine('Target One'))).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: zhCN.common.refresh }))

    // The 404 is the settled answer "not impersonating": the cached grant
    // is discarded, and nothing renders as a failure or a stale marker.
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
    await vi.waitFor(() => expect(ended).toBe(1))

    // The end resets the status cache and re-reads it: the retired grant is
    // gone from the screen while the new read is still in flight (an
    // invalidate would keep it there until the refetch settled).
    await vi.waitFor(() => {
      expect(screen.queryByText(targetLine('Target Seven'))).toBeNull()
    })
    expect(reads).toBeGreaterThan(1)

    releaseRead()
    // The re-read answers the bare 404: the settled "not impersonating"
    // state, not an error.
    expect(await screen.findByText(zhCN.impersonations.current.none)).toBeInTheDocument()

    // The confirmation names the account the end changed, and it lives in
    // the current-impersonation section.
    const section = screen
      .getByRole('heading', { name: zhCN.impersonations.current.title })
      .closest('section')
    expect(section).not.toBeNull()
    expect(
      within(section as HTMLElement).getByText(fill(zhCN.impersonations.current.ended, { account: 'acc-7' })),
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
    await user.type(screen.getByLabelText(zhCN.impersonations.start.accountLabel), 'acc-9')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))
    // A start writes a record into the tenant's list as well, so the list
    // is re-read (the read ran once when the tenant was selected).
    await vi.waitFor(() =>
      expect(listImpersonations.mock.calls.length).toBeGreaterThan(1),
    )

    // The landed start replaces the form with the in-force panel; the
    // confirmation is not unmounted with the form that produced it.
    expect(
      await screen.findByText(fill(zhCN.impersonations.current.started, { account: 'acc-1' })),
    ).toBeInTheDocument()
    expect(screen.queryByLabelText(zhCN.impersonations.start.accountLabel)).not.toBeInTheDocument()
    const section = screen
      .getByRole('heading', { name: zhCN.impersonations.current.title })
      .closest('section')
    expect(section).not.toBeNull()
    expect(
      within(section as HTMLElement).getByText(fill(zhCN.impersonations.current.started, { account: 'acc-1' })),
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

    await screen.findByText(zhCN.impersonations.current.none)
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.impersonations.start.accountLabel), 'acc-9')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

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

  // --- The impersonation-starting seam -----------------------------------

  it('fires the starting hook before the request and leaves a landed start to the host', async () => {
    // The POST stays in flight until the test settles it: the starting hook
    // must have been called by then, which pins "before mutateAsync"
    // (a hook fired on settlement would still read zero here).
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
      <ConsoleImpersonationChangeProvider onImpersonationChange={onChange}>
        <ConsoleImpersonationStartingProvider
          onImpersonationStarting={starting}
        >
          <ImpersonationsPage />
        </ConsoleImpersonationStartingProvider>
      </ConsoleImpersonationChangeProvider>,
      { operations: withPage() },
    )

    await screen.findByText(zhCN.impersonations.current.none)
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.impersonations.start.accountLabel), 'acc-1')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

    await vi.waitFor(() => expect(starting).toHaveBeenCalledTimes(1))
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

    await vi.waitFor(() => expect(onChange).toHaveBeenCalledWith('started'))
    // A landed write is the host's to release once its change action
    // settles; the page never releases it.
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
      <ConsoleImpersonationStartingProvider onImpersonationStarting={starting}>
        <ImpersonationsPage />
      </ConsoleImpersonationStartingProvider>,
      { operations: withPage() },
    )

    await screen.findByText(zhCN.impersonations.current.none)
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.impersonations.start.accountLabel), 'acc-9')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

    expect(await screen.findByText(zhCN.errors.authn.forbidden)).toBeInTheDocument()
    await vi.waitFor(() => expect(release).toHaveBeenCalledTimes(1))
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
      <ConsoleImpersonationStartingProvider onImpersonationStarting={starting}>
        <ImpersonationsPage />
      </ConsoleImpersonationStartingProvider>,
      { operations: withPage(), session: createStepUpSession() },
    )

    await screen.findByText(zhCN.impersonations.current.none)
    await pickTenant(user)
    await user.type(screen.getByLabelText(zhCN.impersonations.start.accountLabel), 'acc-9')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

    // The gate took the first refusal over and the replay is a new run, so
    // the hook fired again; the blocked attempt's release ran, the landed
    // replay's did not.
    await vi.waitFor(() => expect(starting).toHaveBeenCalledTimes(2))
    expect(releases[0]).toHaveBeenCalledTimes(1)
    expect(releases[1]).not.toHaveBeenCalled()
    expect(posts[1]).toEqual(posts[0])
  })

  // --- The refusal of an impersonation-restricted write ------------------

  it('renders the impersonation-restricted refusal without naming one cause', async () => {
    // The code answers several refusals -- the common one is a target that
    // is not a member of the tenant -- so the text states the requirements
    // side by side instead of naming the rarest cause.
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
    await user.type(screen.getByLabelText(zhCN.impersonations.start.accountLabel), 'acc-9')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

    const alert = await screen.findByRole('alert')
    const rendered = alert.textContent ?? ''
    // The refusal is the restricted-impersonation message itself: it names
    // every requirement as a peer instead of picking one out as the cause.
    // The "during an impersonation" reading of the old text is gone in both
    // languages.
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
        await screen.findByText(targetLine('Target One')),
      ).toBeInTheDocument()
      // The one line replaces the selector, the list and its controls.
      expect(screen.getByText(RECORDS_UNAVAILABLE)).toBeInTheDocument()
      expect(screen.queryByLabelText(zhCN.common.tenant)).toBeNull()
      expect(screen.queryByText(zhCN.impersonations.empty.title)).toBeNull()
      expect(screen.queryByRole('button', { name: zhCN.impersonations.terminate })).toBeNull()
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
      if (path === '/api/v1/auth/impersonation' && (options?.method ?? 'GET') === 'GET') {
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

      await user.type(screen.getByLabelText(zhCN.impersonations.start.accountLabel), 'acc-9')
      await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))
      // Once the status read lands on the grant, the record half is closed.
      expect(await screen.findByText(RECORDS_UNAVAILABLE)).toBeInTheDocument()

      await user.click(screen.getByRole('button', { name: zhCN.impersonations.current.end }))
      await vi.waitFor(() => expect(backend.ended).toHaveLength(1))

      // The surface returns without a refresh and issues a new read. The
      // cached pre-impersonation list is gone while that read is in flight:
      // it describes a state the end just changed.
      await vi.waitFor(() => expect(reads).toBe(3))
      expect(screen.queryByText(RECORDS_UNAVAILABLE)).toBeNull()
      expect(screen.getByLabelText(zhCN.common.tenant)).toBeInTheDocument()
      expect(screen.queryByText('Staff One')).toBeNull()
      expect(
        screen.getByRole('status', { name: zhCN.impersonations.loading }),
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
    await user.type(screen.getByLabelText(zhCN.impersonations.start.accountLabel), 'acc-9')
    await user.click(screen.getByRole('button', { name: zhCN.impersonations.start.submit }))

    expect(
      await screen.findByText(zhCN.errors.authn.factor_not_seasoned),
    ).toBeInTheDocument()
    // The refusal stays in the current-impersonation section's region: the
    // section that owns the start form, not the record table's.
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
