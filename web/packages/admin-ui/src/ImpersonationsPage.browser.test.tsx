import { afterEach, describe, expect, it } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import { page, userEvent } from 'vitest/browser'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { ImpersonationsPage } from './ImpersonationsPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createAdminOperations,
  makeAdminBackend,
  tenantFixture,
} from '../test-utils/operations.js'
import { expectNoHorizontalOverflow } from '../test-utils/browser-overflow.js'
import { clearTenant, seedTenant } from '../test-utils/tenant-url.js'
import type { AdminImpersonation } from './operations.js'
import enUS from './locales/en-US.json' with { type: 'json' }

const FLOOR_WIDTHS = [600, 900, 1200] as const

const TENANTS = [tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })]

const RECORDS: readonly AdminImpersonation[] = [
  {
    id: 'imp-1',
    actor: 'staff-1',
    target: 'account-identifier-with-a-long-value-0001',
    tenant: 'acme',
    session_id: 'session-identifier-with-a-long-value-0001',
    started_at: '2026-10-15T12:00:00Z',
    ends_at: '2026-10-15T13:00:00Z',
    ended_at: null,
  },
]

const idleCallerSession: RequestFn = <T,>(
  path: string,
  options?: RequestOptions,
) => {
  const method = options?.method ?? 'GET'
  if (path === '/api/v1/auth/impersonation' && method === 'GET') {
    return Promise.reject({ status: 404, code: 'client.http.404' })
  }
  return Promise.resolve(undefined as T)
}

afterEach(clearTenant)

describe('ImpersonationsPage horizontal overflow in Chromium', () => {
  for (const width of FLOOR_WIDTHS) {
    it(`keeps the record table inside a ${width}px viewport`, async () => {
      await page.viewport(width, 900)
      seedTenant('acme')
      bindRequestFn(idleCallerSession)

      const operations = createAdminOperations(
        makeAdminBackend({
          listTenants: () => Promise.resolve(TENANTS),
          listImpersonations: () => Promise.resolve(RECORDS),
        }),
      )
      renderWithProviders(<ImpersonationsPage />, { operations, language: 'en-US' })

      await screen.findAllByText('Operator')
      expect(screen.queryByText('staff-1')).toBeNull()
      await expectNoHorizontalOverflow()
    })
  }
})

