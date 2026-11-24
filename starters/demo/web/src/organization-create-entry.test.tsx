import { beforeEach, describe, expect, it } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { adminUiResources } from '@speed/admin-ui'
import { orgCreateUiResources } from '@speed/org-create-ui'
import { uiKitResources } from '@speed/ui-kit'
import {
  getCurrentTenant,
  getRecordedTenantName,
  setCurrentTenant,
  setTenantNames,
} from './chrome.js'
import demoEnUS from './locales/en-US.json' with { type: 'json' }
import { clearSelfTenants } from './tenant-self.js'
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
  readyFeatures,
} from './test-utils/session.js'

const orgEn = orgCreateUiResources['en-US'] as unknown as {
  readonly page: { readonly title: string }
  readonly fields: {
    readonly name: string
    readonly displayName: string
    readonly description: string
  }
  readonly submit: string
}

const adminEn = adminUiResources['en-US'] as unknown as {
  readonly tenants: { readonly create: { readonly title: string } }
}

const uiEn = uiKitResources['en-US'] as unknown as {
  readonly emptyState: {
    readonly noPermission: { readonly title: string }
  }
}

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  clearSelfTenants()
  setCurrentTenant(null)
  setTenantNames(new Map())
})

function bindSelfTenants(): void {
  bindRequestFn(<T,>(path: string): Promise<T> => {
    if (path === '/api/v1/auth/me/tenants') {
      return Promise.resolve({
        tenants: [
          {
            id: 'personal-1',
            name: 'tester',
            display_name: '',
            kind: 'personal',
          },
          {
            id: 'acme',
            name: 'acme',
            display_name: 'Acme Inc',
            kind: 'business',
          },
        ],
      } as T)
    }
    return Promise.reject(new Error(`unexpected request: ${path}`))
  })
}

function sessionWith(features: ReturnType<typeof readyFeatures>, impersonating = false) {
  const snapshot = authenticatedSnapshot({ features })
  return createTestSession(
    impersonating ? { ...snapshot, impersonating: true } : snapshot,
  )
}

async function openHome(): Promise<void> {
  expect(
    await screen.findByRole('heading', {
      level: 1,
      name: demoEnUS.home.heading,
    }),
  ).toBeInTheDocument()
}

function createCard(): HTMLElement | null {
  const heading = screen.queryByRole('heading', {
    level: 2,
    name: demoEnUS.home.cards.createOrganization.title,
  })
  return heading?.closest('a') ?? null
}

async function expandOrganizations(
  user: ReturnType<typeof userEvent.setup>,
): Promise<void> {
  await openHome()
  await user.click(await screen.findByTestId('user-menu-trigger'))
  const row = await screen.findByRole('menuitem', { name: 'Acme Inc' })
  await user.click(row)
}

describe('the self-service creation home card', () => {
  it('is offered while org.create is served', async () => {
    renderDemoApp({ session: sessionWith(readyFeatures('org.create')) })
    await openHome()
    expect(createCard()).toHaveAttribute('href', '/organizations/new')
  })

  it('is absent when org.create is not served', async () => {
    renderDemoApp({ session: sessionWith(readyFeatures('tenant.admin')) })
    await openHome()
    expect(createCard()).not.toBeInTheDocument()
  })

  it('is absent while impersonating', async () => {
    renderDemoApp({
      session: sessionWith(readyFeatures('org.create'), true),
    })
    await openHome()
    expect(createCard()).not.toBeInTheDocument()
  })
})

describe('the account menu organization row entry', () => {
  it('offers the creation destination in its expansion while org.create is served', async () => {
    const user = userEvent.setup()
    bindSelfTenants()
    renderDemoApp({ session: sessionWith(readyFeatures('org.create')) })
    await expandOrganizations(user)
    expect(
      screen.getByRole('menuitem', {
        name: demoEnUS.organization.create,
      }),
    ).toHaveAttribute('href', '/organizations/new')
  })

  it('is absent when org.create is not served', async () => {
    const user = userEvent.setup()
    bindSelfTenants()
    renderDemoApp({ session: sessionWith(readyFeatures('tenant.admin')) })
    await expandOrganizations(user)
    expect(
      screen.queryByRole('menuitem', {
        name: demoEnUS.organization.create,
      }),
    ).not.toBeInTheDocument()
  })

  it('is absent while impersonating', async () => {
    const user = userEvent.setup()
    bindSelfTenants()
    renderDemoApp({
      session: sessionWith(readyFeatures('org.create'), true),
    })
    await openHome()
    await user.click(await screen.findByTestId('user-menu-trigger'))
    expect(
      screen.queryByRole('menuitem', {
        name: demoEnUS.organization.create,
      }),
    ).not.toBeInTheDocument()
  })
})

describe('the platform registration page guard', () => {
  it('denies a staff session that does not hold tenant.read', async () => {
    window.history.replaceState({}, '', '/admin/tenants/new')
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({
          system: ['audit.read'],
          features: readyFeatures('tenant.admin', 'audit.admin'),
        }),
      ),
    })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: uiEn.emptyState.noPermission.title,
      }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', {
        level: 1,
        name: adminEn.tenants.create.title,
      }),
    ).not.toBeInTheDocument()
  })

  it('renders the registration page for a staff session holding tenant.read', async () => {
    window.history.replaceState({}, '', '/admin/tenants/new')
    renderDemoApp({
      session: createTestSession(
        authenticatedSnapshot({
          system: ['tenant.read'],
          features: readyFeatures('tenant.admin'),
        }),
      ),
    })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: adminEn.tenants.create.title,
      }),
    ).toBeInTheDocument()
  })
})

describe('the self-service creation success flow', () => {
  it('creates, switches to the new organization and lands on its profile page', async () => {
    const user = userEvent.setup()
    let tenants = [
      {
        name: 'tester',
        display_name: '',
        kind: 'personal',
      },
    ]
    bindRequestFn(<T,>(path: string, options?: { method?: string }): Promise<T> => {
      if (path === '/api/v1/auth/me/tenants') {
        return Promise.resolve({ tenants } as T)
      }
      if (path === '/api/v1/orgs' && options?.method === 'POST') {
        const created = {
          name: 'team',
          display_name: 'E2E Team',
          description: '',
          status: 'active',
        }
        tenants = [
          ...tenants,
          {
            name: created.name,
            display_name: created.display_name,
            kind: 'business',
          },
        ]
        return Promise.resolve({ tenant: created } as T)
      }
      if (path === '/api/v1/authz/me/permissions') {
        return Promise.resolve({ platform: [], tenant: [] } as T)
      }
      return Promise.reject(new Error(`unexpected request: ${path}`))
    })
    window.history.replaceState({}, '', '/organizations/new')
    renderDemoApp({ session: sessionWith(readyFeatures('org.create')) })

    expect(
      await screen.findByRole('heading', {
        level: 1,
        name: orgEn.page.title,
      }),
    ).toBeInTheDocument()
    await user.type(screen.getByLabelText(orgEn.fields.name), 'team')
    await user.type(
      screen.getByLabelText(orgEn.fields.displayName),
      'E2E Team',
    )
    await user.click(screen.getByRole('button', { name: orgEn.submit }))

    await waitFor(() => {
      expect(window.location.pathname).toBe('/organization/admin/profile')
    })
    expect(getCurrentTenant()).toBe('team')
    expect(getRecordedTenantName('team')).toBe('team')
  })
})
