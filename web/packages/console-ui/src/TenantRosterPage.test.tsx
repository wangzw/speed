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

import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { mockMatchMedia } from '@speed/test-utils/matchMedia'
import { TenantRosterPage } from './TenantRosterPage.js'
import type { ConsoleTenant } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import { containerMaxWidthOf } from '../test-utils/container-visibility.js'
import {
  createConsoleOperations,
  makeConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'

/** jsdom implements no layout; a scroll observer installs this stub. */
function stubScrollIntoView(): ReturnType<typeof vi.fn> {
  const spy = vi.fn()
  Object.defineProperty(Element.prototype, 'scrollIntoView', {
    configurable: true,
    writable: true,
    value: spy,
  })
  return spy
}

/** A promise the test settles by hand, for observing an in-flight write. */
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
    await user.type(screen.getByLabelText('别名'), 'Acme-Inc')
    await user.click(screen.getByRole('button', { name: '登记' }))

    expect(await screen.findByText('Acme-Inc')).toBeInTheDocument()
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
    await user.type(screen.getByLabelText('租户 acme 的新别名'), 'Acme-Inc')
    await user.click(screen.getByRole('button', { name: '为租户 acme 改名' }))
    expect(
      await screen.findByText('租户「acme」的别名已更新'),
    ).toBeInTheDocument()
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
    await user.type(screen.getByLabelText('租户 acme 的新别名'), 'Acme-Inc')
    await user.click(screen.getByRole('button', { name: '为租户 acme 改名' }))
    expect(calls).toEqual(['acme:Acme-Inc'])
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

  it('disables the refresh while a row write is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<void>()
    const reads: string[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => {
          reads.push('read')
          return Promise.resolve([
            { id: 'acme', alias: 'Acme', status: 'active' },
          ])
        },
        setTenantAlias: () => pending.promise,
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')
    expect(reads).toHaveLength(1)

    await user.type(screen.getByLabelText('租户 acme 的新别名'), 'Acme-Inc')
    await user.click(screen.getByRole('button', { name: '为租户 acme 改名' }))

    // The row's scope is in flight, so the page-level refresh is
    // natively disabled. The scope is chosen, so the disabled control
    // carries no "choose a scope first" description.
    const refresh = screen.getByRole('button', { name: '刷新' })
    await vi.waitFor(() => {
      expect(refresh).toBeDisabled()
    })
    expect(refresh).not.toHaveAttribute('aria-describedby')
    // A forced activation issues no read while the write it would race
    // is in flight.
    fireEvent.click(refresh)
    await act(async () => {
      await Promise.resolve()
    })
    expect(reads).toHaveLength(1)

    await act(async () => {
      pending.resolve()
    })
    await vi.waitFor(() => {
      expect(refresh).not.toBeDisabled()
    })
  })

  it('disables the refresh while a registration is in flight', async () => {
    const user = userEvent.setup()
    const pending = deferred<ConsoleTenant>()
    const reads: string[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => {
          reads.push('read')
          return Promise.resolve([])
        },
        createTenant: () => pending.promise,
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('暂无租户')
    expect(reads).toHaveLength(1)

    await user.type(screen.getByLabelText('标识'), 'acme')
    await user.type(screen.getByLabelText('别名'), 'Acme')
    await user.click(screen.getByRole('button', { name: '登记' }))

    const refresh = screen.getByRole('button', { name: '刷新' })
    await vi.waitFor(() => {
      expect(refresh).toBeDisabled()
    })
    expect(refresh).not.toHaveAttribute('aria-describedby')
    fireEvent.click(refresh)
    await act(async () => {
      await Promise.resolve()
    })
    expect(reads).toHaveLength(1)

    await act(async () => {
      pending.resolve({ id: 'acme', alias: 'Acme', status: 'active' })
    })
    await vi.waitFor(() => {
      expect(refresh).not.toBeDisabled()
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

  it('caps the long alias and identifier at the shared primary width', async () => {
    const id = 'tenant-emea-production-000123'
    const alias = 'AcmeCorporationInternationalHoldingsEMEA'
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => Promise.resolve([{ id, alias, status: 'active' }]),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText(alias)

    // The alias is the page's primary identity: it never hides, and the
    // shared cap (160px below `md`, 200px from `md` up) keeps a long
    // value from pushing the row's controls out of the table's container.
    // The complete value rides the cell's title.
    const aliasText = screen.getByText(alias)
    expect(aliasText).toHaveAttribute('title', alias)
    expect(containerMaxWidthOf(aliasText)).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )

    // The identifier is host-supplied and unbounded; the cap counts the
    // copy control beside it, so the whole cell stays inside the same
    // budget while the title and the control keep the full value.
    const idCell = screen.getByText(id).closest('td')
    expect(idCell).not.toBeNull()
    expect(idCell).toHaveAttribute('title', id)
    expect(containerMaxWidthOf(idCell!.firstElementChild!)).toEqual(
      new Map([
        [0, 160],
        [900, 200],
      ]),
    )
  })

  it('clears the create inputs once a registration lands', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(makeConsoleBackend())
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('暂无租户')

    await user.type(screen.getByLabelText('标识'), 'acme')
    await user.type(screen.getByLabelText('别名'), 'Acme-Inc')
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
    await user.type(input, 'Acme-Inc')
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

  it('withholds a registration whose identifier or alias carries a blank', async () => {
    const user = userEvent.setup()
    const created: string[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        createTenant: (input) => {
          created.push(`${input.id}:${input.alias}`)
          return Promise.resolve({
            id: input.id,
            alias: input.alias,
            status: 'active',
          })
        },
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('暂无租户')

    const id = screen.getByLabelText('标识')
    const alias = screen.getByLabelText('别名')
    const submit = screen.getByRole('button', { name: '登记' })

    // A blank in either field is the identifier domain's refusal. The
    // client rule is the gate: the input says why, the submit is inert,
    // and no request is made — the server's generic bad-request text is
    // never what the operator meets.
    await user.type(id, 'acme 1')
    await user.type(alias, 'Acme')
    expect(id).toHaveAttribute('aria-invalid', 'true')
    expect(submit).toBeDisabled()
    fireEvent.click(submit)
    // The write is dispatched asynchronously, so a request made from the
    // click has not landed yet in this tick. Flush the queue before
    // claiming none was made; the disabled mirror alone is not proof.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(created).toEqual([])

    await user.clear(id)
    await user.type(id, 'acme')
    await user.clear(alias)
    await user.type(alias, 'Acme Inc')
    expect(alias).toHaveAttribute('aria-invalid', 'true')
    expect(submit).toBeDisabled()
    fireEvent.click(submit)
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(created).toEqual([])
  })

  it('withholds a rename whose alias carries a blank', async () => {
    const user = userEvent.setup()
    const calls: string[] = []
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () =>
          Promise.resolve([{ id: 'acme', alias: 'Acme', status: 'active' }]),
        setTenantAlias: (input) => {
          calls.push(`${input.id}:${input.alias}`)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    const input = screen.getByLabelText('租户 acme 的新别名')
    await user.type(input, 'Acme Inc')
    const submit = screen.getByRole('button', { name: '为租户 acme 改名' })
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(submit).toBeDisabled()
    fireEvent.click(submit)
    // The write is dispatched asynchronously, so a request made from the
    // click has not landed yet in this tick. Flush the queue before
    // claiming none was made; the disabled mirror alone is not proof.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(calls).toEqual([])
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    expect(await screen.findByText('租户加载失败')).toBeInTheDocument()
    expect(screen.getByText('名册未能完成这次请求。')).toBeInTheDocument()
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
    // A read's refusal never settles into a write scope's slot.
    expect(screen.queryByRole('alert')).toBeNull()
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

  it("keeps each row's refusal in that row's own scope", async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () =>
          Promise.resolve([
            { id: 'acme', alias: 'Acme', status: 'active' },
            { id: 'globex', alias: 'Globex', status: 'active' },
          ]),
        setTenantAlias: (input) =>
          input.id === 'acme'
            ? Promise.reject({ code: 'tenant.conflict' })
            : Promise.resolve(),
        setTenantStatus: (input) =>
          input.id === 'globex'
            ? Promise.reject({ code: 'tenant.bad_request' })
            : Promise.resolve(),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    const acmeAlias = screen.getByLabelText('租户 acme 的新别名')
    await user.type(acmeAlias, 'Acme-Inc')
    await user.click(
      screen.getByRole('button', { name: '为租户 acme 改名' }),
    )
    // The same resolved text stands in the row marker and in the region.
    expect(
      await screen.findAllByText('该标识或别名已被其他租户占用。'),
    ).toHaveLength(2)

    const globexToggle = screen.getByRole('button', {
      name: '停用租户 globex',
    })
    await user.click(globexToggle)
    expect(
      await screen.findAllByText('服务端无法读取该请求，请检查表单后重试。'),
    ).toHaveLength(2)

    // Each row marks its own failure; the other row is untouched.
    expect(acmeAlias).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByLabelText('租户 globex 的新别名')).toHaveAttribute(
      'aria-invalid',
      'false',
    )
    expect(globexToggle).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByRole('button', { name: '停用租户 acme' })).toHaveAttribute(
      'aria-invalid',
      'false',
    )
    // Two results, one per row scope: no page-level aggregate.
    expect(
      screen.getAllByRole('alert').map((alert) => alert.textContent),
    ).toEqual([
      '该标识或别名已被其他租户占用。',
      '服务端无法读取该请求，请检查表单后重试。',
    ])
  })

  it('marks the refused rename inline and brings its region into view', async () => {
    mockMatchMedia(false)
    const scrollIntoView = stubScrollIntoView()
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () =>
          Promise.resolve([{ id: 'acme', alias: 'Acme', status: 'active' }]),
        setTenantAlias: () => Promise.reject({ code: 'tenant.conflict' }),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    const input = screen.getByLabelText('租户 acme 的新别名')
    await user.type(input, 'Acme-Inc')
    await user.click(
      screen.getByRole('button', { name: '为租户 acme 改名' }),
    )

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('该标识或别名已被其他租户占用。')
    expect(input).toHaveAttribute('aria-invalid', 'true')
    const hintId = input.getAttribute('aria-describedby')
    expect(hintId).not.toBeNull()
    expect(document.getElementById(hintId as string)?.textContent).toBe(
      alert.textContent,
    )
    expect(scrollIntoView).toHaveBeenCalledWith({
      behavior: 'smooth',
      block: 'center',
    })
    await expectNoAxeViolations()
  })

  it('marks the refused status change inline and jumps under reduced motion', async () => {
    mockMatchMedia(true)
    const scrollIntoView = stubScrollIntoView()
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () =>
          Promise.resolve([{ id: 'acme', alias: 'Acme', status: 'active' }]),
        setTenantStatus: () => Promise.reject({ code: 'tenant.bad_request' }),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    const toggle = screen.getByRole('button', { name: '停用租户 acme' })
    await user.click(toggle)

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(
      '服务端无法读取该请求，请检查表单后重试。',
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

  it('names the tenant on each landed write', async () => {
    const user = userEvent.setup()
    const operations = createConsoleOperations(
      makeConsoleBackend({
        listTenants: () =>
          Promise.resolve([{ id: 'acme', alias: 'Acme', status: 'active' }]),
      }),
    )
    renderWithProviders(<TenantRosterPage />, { operations })
    await screen.findByText('Acme')

    // The registration names the tenant it created.
    await user.type(screen.getByLabelText('标识'), 'globex')
    await user.type(screen.getByLabelText('别名'), 'Globex')
    await user.click(screen.getByRole('button', { name: '登记' }))
    expect(
      await screen.findByText('租户「globex」已登记'),
    ).toBeInTheDocument()

    // The row's rename names its tenant.
    await user.type(screen.getByLabelText('租户 acme 的新别名'), 'Acme-Inc')
    await user.click(
      screen.getByRole('button', { name: '为租户 acme 改名' }),
    )
    expect(
      await screen.findByText('租户「acme」的别名已更新'),
    ).toBeInTheDocument()

    // The row's status change names its tenant and the new state.
    await user.click(screen.getByRole('button', { name: '停用租户 acme' }))
    expect(
      await screen.findByText('租户「acme」已停用'),
    ).toBeInTheDocument()
    // The registration's confirmation lives in the other scope.
    expect(screen.getByText('租户「globex」已登记')).toBeInTheDocument()
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
