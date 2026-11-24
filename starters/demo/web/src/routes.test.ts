import { describe, expect, it } from 'vitest'
import { FEATURE_NAMES } from '@speed/api-sdk/features'
import {
  BACKEND_PREFIXES,
  backendProxyRules,
  fallsUnderPrefix,
  proxyKeyFor,
} from './backend-prefixes.js'
import {
  ACCOUNT_CALLBACK_PATH,
  ACCOUNT_PATH,
  AUTH_CALLBACK_PATH,
  authCallbackRoutes,
  businessRoutes,
  ADMIN_LANDING_PATH,
  ADMIN_PREFIX,
  adminShellRoutes,
  invitationRoutes,
  isAdminPath,
  navIconFor,
  NOTIFICATIONS_PATH,
  passwordResetRoutes,
  routes,
  TRANSFER_REQUESTS_PATH,
  TRANSFER_REQUEST_PATH,
} from './routes.js'
import {
  ACCOUNT_SECTION_PATHS,
  INVITATION_PREFIX,
  ORGANIZATIONS_NEW_PATH,
  PASSWORD_RESET_PATH,
  ORGANIZATION_ADMIN_PREFIX,
} from './paths.js'
import { organizationAdminShellRoutes } from './organization-admin.js'

describe('the route table', () => {
  it('names the business slot, the account page and the notification center, all labelled but for the completion points beside the unlabelled callback', () => {
    expect(businessRoutes.map((route) => route.path)).toEqual([
      '/',
      ACCOUNT_PATH,
      ACCOUNT_SECTION_PATHS.profile,
      ACCOUNT_SECTION_PATHS.security,
      ACCOUNT_SECTION_PATHS.sessions,
      ACCOUNT_SECTION_PATHS.preferences,
      ORGANIZATIONS_NEW_PATH,
      TRANSFER_REQUESTS_PATH,
      TRANSFER_REQUEST_PATH,
      NOTIFICATIONS_PATH,
      ACCOUNT_CALLBACK_PATH,
    ])
    const labelled = new Map(
      businessRoutes
        .filter((route) => route.label !== undefined)
        .map((route) => [route.path, route.label]),
    )
    expect(labelled.has('/')).toBe(true)
    expect(labelled.has(ACCOUNT_PATH)).toBe(true)
    expect(labelled.has(NOTIFICATIONS_PATH)).toBe(true)
    for (const pane of Object.values(ACCOUNT_SECTION_PATHS)) {
      expect(labelled.has(pane), pane).toBe(false)
    }
    expect(labelled.has(ACCOUNT_CALLBACK_PATH)).toBe(false)
    expect(labelled.has(TRANSFER_REQUESTS_PATH)).toBe(false)
    expect(labelled.has(TRANSFER_REQUEST_PATH)).toBe(false)
  })

  it('carries each navigation entry its icon on the declaration itself', () => {
    for (const route of businessRoutes) {
      if (route.label === undefined) {
        expect(navIconFor(route.path), route.path).toBeUndefined()
        continue
      }
      expect(route.icon, route.path).toBeDefined()
      expect(navIconFor(route.path), route.path).toBe(route.icon)
    }
  })

  it('mounts every admin page under the admin prefix', () => {
    expect(adminShellRoutes.map((route) => route.path)).toEqual([
      ADMIN_PREFIX,
      `${ADMIN_PREFIX}/tenants`,
      `${ADMIN_PREFIX}/tenants/new`,
      `${ADMIN_PREFIX}/tenants/:name`,
      `${ADMIN_PREFIX}/accounts`,
      `${ADMIN_PREFIX}/sessions`,
      `${ADMIN_PREFIX}/impersonations`,
      `${ADMIN_PREFIX}/permissions`,
      `${ADMIN_PREFIX}/invitations`,
      `${ADMIN_PREFIX}/sso`,
      `${ADMIN_PREFIX}/settings`,
      `${ADMIN_PREFIX}/sms`,
      `${ADMIN_PREFIX}/audit`,
      `${ADMIN_PREFIX}/audit/:id`,
    ])
    for (const route of adminShellRoutes) {
      if (route.label !== undefined) {
        expect(route.permission, route.path).toBeTruthy()
      }
    }
  })

  it('renders the dashboard at the admin prefix itself, labelled', () => {
    const landing = routes.find((route) => route.path === ADMIN_PREFIX)
    expect(landing).toBeDefined()
    expect(landing?.label).toBeDefined()
    expect(ADMIN_LANDING_PATH).toBe(ADMIN_PREFIX)
  })

  it('mounts the auth callback route at its own path, unlabelled', () => {
    expect(authCallbackRoutes.map((route) => route.path)).toEqual([
      AUTH_CALLBACK_PATH,
    ])
    expect(authCallbackRoutes[0]?.label).toBeUndefined()
  })

  it('mounts the invite-acceptance route under its prefix, unlabelled and ungated', () => {
    expect(invitationRoutes.map((route) => route.path)).toEqual([
      `${INVITATION_PREFIX}/:token`,
    ])
    expect(invitationRoutes[0]?.label).toBeUndefined()
    expect(invitationRoutes[0]?.requires).toBeUndefined()
  })

  it('mounts the anonymous reset route at its own path, unlabelled', () => {
    expect(passwordResetRoutes.map((route) => route.path)).toEqual([
      PASSWORD_RESET_PATH,
    ])
    expect(passwordResetRoutes[0]?.label).toBeUndefined()
    expect(passwordResetRoutes[0]?.requires).toBeUndefined()
  })

  it('gates only on names the platform feature vocabulary declares', () => {
    const known = new Set<string>(FEATURE_NAMES)
    let gated = 0
    for (const route of routes) {
      for (const name of route.requires ?? []) {
        gated += 1
        expect(
          known.has(name),
          `${route.path} requires ${name}, which FEATURE_NAMES does not declare`,
        ).toBe(true)
      }
    }
    expect(gated).toBeGreaterThan(0)
    expect(
      businessRoutes.find((route) => route.path === NOTIFICATIONS_PATH)
        ?.requires,
    ).toContain('notification.inbox')
    expect(
      businessRoutes.find((route) => route.path === ORGANIZATIONS_NEW_PATH)
        ?.requires,
    ).toContain('org.create')
  })

  it('combines the business slot, the callback, invitation and reset routes and the admin pages', () => {
    expect(routes).toHaveLength(
      businessRoutes.length +
        authCallbackRoutes.length +
        invitationRoutes.length +
        passwordResetRoutes.length +
        adminShellRoutes.length +
        organizationAdminShellRoutes.length,
    )
  })

  it('mounts the organization declarations under their own prefix, outside the admin prefix', () => {
    expect(organizationAdminShellRoutes.map((route) => route.path)).toEqual([
      `${ORGANIZATION_ADMIN_PREFIX}/profile`,
      `${ORGANIZATION_ADMIN_PREFIX}/invitations`,
      `${ORGANIZATION_ADMIN_PREFIX}/sso`,
      `${ORGANIZATION_ADMIN_PREFIX}/roles`,
      `${ORGANIZATION_ADMIN_PREFIX}/settings`,
    ])
    for (const route of organizationAdminShellRoutes) {
      expect(isAdminPath(route.path)).toBe(false)
    }
  })

  it('keeps the five organization declarations with their tenant-domain codes', () => {
    expect(organizationAdminShellRoutes.map((route) => route.id)).toEqual([
      'organization-profile',
      'organization-invitations',
      'organization-sso',
      'organization-roles',
      'organization-settings',
    ])
    expect(
      organizationAdminShellRoutes.map((route) => route.permission),
    ).toEqual([
      'tenant.profile.read',
      'authn.membership.manage',
      'authn.idp.manage',
      'authz.tenant.role.read',
      'settings.tenant.read',
    ])
    expect(organizationAdminShellRoutes.map((route) => route.requires)).toEqual([
      ['tenant.profile'],
      ['authn.admin', 'authn.invitations'],
      ['authn.admin', 'authn.admin.sso'],
      ['authz.tenantadmin', 'authn.admin'],
      ['settings.tenantadmin'],
    ])
  })
})

