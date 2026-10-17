/**
 * TenantRosterPage: the roster-administration page.
 *
 * Its read renders the settled list as a table (with the roster's own
 * status spelling, a value outside the closed set falling to the generic
 * label), and its writes drive the operations bundle -- a successful
 * create invalidates the list and the new row appears, and a refused write
 * renders the answer's code text (a roster collision reading the conflict
 * text), never a raw key. A landed write clears the input that carried it;
 * a refused one keeps it. The identifier and alias inputs mark themselves
 * invalid while a typed value breaks the identifier domain.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TenantRosterPage } from './TenantRosterPage.js'
import type { ConsoleTenant } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  createConsoleOperations,
  makeConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'

describe('TenantRosterPage', () => {
  it('renders the settled empty state when the roster answers no tenants', async () => {
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(<TenantRosterPage />, { operations })
    expect(await screen.findByText('暂无租户')).toBeInTheDocument()
  })

  it('lists tenants with the roster status spelling', async () => {
    const tenants: ConsoleTenant[] = [
      { id: 'acme', alias: 'Acme', status: 'active' },
      { id: 'old', alias: 'Old Co', status: 'disabled' },
    ]
    const operations = createConsoleOperations(
      makeConsoleBackend({ listTenants: () => Promise.resolve(tenants) }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    expect(await screen.findByText('Acme')).toBeInTheDocument()
    expect(screen.getByText('已启用')).toBeInTheDocument()
    expect(screen.getByText('已停用')).toBeInTheDocument()
  })


  it('raises its compact actions to the 44px floor under a coarse pointer', async () => {
    const tenants: ConsoleTenant[] = [
      { id: 'acme', alias: 'Acme', status: 'active' },
      { id: 'old', alias: 'Old Co', status: 'disabled' },
    ]
    const operations = createConsoleOperations(
      makeConsoleBackend({ listTenants: () => Promise.resolve(tenants) }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')
    const gated = [
      screen.getByRole('button', { name: '刷新' }),
      screen.getByRole('button', { name: '为租户 acme 改名' }),
      screen.getByRole('button', { name: '停用租户 acme' }),
      screen.getByRole('button', { name: '启用租户 old' }),
    ]
    for (const control of gated) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
  })

  it('registers a tenant and reflects the invalidated list', async () => {
    const user = userEvent.setup()
    const store: ConsoleTenant[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => Promise.resolve([...store]),
        createTenant: (input) => {
          const tenant: ConsoleTenant = {
            id: input.id,
            alias: input.alias,
            status: 'active',
          }
          store.push(tenant)
          return Promise.resolve(tenant)
        },
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('暂无租户')

    await user.type(screen.getByLabelText('标识'), 'acme')
    await user.type(screen.getByLabelText('别名'), 'Acme Inc')
    await user.click(screen.getByRole('button', { name: '登记' }))

    expect(await screen.findByText('Acme Inc')).toBeInTheDocument()
  })

  it('renders the conflict text when a registration is refused', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
        createTenant: () => Promise.reject({ code: 'tenant.conflict' }),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('暂无租户')

    await user.type(screen.getByLabelText('标识'), 'acme')
    await user.type(screen.getByLabelText('别名'), 'Acme')
    await user.click(screen.getByRole('button', { name: '登记' }))

    expect(
      await screen.findByText('该标识或别名已被其他租户占用。'),
    ).toBeInTheDocument()
  })

  it('disables a tenant through the row control', async () => {
    const user = userEvent.setup()
    const calls: string[] = []
    const tenants: ConsoleTenant[] = [
      { id: 'acme', alias: 'Acme', status: 'active' },
    ]
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => Promise.resolve(tenants),
        setTenantStatus: (input) => {
          calls.push(`${input.id}:${input.status}`)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')
    // The row's enable/disable control names its own tenant, so it is not
    // confusable with another row's toggle.
    await user.click(
      screen.getByRole('button', { name: '停用租户 acme' }),
    )
    expect(calls).toEqual(['acme:disabled'])
  })

  it('names each row control with its own tenant', async () => {
    const tenants: ConsoleTenant[] = [
      { id: 'acme', alias: 'Acme', status: 'active' },
      { id: 'globex', alias: 'Globex', status: 'active' },
    ]
    const operations = createConsoleOperations(
      makeConsoleBackend({ listTenants: () => Promise.resolve(tenants) }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    // The two rows carry the same controls distinguished only by the tenant
    // id baked into each accessible name -- never two identical "Rename"
    // buttons.
    expect(
      screen.getByRole('button', { name: '为租户 acme 改名' }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: '为租户 globex 改名' }),
    ).toBeInTheDocument()
    expect(screen.getByLabelText('租户 acme 的新别名')).toBeInTheDocument()
    expect(screen.getByLabelText('租户 globex 的新别名')).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: '停用租户 acme' }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: '停用租户 globex' }),
    ).toBeInTheDocument()
  })

  it('announces a landed rename and a landed status change', async () => {
    const user = userEvent.setup()
    const tenants: ConsoleTenant[] = [
      { id: 'acme', alias: 'Acme', status: 'active' },
    ]
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => Promise.resolve(tenants),
        setTenantAlias: () => Promise.resolve(),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    // The success notice is a standing status region: mounted clipped until a
    // write lands, then filled with the confirmation. The rename's lands here.
    await user.type(screen.getByLabelText('租户 acme 的新别名'), 'Acme Inc')
    await user.click(screen.getByRole('button', { name: '为租户 acme 改名' }))
    expect(await screen.findByText('别名已更新')).toBeInTheDocument()
  })

  it('renames a tenant through the row control', async () => {
    const user = userEvent.setup()
    const calls: string[] = []
    const tenants: ConsoleTenant[] = [
      { id: 'acme', alias: 'Acme', status: 'active' },
    ]
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => Promise.resolve(tenants),
        setTenantAlias: (input) => {
          calls.push(`${input.id}:${input.alias}`)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')
    await user.type(screen.getByLabelText('租户 acme 的新别名'), 'Acme Inc')
    await user.click(screen.getByRole('button', { name: '为租户 acme 改名' }))
    expect(calls).toEqual(['acme:Acme Inc'])
  })

  it('opens with the page header: the title as h1, a breadcrumb trail, and a refresh action', async () => {
    const tenants: ConsoleTenant[] = [
      { id: 'acme', alias: 'Acme', status: 'active' },
    ]
    const listTenants = vi.fn(() => Promise.resolve(tenants))
    const operations = createConsoleOperations(makeConsoleBackend({ listTenants }))
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    // The page title is the level-one heading.
    expect(
      screen.getByRole('heading', { level: 1, name: '租户' }),
    ).toBeInTheDocument()
    // The breadcrumb trail is its own navigation landmark, running from
    // the console to this page.
    const breadcrumbs = screen.getByRole('navigation')
    expect(within(breadcrumbs).getByText('控制台')).toBeInTheDocument()
    // The refresh action re-reads the roster.
    const user = userEvent.setup()
    const before = listTenants.mock.calls.length
    await user.click(screen.getByRole('button', { name: '刷新' }))
    await waitFor(() => {
      expect(listTenants.mock.calls.length).toBeGreaterThan(before)
    })
  })

  it('renders the roster as a table with column headers', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () =>
          Promise.resolve([{ id: 'acme', alias: 'Acme', status: 'active' }]),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')
    for (const name of ['标识', '别名', '状态', '操作']) {
      expect(
        screen.getByRole('columnheader', { name }),
      ).toBeInTheDocument()
    }
  })

  it('clears the create inputs once a registration lands', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('暂无租户')

    await user.type(screen.getByLabelText('标识'), 'acme')
    await user.type(screen.getByLabelText('别名'), 'Acme Inc')
    await user.click(screen.getByRole('button', { name: '登记' }))

    await waitFor(() => {
      expect(screen.getByLabelText('标识')).toHaveValue('')
      expect(screen.getByLabelText('别名')).toHaveValue('')
    })
  })

  it('keeps the create inputs when a registration is refused', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
        createTenant: () => Promise.reject({ code: 'tenant.conflict' }),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('暂无租户')

    await user.type(screen.getByLabelText('标识'), 'acme')
    await user.type(screen.getByLabelText('别名'), 'Acme')
    await user.click(screen.getByRole('button', { name: '登记' }))

    await screen.findByText('该标识或别名已被其他租户占用。')
    expect(screen.getByLabelText('标识')).toHaveValue('acme')
    expect(screen.getByLabelText('别名')).toHaveValue('Acme')
  })

  it('clears the rename input once a rename lands', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () =>
          Promise.resolve([{ id: 'acme', alias: 'Acme', status: 'active' }]),
        setTenantAlias: () => Promise.resolve(),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    const input = screen.getByLabelText('租户 acme 的新别名')
    await user.type(input, 'Acme Inc')
    await user.click(screen.getByRole('button', { name: '为租户 acme 改名' }))

    await waitFor(() => expect(input).toHaveValue(''))
  })

  it('marks an identifier input invalid while it carries a blank', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('暂无租户')

    const input = screen.getByLabelText('标识')
    await user.type(input, 'Acme 公司')
    expect(input).toHaveAttribute('aria-invalid', 'true')

    await user.clear(input)
    await user.type(input, 'acme')
    expect(input).toHaveAttribute('aria-invalid', 'false')
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    expect(await screen.findByText('租户加载失败')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the read is refused with 403', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    expect(await screen.findByText('权限不足')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
  })

  it('links each row to the tenant detail when given the roster path', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () =>
          Promise.resolve([{ id: 'acme', alias: 'Acme', status: 'active' }]),
      }),
    )
    renderWithProviders(<TenantRosterPage basePath="/console/tenants" />, {
      operations,
    })
    await screen.findByText('Acme')
    expect(screen.getByRole('link', { name: 'acme' })).toHaveAttribute(
      'href',
      '/console/tenants/acme',
    )
  })

  it('has no axe violations', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () =>
          Promise.resolve([{ id: 'acme', alias: 'Acme', status: 'active' }]),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')
    await expectNoAxeViolations()
  })
})
