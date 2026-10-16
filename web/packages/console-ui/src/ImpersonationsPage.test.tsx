/**
 * ImpersonationsPage: the impersonation-record administration page.
 *
 * Its read lists the records, an in-force record (the zero `ended_at`)
 * offers termination and an ended one does not, terminating drives the
 * bundle, and a failed read settles into the retryable error placeholder.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ImpersonationsPage } from './ImpersonationsPage.js'
import type { ConsoleImpersonation } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createConsoleOperations,
  makeConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'

const ZERO = '0001-01-01T00:00:00Z'

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

describe('ImpersonationsPage', () => {
  it('renders the empty state when there is no record', async () => {
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(<ImpersonationsPage />, { operations })
    expect(await screen.findByText('暂无冒充记录')).toBeInTheDocument()
  })

  it('lists records and marks an in-force one', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listImpersonations: () => Promise.resolve([record()]),
      }),
    )
    renderWithProviders(<ImpersonationsPage />, { operations })
    expect(await screen.findByText('staff-1')).toBeInTheDocument()
    expect(screen.getByText('acc-1')).toBeInTheDocument()
    expect(screen.getByText('生效中')).toBeInTheDocument()
  })

  it('marks an ended record with its reason and disables termination', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listImpersonations: () =>
          Promise.resolve([
            record({ ended_at: '2026-10-15T12:30:00Z', end_reason: 'revoked' }),
          ]),
      }),
    )
    renderWithProviders(<ImpersonationsPage />, { operations })
    expect(await screen.findByText('已结束（revoked）')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '终止' })).toBeDisabled()
  })

  it('terminates an in-force impersonation', async () => {
    const user = userEvent.setup()
    const ended: string[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listImpersonations: () => Promise.resolve([record()]),
        revokeImpersonation: (input) => {
          ended.push(input.id)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<ImpersonationsPage />, { operations })
    await screen.findByText('staff-1')
    await user.click(screen.getByRole('button', { name: '终止' }))
    await vi.waitFor(() => {
      expect(ended).toEqual(['imp-1'])
    })
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listImpersonations: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<ImpersonationsPage />, { operations })
    expect(await screen.findByText('冒充记录加载失败')).toBeInTheDocument()
  })

  it('has no axe violations', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listImpersonations: () => Promise.resolve([record()]),
      }),
    )
    renderWithProviders(<ImpersonationsPage />, { operations })
    await screen.findByText('staff-1')
    await expectNoAxeViolations()
  })
})
