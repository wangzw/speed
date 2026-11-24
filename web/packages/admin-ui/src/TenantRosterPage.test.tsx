import { afterEach, describe, expect, it, vi } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { mockMatchMedia } from '@speed/test-utils/matchMedia'
import { TenantRosterPage } from './TenantRosterPage.js'
import type { AdminTenant } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { containerMaxWidthOf } from '../test-utils/container-visibility.js'
import {
  createAdminOperations,
  makeAdminBackend,
  tenantFixture,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

function text(
  template: string,
  values: Record<string, string>,
): string {
  return template.replace(/\{\{(\w+)\}\}/g, (match, key: string) =>
    values[key] ?? match,
  )
}

function stubScrollIntoView(): ReturnType<typeof vi.fn> {
  const spy = vi.fn()
  Object.defineProperty(Element.prototype, 'scrollIntoView', {
    configurable: true,
    writable: true,
    value: spy,
  })
  return spy
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

afterEach(() => {
  delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView
})

describe('TenantRosterPage', () => {
  it('renders the settled empty state when the roster answers no tenants', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    renderWithProviders(<TenantRosterPage />, { operations })
    expect(await screen.findByText(zhCN.tenants.empty.title)).toBeInTheDocument()
  })

  it('lists tenants with the roster status spelling', async () => {
    const tenants: AdminTenant[] = [
      tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
      tenantFixture({ name: 'old', display_name: 'Old Co', status: 'disabled' }),
    ]
    const operations = createAdminOperations(
      makeAdminBackend({ listTenants: () => Promise.resolve(tenants) }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    expect(await screen.findByText('Acme')).toBeInTheDocument()
    expect(screen.getByText(zhCN.tenants.status.active)).toBeInTheDocument()
    expect(screen.getByText(zhCN.tenants.status.disabled)).toBeInTheDocument()
  })

  it('renders the deleted state and hides a tombstone row\'s write controls', async () => {
    const tenants: AdminTenant[] = [
      tenantFixture({ name: 'gone', display_name: 'Gone Co', status: 'deleted' }),
    ]
    const operations = createAdminOperations(
      makeAdminBackend({ listTenants: () => Promise.resolve(tenants) }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    expect(await screen.findByText('Gone Co')).toBeInTheDocument()
    expect(screen.getByText(zhCN.tenants.status.deleted)).toBeInTheDocument()
    expect(
      screen.queryByRole('switch', {
        name: text(zhCN.tenants.enableAria, { name: 'gone' }),
      }),
    ).toBeNull()
    expect(
      screen.queryByRole('switch', {
        name: text(zhCN.tenants.disableAria, { name: 'gone' }),
      }),
    ).toBeNull()
  })

  it('shows who disabled a disabled row and nothing on a row that is not disabled', async () => {
    const tenants: AdminTenant[] = [
      tenantFixture({ name: 'op', display_name: 'Operator Off', status: 'disabled', disabled_by: 'operator' }),
      tenantFixture({ name: 'acc', display_name: 'Account Off', status: 'disabled', disabled_by: 'account' }),
      tenantFixture({ name: 'on', display_name: 'Still On', status: 'active' }),
    ]
    const operations = createAdminOperations(
      makeAdminBackend({ listTenants: () => Promise.resolve(tenants) }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Operator Off')
    expect(screen.getByText(zhCN.tenants.disabledBy.operator)).toBeInTheDocument()
    expect(screen.getByText(zhCN.tenants.disabledBy.account)).toBeInTheDocument()
    expect(screen.getAllByText(zhCN.tenants.disabledBy.operator)).toHaveLength(1)
  })

  it('raises its compact actions to the 44px floor under a coarse pointer', async () => {
    const tenants: AdminTenant[] = [
      tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
      tenantFixture({ name: 'old', display_name: 'Old Co', status: 'disabled' }),
    ]
    const operations = createAdminOperations(
      makeAdminBackend({ listTenants: () => Promise.resolve(tenants) }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')
    const acmeToggle = screen.getByRole('switch', {
      name: text(zhCN.tenants.disableAria, { name: 'acme' }),
    })
    const oldToggle = screen.getByRole('switch', {
      name: text(zhCN.tenants.enableAria, { name: 'old' }),
    })
    const acmeRoot = acmeToggle.closest('.MuiSwitch-root')
    const oldRoot = oldToggle.closest('.MuiSwitch-root')
    expect(acmeRoot).not.toBeNull()
    expect(oldRoot).not.toBeNull()
    const gated = [
      screen.getByRole('button', { name: zhCN.common.refresh }),
      acmeRoot as Element,
      oldRoot as Element,
    ]
    for (const control of gated) {
      expect(hasCoarsePointerTouchTarget(control)).toBe(true)
    }
  })

  it('links to the standalone registration page and renders no inline form', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({ listTenants: () => Promise.resolve([]) }),
    )
    renderWithProviders(<TenantRosterPage basePath="/admin/tenants" />, {
      operations,
    })
    await screen.findByText(zhCN.tenants.empty.title)

    expect(
      screen.getByRole('link', { name: zhCN.tenants.register }),
    ).toHaveAttribute('href', '/admin/tenants/new')
    expect(screen.queryAllByRole('textbox')).toHaveLength(0)
  })

  it('offers no registration link when the host gives no roster path', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText(zhCN.tenants.empty.title)
    expect(
      screen.queryByRole('link', { name: zhCN.tenants.register }),
    ).not.toBeInTheDocument()
  })

  it('disables a tenant through the row control', async () => {
    const user = userEvent.setup()
    const calls: string[] = []
    const tenants: AdminTenant[] = [
      tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
    ]
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.resolve(tenants),
        setTenantStatus: (input) => {
          calls.push(`${input.name}:${input.status}`)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')
    await user.click(
      screen.getByRole('switch', { name: text(zhCN.tenants.disableAria, { name: 'acme' }) }),
    )
    expect(calls).toEqual(['acme:disabled'])
  })

  it('keeps focus on the row status switch through an in-flight write and refuses a second activation', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const writes: string[] = []
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () =>
          Promise.resolve([
            tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
          ]),
        setTenantStatus: (input) => {
          writes.push(`${input.name}:${input.status}`)
          return pending.promise
        },
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    const toggle = screen.getByRole('switch', {
      name: text(zhCN.tenants.disableAria, { name: 'acme' }),
    })
    await user.click(toggle)

    expect(writes).toEqual(['acme:disabled'])
    expect(document.activeElement).toBe(toggle)
    expect(toggle).toHaveAttribute('aria-busy', 'true')
    expect(toggle).toHaveAttribute('aria-disabled', 'true')
    expect(toggle).toBeChecked()

    await user.click(toggle)
    await user.keyboard('{Enter}')
    await user.keyboard(' ')
    expect(writes).toEqual(['acme:disabled'])
    expect(toggle).toBeChecked()
    expect(document.activeElement).toBe(toggle)

    await act(async () => {
      pending.resolve()
    })
    await waitForCondition(() => expect(toggle).not.toHaveAttribute('aria-busy'))
    expect(document.activeElement).toBe(toggle)
  })

  it('names each row control with its own tenant', async () => {
    const tenants: AdminTenant[] = [
      tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
      tenantFixture({ name: 'globex', display_name: 'Globex', status: 'active' }),
    ]
    const operations = createAdminOperations(
      makeAdminBackend({ listTenants: () => Promise.resolve(tenants) }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    expect(
      screen.getByRole('switch', { name: text(zhCN.tenants.disableAria, { name: 'acme' }) }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('switch', { name: text(zhCN.tenants.disableAria, { name: 'globex' }) }),
    ).toBeInTheDocument()
  })

  it('announces a landed status change', async () => {
    const user = userEvent.setup()
    const tenants: AdminTenant[] = [
      tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
    ]
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.resolve(tenants),
        setTenantStatus: () => Promise.resolve(),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    await user.click(screen.getByRole('switch', { name: text(zhCN.tenants.disableAria, { name: 'acme' }) }))
    expect(
      await screen.findByText(text(zhCN.tenants.disabledToast, { name: 'acme' })),
    ).toBeInTheDocument()
  })

  it('opens with the page header: the title as h1, a breadcrumb trail, and a refresh action', async () => {
    const tenants: AdminTenant[] = [
      tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
    ]
    const listTenants = vi.fn(() => Promise.resolve(tenants))
    const operations = createAdminOperations(makeAdminBackend({ listTenants }))
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    expect(
      screen.getByRole('heading', { level: 1, name: zhCN.tenants.title }),
    ).toBeInTheDocument()
    const breadcrumbs = screen.getByRole('navigation')
    expect(within(breadcrumbs).getByText(zhCN.shell.title)).toBeInTheDocument()
    const user = userEvent.setup()
    const before = listTenants.mock.calls.length
    await user.click(screen.getByRole('button', { name: zhCN.common.refresh }))
    await waitFor(() => {
      expect(listTenants.mock.calls.length).toBeGreaterThan(before)
    })
  })

  it('disables the refresh while a row write is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const reads: string[] = []
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => {
          reads.push('read')
          return Promise.resolve([
            tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
          ])
        },
        setTenantStatus: () => pending.promise,
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')
    expect(reads).toHaveLength(1)

    await user.click(screen.getByRole('switch', { name: text(zhCN.tenants.disableAria, { name: 'acme' }) }))

    const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
    await waitForCondition(() => {
      expect(refresh).toHaveAttribute('aria-disabled', 'true')
    })
    expect(refresh).not.toHaveAttribute('aria-describedby')
    fireEvent.click(refresh)
    await act(async () => {
      await Promise.resolve()
    })
    expect(reads).toHaveLength(1)

    await act(async () => {
      pending.resolve()
    })
    await waitForCondition(() => {
      expect(refresh).not.toHaveAttribute('aria-disabled', 'true')
    })
  })

  it('disables the refresh while a row write is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const reads: string[] = []
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => {
          reads.push('read')
          return Promise.resolve([
            tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
          ])
        },
        setTenantStatus: () => pending.promise,
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')
    expect(reads).toHaveLength(1)

    await user.click(
      screen.getByRole('switch', {
        name: text(zhCN.tenants.disableAria, { name: 'acme' }),
      }),
    )

    const refresh = screen.getByRole('button', { name: zhCN.common.refresh })
    await waitForCondition(() => {
      expect(refresh).toHaveAttribute('aria-disabled', 'true')
    })
    expect(refresh).not.toHaveAttribute('aria-describedby')
    fireEvent.click(refresh)
    await act(async () => {
      await Promise.resolve()
    })
    expect(reads).toHaveLength(1)

    await act(async () => {
      pending.resolve()
    })
    await waitForCondition(() => {
      expect(refresh).not.toHaveAttribute('aria-disabled', 'true')
    })
  })

  it('renders the roster as a table with column headers', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () =>
          Promise.resolve([tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })]),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')
    for (const name of [
      zhCN.tenantProfile.field.displayName,
      zhCN.tenants.column.name,
      zhCN.tenants.column.status,
      zhCN.tenants.column.actions,
    ]) {
      expect(
        screen.getByRole('columnheader', { name }),
      ).toBeInTheDocument()
    }
  })

  it('inserts host columns before the row-actions column', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () =>
          Promise.resolve([tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })]),
      }),
    )
    renderWithProviders(
      <TenantRosterPage
        extraColumns={[
          {
            id: 'region',
            header: 'Region',
            cell: () => <span>emea</span>,
          },
        ]}
      />,
      { operations },
    )
    await screen.findByText('Acme')

    expect(
      screen.getAllByRole('columnheader').map((cell) => cell.textContent),
    ).toEqual([
      zhCN.tenantProfile.field.displayName,
      zhCN.tenants.column.name,
      zhCN.tenants.column.status,
      'Region',
      zhCN.tenants.column.actions,
    ])
  })

  it('caps the long display name and name at the shared primary width', async () => {
    const displayName = 'AcmeCorporationInternationalHoldingsEMEA'
    const name = 'tenant-emea-production-000123'
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () =>
          Promise.resolve([
            tenantFixture({ name, display_name: displayName, status: 'active' }),
          ]),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText(displayName)

    const nameCell = screen.getByText(displayName).closest('td')
    expect(nameCell).not.toBeNull()
    expect(nameCell).toHaveAttribute('title', displayName)
    expect(containerMaxWidthOf(nameCell!.firstElementChild!)).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )

    const nameText = screen.getByText(name)
    expect(nameText).toHaveAttribute('title', name)
    expect(containerMaxWidthOf(nameText)).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    expect(await screen.findByText(zhCN.tenants.error.title)).toBeInTheDocument()
    expect(screen.getByText(zhCN.tenants.error.description)).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: zhCN.tenants.retry }),
    ).toBeInTheDocument()
  })

  it('renders the permission placeholder with no retry when the read is refused with 403', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () => Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    expect(await screen.findByText(zhCN.forbidden.title)).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: zhCN.tenants.retry }),
    ).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('links each row to the tenant detail when given the roster path', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () =>
          Promise.resolve([tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })]),
      }),
    )
    renderWithProviders(<TenantRosterPage basePath="/admin/tenants" />, {
      operations,
    })
    await screen.findByText('Acme')
    expect(screen.getByRole('link', { name: 'Acme' })).toHaveAttribute(
      'href',
      '/admin/tenants/acme',
    )
  })

  it("keeps each row's refusal in that row's own scope", async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () =>
          Promise.resolve([
            tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
            tenantFixture({ name: 'globex', display_name: 'Globex', status: 'active' }),
          ]),
        setTenantStatus: (input) =>
          input.name === 'acme'
            ? Promise.reject({ code: 'tenant.conflict' })
            : Promise.reject({ code: 'tenant.bad_request' }),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    const acmeToggle = screen.getByRole('switch', {
      name: text(zhCN.tenants.disableAria, { name: 'acme' }),
    })
    await user.click(acmeToggle)
    await waitFor(() =>
      expect(screen.getAllByText(zhCN.errors.tenant.conflict)).toHaveLength(2),
    )

    const globexToggle = screen.getByRole('switch', {
      name: text(zhCN.tenants.disableAria, { name: 'globex' }),
    })
    await user.click(globexToggle)
    await waitFor(() =>
      expect(screen.getAllByText(zhCN.errors.tenant.bad_request)).toHaveLength(
        2,
      ),
    )

    expect(acmeToggle).toHaveAttribute('aria-invalid', 'true')
    expect(globexToggle).toHaveAttribute('aria-invalid', 'true')
    expect(
      screen.getAllByRole('alert').map((alert) => alert.textContent),
    ).toEqual([
      zhCN.errors.tenant.conflict,
      zhCN.errors.tenant.bad_request,
    ])
  })

  it('marks the refused status change inline and jumps under reduced motion', async () => {
    mockMatchMedia(true)
    const scrollIntoView = stubScrollIntoView()
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () =>
          Promise.resolve([tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })]),
        setTenantStatus: () => Promise.reject({ code: 'tenant.bad_request' }),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    const toggle = screen.getByRole('switch', { name: text(zhCN.tenants.disableAria, { name: 'acme' }) })
    await user.click(toggle)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(
      zhCN.errors.tenant.bad_request,
    )
    expect(toggle).toHaveAttribute('aria-invalid', 'true')
    const hintId = toggle.getAttribute('aria-describedby')
    expect(hintId).not.toBeNull()
    expect(document.getElementById(hintId as string)?.textContent).toBe(
      alert.textContent,
    )
    expect(scrollIntoView).toHaveBeenCalledWith({
      behavior: 'auto',
      block: 'center',
    })
  })

  it('names the tenant on each landed row write', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () =>
          Promise.resolve([tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })]),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    await user.click(screen.getByRole('switch', { name: text(zhCN.tenants.disableAria, { name: 'acme' }) }))
    expect(
      await screen.findByText(text(zhCN.tenants.disabledToast, { name: 'acme' })),
    ).toBeInTheDocument()
  })

  it('resets the row scope and inline marker on refresh', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () =>
          Promise.resolve([tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })]),
        setTenantStatus: () =>
          Promise.reject({ status: 400, code: 'client.http.400' }),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')
    const toggle = screen.getByRole('switch', {
      name: text(zhCN.tenants.disableAria, { name: 'acme' }),
    })
    await user.click(toggle)

    await waitFor(() => expect(toggle).toHaveAttribute('aria-invalid', 'true'))
    expect(screen.getByRole('alert')).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )
    await waitFor(() =>
      expect(toggle).toHaveAttribute('aria-invalid', 'false'),
    )
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('clears a landed row confirmation on refresh', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () =>
          Promise.resolve([tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })]),
        setTenantStatus: () => Promise.resolve(),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')
    await user.click(
      screen.getByRole('switch', {
        name: text(zhCN.tenants.disableAria, { name: 'acme' }),
      }),
    )
    const confirmation = text(zhCN.tenants.disabledToast, { name: 'acme' })
    expect(await screen.findByText(confirmation)).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )
    await waitFor(() => {
      expect(screen.queryByText(confirmation)).toBeNull()
    })
  })

  it('has no axe violations', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        listTenants: () =>
          Promise.resolve([tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })]),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')
    await expectNoAxeViolations()
  })
})
