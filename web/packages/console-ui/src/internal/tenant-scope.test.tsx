/**
 * TenantScope: the tenant selector the tenant-addressed pages carry.
 *
 * The roster read behind it has four presentation states beside its
 * content: loading (disabled, said in the helper text), a 403 (said, no
 * retry), any other failure (said, with a retry that re-reads), and an
 * empty roster (an explanatory line). A tenant seeded from the URL stays
 * shown through every one of them.
 */

import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TenantScope } from './tenant-scope.js'
import { renderWithProviders } from '../../test-utils/render.js'
import {
  createConsoleOperations,
  makeConsoleBackend,
} from '../../test-utils/operations.js'
import type { ConsoleTenant } from '../operations.js'

const TENANT: ConsoleTenant = { id: 'acme', alias: 'Acme', status: 'active' }

/** A host holding the selection, as the pages do. */
function Scope({ initial = '' }: { readonly initial?: string }) {
  const [tenant, setTenant] = useState(initial)
  return <TenantScope value={tenant} onSelect={setTenant} />
}

describe('TenantScope', () => {
  it('is disabled and says it is loading while the roster read runs', () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => new Promise<readonly ConsoleTenant[]>(() => undefined),
      }),
    )
    renderWithProviders(<Scope />, { operations })
    // MUI marks the select's combobox aria-disabled rather than natively
    // disabling a form control.
    expect(screen.getByLabelText('租户')).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByText('正在加载租户…')).toBeInTheDocument()
  })

  it('lists the roster once it settles', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({ listTenants: () => Promise.resolve([TENANT]) }),
    )
    renderWithProviders(<Scope />, { operations })
    await vi.waitFor(() => {
      expect(screen.queryByText('正在加载租户…')).toBeNull()
    })
    expect(screen.getByLabelText('租户')).not.toHaveAttribute(
      'aria-disabled',
      'true',
    )
    await user.click(screen.getByLabelText('租户'))
    expect(await screen.findByRole('option', { name: 'Acme' })).toBeInTheDocument()
  })

  it('says a 403 refused the roster and offers no retry', async () => {
    const listTenants = vi.fn(() =>
      Promise.reject({ status: 403, code: 'client.http.403' }),
    )
    const operations = createConsoleOperations(
      makeConsoleBackend({ listTenants }),
    )
    renderWithProviders(<Scope />, { operations })
    expect(
      await screen.findByText('你没有列出租户的权限。'),
    ).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
    expect(listTenants).toHaveBeenCalledTimes(1)
  })

  it('says the roster failed and retries with a re-read', async () => {
    const user = userEvent.setup()
    let calls = 0
    const listTenants = vi.fn(() => {
      calls += 1
      return calls === 1
        ? Promise.reject(new Error('boom'))
        : Promise.resolve([TENANT])
    })
    const operations = createConsoleOperations(
      makeConsoleBackend({ listTenants }),
    )
    renderWithProviders(<Scope />, { operations })
    expect(await screen.findByText('租户名册加载失败。')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重试' }))
    await vi.waitFor(() => {
      expect(screen.queryByText('租户名册加载失败。')).toBeNull()
    })
    await user.click(screen.getByLabelText('租户'))
    expect(await screen.findByRole('option', { name: 'Acme' })).toBeInTheDocument()
    expect(listTenants).toHaveBeenCalledTimes(2)
  })

  it('explains an empty roster', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({ listTenants: () => Promise.resolve([]) }),
    )
    renderWithProviders(<Scope />, { operations })
    expect(await screen.findByText('尚未登记任何租户。')).toBeInTheDocument()
  })

  it('keeps a tenant seeded from the URL shown, even when the roster fails', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<Scope initial="acme" />, { operations })
    expect(await screen.findByText('租户名册加载失败。')).toBeInTheDocument()
    expect(screen.getByLabelText('租户')).toHaveTextContent('acme')
  })

  it('keeps a seeded tenant shown before the roster settles and when it is empty', async () => {
    let finish!: (tenants: readonly ConsoleTenant[]) => void
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () =>
          new Promise<readonly ConsoleTenant[]>((resolve) => {
            finish = resolve
          }),
      }),
    )
    renderWithProviders(<Scope initial="acme" />, { operations })
    expect(screen.getByLabelText('租户')).toHaveTextContent('acme')
    finish([])
    expect(await screen.findByText('尚未登记任何租户。')).toBeInTheDocument()
    expect(screen.getByLabelText('租户')).toHaveTextContent('acme')
  })
})