describe('isAdminPath', () => {
  it('recognizes the admin prefix and its descendants', () => {
    expect(isAdminPath(ADMIN_PREFIX)).toBe(true)
    expect(isAdminPath(`${ADMIN_PREFIX}/tenants`)).toBe(true)
  })

  it('rejects the tenant paths and a prefix look-alike', () => {
    expect(isAdminPath('/')).toBe(false)
    expect(isAdminPath('/tenants')).toBe(false)
    expect(isAdminPath(`${ADMIN_PREFIX}x`)).toBe(false)
  })
})

function proxyMatches(key: string, url: string): boolean {
  return key.startsWith('^') ? new RegExp(key).test(url) : url.startsWith(key)
}

const PAGE_PATHS = [...new Set(routes.map((route) => route.path))]

const PAGE_PREFIXES = [
  ADMIN_PREFIX,
  INVITATION_PREFIX,
  ACCOUNT_CALLBACK_PATH,
  AUTH_CALLBACK_PATH,
]

describe('the backend prefix manifest', () => {
  it('lists only concrete, slash-leading prefixes', () => {
    expect(BACKEND_PREFIXES.length).toBeGreaterThan(0)
    for (const prefix of BACKEND_PREFIXES) {
      expect(prefix, `${prefix} is not an absolute path`).toMatch(/^\//)
      expect(prefix, 'the manifest lists the root').not.toBe('/')
      expect(prefix, `${prefix} ends with a slash`).not.toMatch(/\/$/)
    }
    expect(new Set(BACKEND_PREFIXES).size).toBe(BACKEND_PREFIXES.length)
  })

  it('keeps every page path and page prefix outside every claimed prefix', () => {
    for (const path of [...PAGE_PATHS, ...PAGE_PREFIXES]) {
      for (const prefix of BACKEND_PREFIXES) {
        expect(
          fallsUnderPrefix(path, prefix),
          `the page path ${path} falls under the backend prefix ${prefix}`,
        ).toBe(false)
      }
    }
  })

  it('matches a claimed prefix at a segment boundary, not inside a segment', () => {
    expect(fallsUnderPrefix('/api/v1/auth', '/api/v1/auth')).toBe(true)
    expect(fallsUnderPrefix('/api/v1/auth/me', '/api/v1/auth')).toBe(true)
    expect(fallsUnderPrefix('/api/v1/system', '/api/v1/auth')).toBe(false)
    expect(fallsUnderPrefix('/api/v1/system/roles', '/api/v1/auth')).toBe(false)
  })

  it('generates one segment-anchored proxy key per claimed prefix', () => {
    const proxy = backendProxyRules('http://backend.test')
    expect(Object.keys(proxy)).toHaveLength(BACKEND_PREFIXES.length)
    for (const prefix of BACKEND_PREFIXES) {
      const key = proxyKeyFor(prefix)
      expect(key.startsWith('^'), `${key} is not an anchored pattern`).toBe(true)
      expect(proxy[key]).toEqual({ target: 'http://backend.test' })
      expect(proxyMatches(key, `${prefix}/x`), `${key} misses ${prefix}/x`).toBe(
        true,
      )
      expect(proxyMatches(key, `${prefix}?q`), `${key} misses ${prefix}?q`).toBe(
        true,
      )
      expect(proxyMatches(key, `${prefix}x`), `${key} matches ${prefix}x`).toBe(
        false,
      )
      expect(
        proxyMatches(key, `${prefix}x/x`),
        `${key} matches ${prefix}x/x`,
      ).toBe(false)
    }
  })

  it('never lets a proxy key claim a page path', () => {
    const keys = Object.keys(backendProxyRules('http://backend.test'))
    for (const key of keys) {
      for (const path of PAGE_PATHS) {
        expect(
          proxyMatches(key, path),
          `the proxy key ${key} matches the page path ${path}`,
        ).toBe(false)
      }
    }
  })

  it('forwards both /api/v1/auth and /api/v1/system without one swallowing the other', () => {
    const keys = Object.keys(backendProxyRules('http://backend.test'))
    const forwards = (url: string): boolean =>
      keys.some((key) => proxyMatches(key, url))
    expect(forwards('/api/v1/auth/methods')).toBe(true)
    expect(forwards('/api/v1/system/permissions')).toBe(true)
    expect(forwards('/authx')).toBe(false)
  })
})
