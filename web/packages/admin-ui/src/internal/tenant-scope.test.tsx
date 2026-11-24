import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TenantScope } from './tenant-scope.js'
import { renderWithProviders } from '../../test-utils/render.js'
import {
  createAdminOperations,
  makeAdminBackend,
  tenantFixture,
} from '../../test-utils/operations.js'
import type { AdminTenant } from '../operations.js'
import zhCN from '../locales/zh-CN.json' with { type: 'json' }

const TENANT: AdminTenant = tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })
const TENANT_LABEL = zhCN.common.tenant

function Scope({ initial = '' }: { readonly initial?: string }) {
  const [tenant, setTenant] = useState(initial)
  return <TenantScope value={tenant} onSelect={setTenant} />
}

describe('TenantScope', () => {
  it('is disabled and says it is loading while the roster read runs', () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => new Promise<readonly AdminTenant[]>(() => undefined),
      }),
    )
    renderWithProviders(<Scope />, { operations })
    expect(screen.getByLabelText(TENANT_LABEL)).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByText(zhCN.common.tenantLoading)).toBeInTheDocument()
  })

  it('lists the roster once it settles', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({ listTenants: () => Promise.resolve([TENANT]) }),
    )
    renderWithProviders(<Scope />, { operations })
    await waitForCondition(() => {
      expect(screen.queryByText(zhCN.common.tenantLoading)).toBeNull()
    })
    expect(screen.getByLabelText(TENANT_LABEL)).not.toHaveAttribute(
      'aria-disabled',
      'true',
    )
    await user.click(screen.getByLabelText(TENANT_LABEL))
    expect(await screen.findByRole('option', { name: 'Acme' })).toBeInTheDocument()
  })

  it('marks the page behind the open tenant menu inert', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({ listTenants: () => Promise.resolve([TENANT]) }),
    )
    const { container } = renderWithProviders(<Scope />, { operations })
    await waitForCondition(() => {
      expect(screen.queryByText(zhCN.common.tenantLoading)).toBeNull()
    })
    await user.click(screen.getByLabelText(TENANT_LABEL))
    expect(await screen.findByRole('option', { name: 'Acme' })).toBeInTheDocument()
    expect(container).toHaveAttribute('inert')
  })

  it('says a 403 refused the roster and offers no retry', async () => {
    const listTenants = vi.fn(() =>
      Promise.reject({ status: 403, code: 'client.http.403' }),
    )
    const operations = createAdminOperations(
      makeAdminBackend({ listTenants }),
    )
    renderWithProviders(<Scope />, { operations })
    expect(
      await screen.findByText(zhCN.common.tenantForbidden),
    ).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: zhCN.common.tenantRetry })).toBeNull()
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
    const operations = createAdminOperations(
      makeAdminBackend({ listTenants }),
    )
    renderWithProviders(<Scope />, { operations })
    expect(await screen.findByText(zhCN.common.tenantLoadFailed)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: zhCN.common.tenantRetry }))
    await waitForCondition(() => {
      expect(screen.queryByText(zhCN.common.tenantLoadFailed)).toBeNull()
    })
    await user.click(screen.getByLabelText(TENANT_LABEL))
    expect(await screen.findByRole('option', { name: 'Acme' })).toBeInTheDocument()
    expect(listTenants).toHaveBeenCalledTimes(2)
  })

  it('explains an empty roster', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({ listTenants: () => Promise.resolve([]) }),
    )
    renderWithProviders(<Scope />, { operations })
    expect(await screen.findByText(zhCN.common.tenantEmpty)).toBeInTheDocument()
  })

  it('keeps a tenant seeded from the URL shown, even when the roster fails', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<Scope initial="acme" />, { operations })
    expect(await screen.findByText(zhCN.common.tenantLoadFailed)).toBeInTheDocument()
    expect(screen.getByLabelText(TENANT_LABEL)).toHaveTextContent('acme')
  })

  it('keeps a seeded tenant shown before the roster settles and when it is empty', async () => {
    let finish!: (tenants: readonly AdminTenant[]) => void
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () =>
          new Promise<readonly AdminTenant[]>((resolve) => {
            finish = resolve
          }),
      }),
    )
    renderWithProviders(<Scope initial="acme" />, { operations })
    expect(screen.getByLabelText(TENANT_LABEL)).toHaveTextContent('acme')
    finish([])
    expect(await screen.findByText(zhCN.common.tenantEmpty)).toBeInTheDocument()
    expect(screen.getByLabelText(TENANT_LABEL)).toHaveTextContent('acme')
  })
})
