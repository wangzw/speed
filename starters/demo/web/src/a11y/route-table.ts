import { act } from '@testing-library/react'
import { accountUiResources } from '@speed/account-ui'
import { adminUiResources } from '@speed/admin-ui'
import { orgCreateUiResources } from '@speed/org-create-ui'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { routes } from '../routes.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from '../test-utils/session.js'
import type { BackendMode } from './backends.js'

export type { BackendMode }

export const ADMIN_PERMISSION_CODES = [
  'tenant.read',
  'authn.account.read',
  'authn.session.read',
  'authn.impersonation.read',
  'authz.role.read',
  'authn.membership.manage',
  'authn.idp.manage',
  'settings.read',
  'sms.template.read',
  'audit.read',
] as const

export function staffSession() {
  return createTestSession(
    authenticatedSnapshot({ system: [...ADMIN_PERMISSION_CODES] }),
  )
}

export const MODES: readonly BackendMode[] = ['data', 'empty', 'error']

export function concretize(pattern: string): string {
  return pattern.replace(/:[A-Za-z_][A-Za-z0-9_]*/g, 'sample')
}

export const ROUTE_PATHS = [
  ...new Set(routes.map((route) => concretize(route.path))),
]

export const EXPECTED_ROUTE_PATHS = [
  '/',
  '/account',
  '/account/profile',
  '/account/security',
  '/account/sessions',
  '/account/preferences',
  '/account/callback',
  '/notifications',
  '/organizations/new',
  '/transfer-requests',
  '/transfer-requests/sample',
  '/sign-in/callback',
  '/invitations/sample',
  '/password-reset',
  '/admin',
  '/admin/tenants',
  '/admin/tenants/new',
  '/admin/tenants/sample',
  '/admin/accounts',
  '/admin/sessions',
  '/admin/impersonations',
  '/admin/permissions',
  '/admin/invitations',
  '/admin/sso',
  '/admin/settings',
  '/admin/sms',
  '/admin/audit',
  '/admin/audit/sample',
  '/organization/admin/invitations',
  '/organization/admin/sso',
  '/organization/admin/roles',
  '/organization/admin/settings',
  '/organization/admin/profile',
] as const

export const ORGANIZATION_MEMBERS_ROLE_DEEP_LINK =
  '/organization/admin/invitations?role=support'

export const SWEEP_PATHS = [
  ...ROUTE_PATHS,
  ORGANIZATION_MEMBERS_ROLE_DEEP_LINK,
] as const

export const ERROR_PLACEHOLDER_BY_PATH: Readonly<Record<string, string>> = {
  '/account': (
    accountUiResources['en-US'] as unknown as {
      readonly profile: { readonly error: { readonly title: string } }
    }
  ).profile.error.title,
  '/admin/audit': (
    adminUiResources['en-US'] as unknown as {
      readonly audit: { readonly error: { readonly title: string } }
    }
  ).audit.error.title,
  '/organization/admin/invitations': demoEnUS.organization.accessFailed.title,
  [ORGANIZATION_MEMBERS_ROLE_DEEP_LINK]:
    demoEnUS.organization.accessFailed.title,
  '/organization/admin/sso': demoEnUS.organization.accessFailed.title,
  '/organization/admin/roles': demoEnUS.organization.accessFailed.title,
  '/organization/admin/settings': demoEnUS.organization.accessFailed.title,
  '/organization/admin/profile': demoEnUS.organization.accessFailed.title,
}

export const EXPECTED_HEADING_BY_PATH: Readonly<Record<string, string>> = {
  '/admin/tenants/new': (
    adminUiResources['en-US'] as unknown as {
      readonly tenants: { readonly create: { readonly title: string } }
    }
  ).tenants.create.title,
  '/organizations/new': (
    orgCreateUiResources['en-US'] as unknown as {
      readonly page: { readonly title: string }
    }
  ).page.title,
  '/organization/admin/invitations': (
    adminUiResources['en-US'] as unknown as {
      readonly invitations: { readonly title: string }
    }
  ).invitations.title,
  [ORGANIZATION_MEMBERS_ROLE_DEEP_LINK]: (
    adminUiResources['en-US'] as unknown as {
      readonly invitations: { readonly title: string }
    }
  ).invitations.title,
  '/organization/admin/sso': (
    adminUiResources['en-US'] as unknown as {
      readonly sso: { readonly title: string }
    }
  ).sso.title,
  '/organization/admin/roles': (
    adminUiResources['en-US'] as unknown as {
      readonly organization: { readonly nav: { readonly roles: string } }
    }
  ).organization.nav.roles,
  '/organization/admin/settings': (
    adminUiResources['en-US'] as unknown as {
      readonly organization: { readonly nav: { readonly settings: string } }
    }
  ).organization.nav.settings,
  '/organization/admin/profile': (
    adminUiResources['en-US'] as unknown as {
      readonly organization: { readonly nav: { readonly profile: string } }
    }
  ).organization.nav.profile,
}

const SETTLE_TICK_CAP = 50

export async function settle(): Promise<void> {
  let previous = ''
  let stableTicks = 0
  for (let tick = 0; tick < SETTLE_TICK_CAP; tick += 1) {
    await act(async () => {
      await Promise.resolve()
      await new Promise<void>((resolve) => {
        if (typeof requestAnimationFrame === 'function') {
          requestAnimationFrame(() => resolve())
        } else {
          setTimeout(resolve, 0)
        }
      })
    })
    const current = document.body.innerHTML
    if (current === previous) {
      stableTicks += 1
      if (stableTicks >= 2) {
        return
      }
    } else {
      stableTicks = 0
      previous = current
    }
  }
  throw new Error(
    `settle() reached its ${SETTLE_TICK_CAP}-tick cap without the DOM ` +
      'stabilising; the scan would run against a page that is still changing',
  )
}

export const SCAN_TENANT_ID = 'acme'
