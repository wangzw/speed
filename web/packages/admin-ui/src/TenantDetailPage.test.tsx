/**
 * TenantDetailPage: one tenant's administration detail.
 *
 * It reads the tenant by id and renders its fields, drives the
 * enable/disable (status) write, links back to the roster
 * when given the roster's path, splits a refused read into the permission
 * placeholder (403, no retry) or the retryable error placeholder.
 */

import { describe, expect, it, vi } from 'vitest'
import { waitForCondition } from '@speed/test-utils/wait'
import type { ReactElement } from 'react'
import { act, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { FeatureName } from '@speed/api-sdk/features'
import { formatInstant } from '@speed/i18n'
import { TenantDetailPage } from './TenantDetailPage.js'
import type { AdminTenant } from './operations.js'
import type { AdminRoute } from './route.js'
import { AdminFeatureProvider } from './internal/features.js'
import { AdminRouteTableProvider } from './internal/route-table.js'
import { TEST_TIME_ZONE } from '@speed/test-utils/render'
import { renderWithProviders } from '../test-utils/render.js'
import type { RenderWithProvidersOptions } from '../test-utils/render.js'
import { hasCoarsePointerTouchTarget } from '@speed/test-utils/emitted-css'
import {
  createAdminOperations,
  makeAdminBackend,
  tenantFixture,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

/**
 * The sibling declarations a host publishes, as its narrowed table carries
 * them: the admin mount prefix is already in each path.
 */
const RELATED_ROUTES: readonly AdminRoute[] = [
  {
    id: 'permissions',
    path: '/admin/permissions',
    view: () => null,
    requires: ['authz.admin', 'tenant.admin'],
  },
  {
    id: 'invitations',
    path: '/admin/invitations',
    view: () => null,
    requires: ['authn.invitations', 'authn.admin', 'tenant.admin'],
  },
  {
    id: 'sso',
    path: '/admin/sso',
    view: () => null,
    requires: ['authn.admin.sso', 'tenant.admin'],
  },
  {
    id: 'impersonations',
    path: '/admin/impersonations',
    view: () => null,
    requires: ['authn.impersonation', 'authn.admin.impersonations', 'tenant.admin'],
  },
]

/**
 * Renders the page under the shell's two seams: the published (already
 * narrowed) route table and the feature answer. Passing `routes: null`
 * mounts no table at all, as a page rendered outside a shell has none.
 */
function renderDetail(
  ui: ReactElement,
  options: RenderWithProvidersOptions,
  {
    routes = RELATED_ROUTES,
    hasFeature = () => true,
  }: {
    readonly routes?: readonly AdminRoute[] | null
    readonly hasFeature?: (name: FeatureName) => boolean
  } = {},
): ReturnType<typeof renderWithProviders> {
  const tree =
    routes === null ? (
      ui
    ) : (
      <AdminRouteTableProvider routes={routes}>{ui}</AdminRouteTableProvider>
    )
  return renderWithProviders(
    <AdminFeatureProvider hasFeature={hasFeature}>{tree}</AdminFeatureProvider>,
    options,
  )
}

describe('TenantDetailPage', () => {
  it('links to the tenant-addressed pages carrying this tenant', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    renderDetail(
      <TenantDetailPage
        name="acme"
        parentHref="/admin/tenants"
        rootHref="/admin"
      />,
      { operations },
    )
    await screen.findAllByText('Acme')

    // Every link lands on the path its sibling declaration publishes, with
    // the tenant in the query parameter those pages read their selection
    // from.
    const expected = [
      [zhCN.tenantDetail.related.permissions, '/admin/permissions?tenant=acme'],
      [zhCN.tenantDetail.related.invitations, '/admin/invitations?tenant=acme'],
      [zhCN.tenantDetail.related.sso, '/admin/sso?tenant=acme'],
      [
        zhCN.tenantDetail.related.impersonations,
        '/admin/impersonations?tenant=acme',
      ],
    ] as const
    for (const [name, href] of expected) {
      expect(screen.getByRole('link', { name })).toHaveAttribute('href', href)
    }
  })

  it('hides the related-page links when no route table is published', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    renderDetail(
      <TenantDetailPage name="acme" rootHref="/admin" />,
      { operations },
      { routes: null },
    )
    await screen.findAllByText('Acme')
    expect(
      screen.queryByRole('heading', {
        level: 2,
        name: zhCN.tenantDetail.related.title,
      }),
    ).toBeNull()
  })

  it('hides a sibling link when no root is given to resolve a admin-relative path', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    // The declarations the package publishes are admin-relative
    // ('/permissions'), which is indistinguishable from an origin path
    // without the root they hang under.
    const relativeRoutes: readonly AdminRoute[] = [
      { id: 'permissions', path: '/permissions', view: () => null },
    ]
    renderDetail(<TenantDetailPage name="acme" />, { operations }, {
      routes: relativeRoutes,
    })
    await screen.findAllByText('Acme')
    expect(
      screen.queryByRole('link', { name: zhCN.tenantDetail.related.permissions }),
    ).toBeNull()
  })

  it('hides a sibling link whose declaration is absent', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    renderDetail(
      <TenantDetailPage name="acme" rootHref="/admin" />,
      { operations },
      { routes: RELATED_ROUTES.filter((route) => route.id !== 'sso') },
    )
    await screen.findAllByText('Acme')

    expect(
      screen.getByRole('link', { name: zhCN.tenantDetail.related.permissions }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('link', { name: zhCN.tenantDetail.related.sso }),
    ).toBeNull()
  })

  it('hides a sibling link whose target feature is absent', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    renderDetail(
      <TenantDetailPage name="acme" rootHref="/admin" />,
      { operations },
      { hasFeature: (name) => name !== 'authn.admin.sso' },
    )
    await screen.findAllByText('Acme')

    expect(
      screen.queryByRole('link', { name: zhCN.tenantDetail.related.sso }),
    ).toBeNull()
    expect(
      screen.getByRole('link', { name: zhCN.tenantDetail.related.permissions }),
    ).toBeInTheDocument()
  })

  it('overrides a sibling link through relatedHref', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    renderDetail(
      <TenantDetailPage
        name="acme"
        rootHref="/admin"
        relatedHref={{ permissions: '/bespoke/permissions' }}
      />,
      { operations },
    )
    await screen.findAllByText('Acme')

    expect(
      screen.getByRole('link', { name: zhCN.tenantDetail.related.permissions }),
    ).toHaveAttribute('href', '/bespoke/permissions?tenant=acme')
  })

  it('joins the tenant onto an override that already carries a query', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    renderDetail(
      <TenantDetailPage
        name="acme"
        rootHref="/admin"
        relatedHref={{ permissions: '/bespoke/permissions?scope=all' }}
      />,
      { operations },
    )
    await screen.findAllByText('Acme')

    // The override's own query survives and the tenant is still a key, so
    // the target page seeds its selection from it.
    expect(
      screen.getByRole('link', { name: zhCN.tenantDetail.related.permissions }),
    ).toHaveAttribute('href', '/bespoke/permissions?scope=all&tenant=acme')
  })

  it('renders host sections after the page sections and the aside above the back link', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    renderDetail(
      <TenantDetailPage
        name="acme"
        parentHref="/admin/tenants"
        rootHref="/admin"
        sections={(tenant) => <p>{`Section ${tenant.name}`}</p>}
        aside={(tenant) => <p>{`Aside ${tenant.name}`}</p>}
      />,
      { operations },
    )
    await screen.findAllByText('Acme')

    // Both slots receive the loaded tenant, and the host section follows
    // the page's own sections in the main column.
    const section = screen.getByText('Section acme')
    expect(section).toBeInTheDocument()
    expect(screen.getByText('Aside acme')).toBeInTheDocument()
    const related = screen.getByRole('heading', {
      level: 2,
      name: zhCN.tenantDetail.related.title,
    })
    expect(
      related.compareDocumentPosition(section) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()

    // The aside sits above the back link in the side column.
    const aside = screen.getByText('Aside acme')
    const back = screen.getByRole('link', { name: zhCN.tenantDetail.back })
    expect(
      aside.compareDocumentPosition(back) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
  })

  it('raises its refresh action to the 44px floor under a coarse pointer', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    renderWithProviders(<TenantDetailPage name="acme" />, { operations })
    const refresh = await screen.findByRole('button', {
      name: zhCN.common.refresh,
    })
    expect(hasCoarsePointerTouchTarget(refresh)).toBe(true)
  })

  it('renders the tenant fields read by name', async () => {
    const getTenant = vi.fn(() =>
      Promise.resolve(tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' })),
    )
    const operations = createAdminOperations(makeAdminBackend({ getTenant }))
    renderWithProviders(<TenantDetailPage name="acme" />, { operations })

    expect(await screen.findAllByText('Acme')).not.toHaveLength(0)
    expect(screen.getByText(zhCN.tenants.status.active)).toBeInTheDocument()
    expect(getTenant).toHaveBeenCalledWith({ name: 'acme' })
  })

  it('disables the tenant through the main-column control', async () => {
    const user = userEvent.setup()
    const calls: string[] = []
    const operations = createAdminOperations(
      makeAdminBackend({
        getTenant: () => Promise.resolve(tenantFixture({ name: 'acme', status: 'active' })),
        setTenantStatus: (input) => {
          calls.push(`${input.name}:${input.status}`)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<TenantDetailPage name="acme" />, { operations })
    await screen.findByText(zhCN.tenants.status.active)

    await user.click(
      screen.getByRole('switch', { name: zhCN.tenantDetail.actions.disable }),
    )
    expect(calls).toEqual(['acme:disabled'])
  })

  it('refreshes the detail read after a status change so the control flips', async () => {
    const user = userEvent.setup()
    let status: AdminTenant['status'] = 'active'
    const getTenant = vi.fn(() =>
      Promise.resolve(tenantFixture({ name: 'acme', status })),
    )
    const operations = createAdminOperations(
      makeAdminBackend({
        getTenant,
        setTenantStatus: (input) => {
          status = input.status
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<TenantDetailPage name="acme" />, { operations })
    await screen.findByText(zhCN.tenants.status.active)

    await user.click(
      screen.getByRole('switch', { name: zhCN.tenantDetail.actions.disable }),
    )
    // The write invalidates the detail read, so it refetches and the
    // control relabels from the settled status (disable -> enable).
    expect(
      await screen.findByRole('switch', {
        name: zhCN.tenantDetail.actions.enable,
      }),
    ).toBeInTheDocument()
    expect(getTenant.mock.calls.length).toBeGreaterThan(1)
  })

  it('confirms a landed status change and clears the confirmation on refresh', async () => {
    const user = userEvent.setup()
    let status: AdminTenant['status'] = 'active'
    const operations = createAdminOperations(
      makeAdminBackend({
        getTenant: () => Promise.resolve(tenantFixture({ name: 'acme', status })),
        setTenantStatus: (input) => {
          status = input.status
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<TenantDetailPage name="acme" />, { operations })
    await screen.findByText(zhCN.tenants.status.active)

    await user.click(
      screen.getByRole('switch', {
        name: zhCN.tenantDetail.actions.disable,
      }),
    )
    const confirmation = zhCN.tenantDetail.actions.disabled.replace(
      '{{name}}',
      'acme',
    )
    expect(await screen.findByText(confirmation)).toBeInTheDocument()
    // The write invalidates the detail read and it re-settles with the new
    // status; the confirmation stays until the refresh.
    expect(
      await screen.findByRole('switch', {
        name: zhCN.tenantDetail.actions.enable,
      }),
    ).toBeInTheDocument()

    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )
    await waitForCondition(() => {
      expect(screen.queryByText(confirmation)).toBeNull()
    })
  })

  it('clears a refused write through the page refresh', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        getTenant: () =>
          Promise.resolve(tenantFixture({ name: 'acme', status: 'active' })),
        setTenantStatus: () => Promise.reject({ code: 'tenant.conflict' }),
      }),
    )
    renderWithProviders(<TenantDetailPage name="acme" />, { operations })
    await screen.findByText(zhCN.tenants.status.active)

    await user.click(
      screen.getByRole('switch', {
        name: zhCN.tenantDetail.actions.disable,
      }),
    )

    // The refusal resolves through the admin whitelist into the scope's
    // failure alert.
    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.tenant.conflict,
    )

    await user.click(
      screen.getByRole('button', { name: zhCN.common.refresh }),
    )
    await waitForCondition(() => {
      expect(screen.queryByRole('alert')).toBeNull()
    })
  })

  it('disables the refresh while the status write is in flight', async () => {
    const user = userEvent.setup()
    let settle: (() => void) | undefined
    const operations = createAdminOperations(
      makeAdminBackend({
        getTenant: () => Promise.resolve(tenantFixture({ name: 'acme', status: 'active' })),
        setTenantStatus: () =>
          new Promise<void>((resolve) => {
            settle = resolve
          }),
      }),
    )
    renderWithProviders(<TenantDetailPage name="acme" />, { operations })
    await screen.findByText(zhCN.tenants.status.active)

    const refresh = () => screen.getByRole('button', { name: zhCN.common.refresh })
    expect(refresh()).toBeEnabled()

    await user.click(screen.getByRole('switch', { name: zhCN.tenantDetail.actions.disable }))
    expect(refresh()).toBeDisabled()
    // A write-pending disable carries no "choose a scope first" description.
    expect(refresh()).not.toHaveAttribute('aria-describedby')

    await act(async () => {
      settle?.()
    })
    expect(await screen.findByRole('button', { name: zhCN.common.refresh })).toBeEnabled()
  })

  it('links back to the roster when given the roster path', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    renderWithProviders(
      <TenantDetailPage name="acme" parentHref="/admin/tenants" />,
      { operations },
    )
    await screen.findAllByText('Acme')
    expect(
      screen.getByRole('link', { name: zhCN.tenantDetail.back }),
    ).toHaveAttribute(
      'href',
      '/admin/tenants',
    )
  })

  it('renders the permission placeholder with no retry when the read is refused with 403', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        getTenant: () => Promise.reject({ status: 403, code: 'client.http.403' }),
      }),
    )
    renderWithProviders(<TenantDetailPage name="acme" />, { operations })
    expect(await screen.findByText(zhCN.forbidden.title)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: zhCN.tenantDetail.retry })).toBeNull()
  })

  it('renders the unavailable placeholder with a retry that re-reads a 404', async () => {
    const user = userEvent.setup()
    let calls = 0
    const getTenant = vi.fn(() => {
      calls += 1
      return calls === 1
        ? Promise.reject({ status: 404, code: 'client.http.404' })
        : Promise.resolve(
            tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
          )
    })
    const operations = createAdminOperations(makeAdminBackend({ getTenant }))
    renderWithProviders(<TenantDetailPage name="acme" />, { operations })

    // A bare 404 keeps the wording undecided about deletion ("not found or
    // unavailable") and still offers the retry that checks again.
    expect(
      await screen.findByText(zhCN.detail.notFound.title),
    ).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: zhCN.tenantDetail.retry }))
    expect(await screen.findAllByText('Acme')).not.toHaveLength(0)
    expect(getTenant).toHaveBeenCalledTimes(2)
  })

  it('replaces the detail with the unavailable placeholder when a refresh answers 404', async () => {
    const user = userEvent.setup()
    let calls = 0
    const getTenant = vi.fn(() => {
      calls += 1
      return calls === 1
        ? Promise.resolve(
            tenantFixture({ name: 'acme', display_name: 'Acme', status: 'active' }),
          )
        : Promise.reject({ status: 404, code: 'client.http.404' })
    })
    const operations = createAdminOperations(makeAdminBackend({ getTenant }))
    renderWithProviders(<TenantDetailPage name="acme" />, { operations })
    await screen.findAllByText('Acme')

    await user.click(screen.getByRole('button', { name: zhCN.common.refresh }))
    // The 404 makes the older content meaningless: it is replaced, not
    // marked stale.
    expect(
      await screen.findByText(zhCN.detail.notFound.title),
    ).toBeInTheDocument()
    expect(screen.queryAllByText('Acme')).toHaveLength(0)
  })

  it('renders the retryable error placeholder when the read fails otherwise', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        getTenant: () => Promise.reject(new Error('boom')),
      }),
    )
    renderWithProviders(<TenantDetailPage name="acme" />, { operations })
    expect(await screen.findByText(zhCN.tenantDetail.error.title)).toBeInTheDocument()
    expect(
      screen.getByText(zhCN.tenantDetail.error.description),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: zhCN.tenantDetail.retry })).toBeInTheDocument()
  })

  it('shows who disabled a disabled tenant', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        getTenant: () =>
          Promise.resolve(
            tenantFixture({
              name: 'acme', display_name: 'Acme',
              status: 'disabled',
              disabled_by: 'account',
            }),
          ),
      }),
    )
    renderWithProviders(<TenantDetailPage name="acme" />, { operations })
    await screen.findAllByText('Acme')
    expect(
      screen.getByText(zhCN.tenantDetail.field.disabledBy),
    ).toBeInTheDocument()
    expect(screen.getByText(zhCN.tenants.disabledBy.account)).toBeInTheDocument()
  })

  it('shows the operator as the source of an operator disable', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        getTenant: () =>
          Promise.resolve(
            tenantFixture({
              name: 'acme', display_name: 'Acme',
              status: 'disabled',
              disabled_by: 'operator',
            }),
          ),
      }),
    )
    renderWithProviders(<TenantDetailPage name="acme" />, { operations })
    await screen.findAllByText('Acme')
    expect(screen.getByText(zhCN.tenants.disabledBy.operator)).toBeInTheDocument()
    expect(screen.queryByText(zhCN.tenants.disabledBy.account)).toBeNull()
  })

  it('reads a disabled row that carries no source as the operator\'s', async () => {
    // The view attaches the source to every disabled row, so a missing one is
    // the operator's closed-set default rather than a reason to show nothing.
    const operations = createAdminOperations(
      makeAdminBackend({
        getTenant: () =>
          Promise.resolve(tenantFixture({ name: 'acme', display_name: 'Acme', status: 'disabled' })),
      }),
    )
    renderWithProviders(<TenantDetailPage name="acme" />, { operations })
    await screen.findAllByText('Acme')
    expect(screen.getByText(zhCN.tenants.disabledBy.operator)).toBeInTheDocument()
  })

  it('shows no disable source on a tenant that is not disabled', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        getTenant: () => Promise.resolve(tenantFixture({ name: 'acme', display_name: 'Acme' })),
      }),
    )
    renderWithProviders(<TenantDetailPage name="acme" />, { operations })
    await screen.findAllByText('Acme')
    expect(screen.queryByText(zhCN.tenantDetail.field.disabledBy)).toBeNull()
    expect(screen.queryByText(zhCN.tenants.disabledBy.operator)).toBeNull()
    expect(screen.queryByText(zhCN.tenants.disabledBy.account)).toBeNull()
  })

  it('shows the profile fields and creation time in the summary', async () => {
    const createdAt = '2026-10-15T12:00:00Z'
    const operations = createAdminOperations(
      makeAdminBackend({
        getTenant: () =>
          Promise.resolve(
            tenantFixture({
              name: 'acme-inc',
              display_name: 'Acme Incorporated',
              description: 'A canned organization.',
              contact_email: 'ops@acme.example',
              website: 'https://acme.example',
              created_at: createdAt,
            }),
          ),
      }),
    )
    renderWithProviders(<TenantDetailPage name="acme-inc" />, { operations })
    await screen.findByText('Acme Incorporated')

    // The summary states each profile fact once per field; the editable
    // values also sit in the form, so the assertion is scoped to the summary
    // card rather than the whole page.
    const summary = screen.getByRole('region', {
      name: zhCN.tenantDetail.summary,
    })
    expect(within(summary).getByText('Acme Incorporated')).toBeInTheDocument()
    expect(within(summary).getByText('A canned organization.')).toBeInTheDocument()
    expect(within(summary).getByText('ops@acme.example')).toBeInTheDocument()
    expect(
      within(summary).getByText('https://acme.example'),
    ).toBeInTheDocument()
    expect(
      within(summary).getByText(
        formatInstant(createdAt, 'zh-CN', { timeZone: TEST_TIME_ZONE }),
      ),
    ).toBeInTheDocument()
  })

  it('writes the profile through the profile card and reports it there', async () => {
    const user = userEvent.setup()
    const updates: unknown[] = []
    const operations = createAdminOperations(
      makeAdminBackend({
        getTenant: () =>
          Promise.resolve(
            tenantFixture({
              name: 'acme-inc',
              display_name: 'Acme',
              description: 'Old description',
            }),
          ),
        setTenantProfile: (input) => {
          updates.push(input)
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<TenantDetailPage name="acme-inc" />, { operations })
    await screen.findByLabelText(zhCN.tenantProfile.field.displayName)

    await user.clear(
      screen.getByLabelText(zhCN.tenantProfile.field.displayName),
    )
    await user.type(
      screen.getByLabelText(zhCN.tenantProfile.field.displayName),
      'New name',
    )
    await user.click(
      screen.getByRole('button', { name: zhCN.tenantProfile.form.save }),
    )

    await waitForCondition(() => expect(updates).toHaveLength(1))
    expect(updates[0]).toEqual({
      name: 'acme-inc',
      fields: {
        displayName: 'New name',
        description: 'Old description',
        contactEmail: '',
        website: '',
      },
    })
    expect(
      await screen.findByText(
        zhCN.tenantProfile.form.savedNamed.replace('{{name}}', 'New name'),
      ),
    ).toBeInTheDocument()
  })

  it('keeps the profile draft when the profile save is refused', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        getTenant: () =>
          Promise.resolve(
            tenantFixture({ name: 'acme-inc', display_name: '' }),
          ),
        setTenantProfile: () => Promise.reject({ code: 'tenant.conflict' }),
      }),
    )
    renderWithProviders(<TenantDetailPage name="acme-inc" />, { operations })
    await screen.findByLabelText(zhCN.tenantProfile.field.displayName)

    const displayName = screen.getByLabelText(
      zhCN.tenantProfile.field.displayName,
    )
    await user.type(displayName, 'Unsaved')
    await user.click(
      screen.getByRole('button', { name: zhCN.tenantProfile.form.save }),
    )

    expect(await screen.findByRole('alert')).toHaveTextContent(
      zhCN.errors.tenant.conflict,
    )
    // The refused save leaves the draft as typed: the profile scope's
    // failure does not reset the form.
    expect(displayName).toHaveValue('Unsaved')
  })

  it('renders a tombstone read-only with no actions', async () => {
    const operations = createAdminOperations(
      makeAdminBackend({
        getTenant: () =>
          Promise.resolve(
            tenantFixture({ name: 'gone', display_name: 'Gone Co', status: 'deleted' }),
          ),
      }),
    )
    renderWithProviders(<TenantDetailPage name="gone" />, { operations })
    await screen.findAllByText('Gone Co')
    expect(screen.getByText(zhCN.tenants.status.deleted)).toBeInTheDocument()
    // A source describes a disabled tenant, and a tombstone is not one.
    expect(screen.queryByText(zhCN.tenantDetail.field.disabledBy)).toBeNull()
    expect(screen.queryByText(zhCN.tenants.disabledBy.operator)).toBeNull()
    expect(screen.queryByText(zhCN.tenants.disabledBy.account)).toBeNull()
    // Deletion is terminal: the page renders the tenant and no write
    // control at all -- neither the status control nor the profile form.
    expect(screen.queryByText(zhCN.tenantDetail.actions.title)).toBeNull()
    expect(screen.queryByText(zhCN.tenantProfile.form.title)).toBeNull()
    expect(
      screen.queryByRole('button', { name: zhCN.tenantProfile.form.save }),
    ).toBeNull()
  })

  it('has no axe violations', async () => {
    const operations = createAdminOperations(makeAdminBackend())
    renderWithProviders(<TenantDetailPage name="acme" />, { operations })
    await screen.findAllByText('Acme')
    await expectNoAxeViolations()
  })
})