function deferred<T>(): {
  readonly promise: Promise<T>
  readonly resolve: (value: T) => void
} {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

function nextFrame(): Promise<void> {
  return new Promise((resolve) => {
    requestAnimationFrame(() => resolve())
  })
}

function describeActiveElement(): string {
  const active = document.activeElement
  if (active === null) {
    return 'null'
  }
  const parts = [active.tagName.toLowerCase()]
  const label = active.getAttribute('aria-label')
  const text = (active.textContent ?? '').replace(/\s+/g, ' ').trim().slice(0, 80)
  if (label !== null) {
    parts.push(`aria-label="${label}"`)
  }
  if (text !== '') {
    parts.push(`text="${text}"`)
  }
  return parts.join(' ')
}

describe('ImpersonationsPage focus in Chromium', () => {
  it('keeps focus on the terminate control while its write is in flight', async () => {
    const pending = deferred<void>()
    seedTenant('acme')
    bindRequestFn(idleCallerSession)
    const operations = createAdminOperations(
      makeAdminBackend({
        listImpersonations: () => Promise.resolve(RECORDS),
        revokeImpersonation: () => pending.promise,
      }),
    )
    renderWithProviders(<ImpersonationsPage />, { operations, language: 'en-US' })
    await screen.findAllByText('Operator')

    const terminate = screen.getByRole('button', {
      name: enUS.impersonations.terminate,
    })
    await userEvent.click(terminate)
    await waitFor(() => {
      expect(within(terminate).getByRole('progressbar')).toBeInTheDocument()
    })
    await nextFrame()
    expect(
      terminate,
      `focus while the termination is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()

    pending.resolve()
    await waitFor(() => {
      expect(within(terminate).queryByRole('progressbar')).not.toBeInTheDocument()
    })
    await nextFrame()
    expect(
      terminate,
      `focus after the termination settled; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
  })

  it('lands focus on the current section heading after the impersonation ends', async () => {
    const pending = deferred<void>()
    let grant:
      | {
          readonly id: string
          readonly target: string
          readonly tenant: string
          readonly starts_at: string
          readonly ends_at: string
        }
      | undefined = {
      id: 'imp-1',
      target: 'acc-1',
      tenant: 'acme',
      starts_at: '2026-10-15T12:00:00Z',
      ends_at: '2026-10-15T13:00:00Z',
    }
    const caller: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      const method = options?.method ?? 'GET'
      if (path !== '/api/v1/auth/impersonation') {
        return Promise.resolve(undefined as T)
      }
      if (method === 'GET') {
        return grant === undefined
          ? Promise.reject({ status: 404, code: 'client.http.404' })
          : Promise.resolve({ grant } as T)
      }
      if (method === 'DELETE') {
        return pending.promise.then(() => {
          grant = undefined
          return undefined as T
        })
      }
      return Promise.resolve(undefined as T)
    }
    bindRequestFn(caller)
    const operations = createAdminOperations(makeAdminBackend({}))
    renderWithProviders(<ImpersonationsPage />, { operations, language: 'en-US' })
    await screen.findByText(`${enUS.impersonations.current.target}Operator`)

    const end = screen.getByRole('button', {
      name: enUS.impersonations.current.end,
    })
    await userEvent.click(end)
    await waitFor(() => {
      expect(within(end).getByRole('progressbar')).toBeInTheDocument()
    })
    await nextFrame()
    expect(
      end,
      `focus while the end is in flight; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()

    pending.resolve()
    const heading = await screen.findByRole('heading', {
      name: enUS.impersonations.current.title,
    })
    await waitFor(() => {
      expect(
        heading,
        `focus after the impersonation ended; document.activeElement is ${describeActiveElement()}`,
      ).toHaveFocus()
    })
  })

  it('keeps focus where the operator moved it while the end is in flight', async () => {
    const pending = deferred<void>()
    let grant:
      | {
          readonly id: string
          readonly target: string
          readonly tenant: string
          readonly starts_at: string
          readonly ends_at: string
        }
      | undefined = {
      id: 'imp-1',
      target: 'acc-1',
      tenant: 'acme',
      starts_at: '2026-10-15T12:00:00Z',
      ends_at: '2026-10-15T13:00:00Z',
    }
    const caller: RequestFn = <T,>(path: string, options?: RequestOptions) => {
      const method = options?.method ?? 'GET'
      if (path !== '/api/v1/auth/impersonation') {
        return Promise.resolve(undefined as T)
      }
      if (method === 'GET') {
        return grant === undefined
          ? Promise.reject({ status: 404, code: 'client.http.404' })
          : Promise.resolve({ grant } as T)
      }
      if (method === 'DELETE') {
        return pending.promise.then(() => {
          grant = undefined
          return undefined as T
        })
      }
      return Promise.resolve(undefined as T)
    }
    bindRequestFn(caller)
    const operations = createAdminOperations(makeAdminBackend({}))
    renderWithProviders(<ImpersonationsPage />, { operations, language: 'en-US' })
    await screen.findByText(`${enUS.impersonations.current.target}Operator`)

    const end = screen.getByRole('button', {
      name: enUS.impersonations.current.end,
    })
    await userEvent.click(end)
    await waitFor(() => {
      expect(within(end).getByRole('progressbar')).toBeInTheDocument()
    })

    const elsewhere = screen.getByRole('button', { name: enUS.common.refresh })
    elsewhere.focus()
    await nextFrame()
    expect(elsewhere).toHaveFocus()

    pending.resolve()
    await waitFor(() => {
      expect(
        screen.queryByRole('button', { name: enUS.impersonations.current.end }),
      ).toBeNull()
    })
    await nextFrame()

    expect(
      elsewhere,
      `focus after the impersonation ended; document.activeElement is ${describeActiveElement()}`,
    ).toHaveFocus()
  })
})
