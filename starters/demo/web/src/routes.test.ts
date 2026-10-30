/**
 * The route table's shape: the business slot at the root, the console area
 * mounted under its prefix, and the console-area predicate the shell switch
 * reads.
 */

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
  CONSOLE_LANDING_PATH,
  CONSOLE_PREFIX,
  consoleShellRoutes,
  invitationRoutes,
  isConsolePath,
  navIconFor,
  NOTIFICATIONS_PATH,
  passwordResetRoutes,
  routes,
} from './routes.js'
import {
  ACCOUNT_SECTION_PATHS,
  ACCOUNT_SETUP_PATH,
  INVITATION_PREFIX,
  PASSWORD_RESET_PATH,
} from './paths.js'

describe('the route table', () => {
  it('names the business slot, the account page and its setup page and the notification center, all labelled but for the completion points beside the unlabelled callback', () => {
    // The account entry declares its four panes as pages of their own, each
    // its own declaration, plus the group address that redirects to one.
    expect(businessRoutes.map((route) => route.path)).toEqual([
      '/',
      ACCOUNT_PATH,
      ACCOUNT_SECTION_PATHS.profile,
      ACCOUNT_SECTION_PATHS.security,
      ACCOUNT_SECTION_PATHS.sessions,
      ACCOUNT_SECTION_PATHS.preferences,
      ACCOUNT_SETUP_PATH,
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
    // The panes nest under the account entry rather than listing at the top
    // level, so they carry no label of their own.
    for (const pane of Object.values(ACCOUNT_SECTION_PATHS)) {
      expect(labelled.has(pane), pane).toBe(false)
    }
    // The setup page and the callback are completion points, not navigation
    // destinations.
    expect(labelled.has(ACCOUNT_SETUP_PATH)).toBe(false)
    expect(labelled.has(ACCOUNT_CALLBACK_PATH)).toBe(false)
  })

  it('carries each navigation entry its icon on the declaration itself', () => {
    // The frame builds an entry from a NavItem (whose id is the declaration
    // path) and looks the mark up through navIconFor; the icon therefore has
    // to ride the same declaration that names the entry, not a parallel table.
    for (const route of businessRoutes) {
      if (route.label === undefined) {
        // A completion point contributes no navigation entry, so it carries
        // no mark either.
        expect(navIconFor(route.path), route.path).toBeUndefined()
        continue
      }
      expect(route.icon, route.path).toBeDefined()
      expect(navIconFor(route.path), route.path).toBe(route.icon)
    }
  })

  it('mounts every console page under the console prefix', () => {
    expect(consoleShellRoutes.map((route) => route.path)).toEqual([
      CONSOLE_PREFIX,
      `${CONSOLE_PREFIX}/tenants`,
      `${CONSOLE_PREFIX}/tenants/:id`,
      `${CONSOLE_PREFIX}/accounts`,
      `${CONSOLE_PREFIX}/sessions`,
      `${CONSOLE_PREFIX}/impersonations`,
      `${CONSOLE_PREFIX}/permissions`,
      `${CONSOLE_PREFIX}/invitations`,
      `${CONSOLE_PREFIX}/sso`,
      `${CONSOLE_PREFIX}/settings`,
      `${CONSOLE_PREFIX}/sms`,
      `${CONSOLE_PREFIX}/audit`,
      `${CONSOLE_PREFIX}/audit/:id`,
    ])
    // Every labelled console page carries a permission code; the detail
    // routes are unlabelled (reached from a row, not the navigation) and
    // inherit the guard of the list they belong to.
    for (const route of consoleShellRoutes) {
      if (route.label !== undefined) {
        expect(route.permission, route.path).toBeTruthy()
      }
    }
  })

  it('renders the dashboard at the console prefix itself, labelled', () => {
    const landing = routes.find((route) => route.path === CONSOLE_PREFIX)
    expect(landing).toBeDefined()
    // The console root is the dashboard, a labelled page (it adds the
    // console's own first navigation entry).
    expect(landing?.label).toBeDefined()
    expect(CONSOLE_LANDING_PATH).toBe(CONSOLE_PREFIX)
  })

  it('mounts the auth callback route at its own path, unlabelled', () => {
    expect(authCallbackRoutes.map((route) => route.path)).toEqual([
      AUTH_CALLBACK_PATH,
    ])
    // Unlabelled, so it adds no navigation entry.
    expect(authCallbackRoutes[0]?.label).toBeUndefined()
  })

  it('mounts the invite-acceptance route under its prefix, unlabelled', () => {
    expect(invitationRoutes.map((route) => route.path)).toEqual([
      `${INVITATION_PREFIX}/:token`,
    ])
    // Unlabelled, so it adds no navigation entry; the app renders its page
    // outside both shells (src/app.tsx).
    expect(invitationRoutes[0]?.label).toBeUndefined()
  })

  it('mounts the anonymous reset route at its own path, unlabelled', () => {
    expect(passwordResetRoutes.map((route) => route.path)).toEqual([
      PASSWORD_RESET_PATH,
    ])
    // Unlabelled, so it adds no navigation entry; the app renders its page
    // outside both shells (src/app.tsx) because the viewer arrives without a
    // session.
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
    // The table must actually exercise the vocabulary: the notification
    // surface and every console page are gated.
    expect(gated).toBeGreaterThan(0)
    expect(
      businessRoutes.find((route) => route.path === NOTIFICATIONS_PATH)
        ?.requires,
    ).toContain('notification.inbox')
  })

  it('combines the business slot, the callback, invitation and reset routes and the console pages', () => {
    expect(routes).toHaveLength(
      businessRoutes.length +
        authCallbackRoutes.length +
        invitationRoutes.length +
        passwordResetRoutes.length +
        consoleShellRoutes.length,
    )
  })
})

describe('isConsolePath', () => {
  it('recognizes the console prefix and its descendants', () => {
    expect(isConsolePath(CONSOLE_PREFIX)).toBe(true)
    expect(isConsolePath(`${CONSOLE_PREFIX}/tenants`)).toBe(true)
  })

  it('rejects the tenant paths and a prefix look-alike', () => {
    expect(isConsolePath('/')).toBe(false)
    expect(isConsolePath('/tenants')).toBe(false)
    expect(isConsolePath(`${CONSOLE_PREFIX}x`)).toBe(false)
  })
})

/**
 * The dev server's proxy matcher, mirrored from vite: a key that begins with
 * `^` is a regular expression matched against the request URL (query string
 * included), and every other key is a `startsWith` test. Asserting through
 * this mirror is what makes a plain-string `/api/v1/auth` key fail here: under
 * `startsWith` it swallows `/authx`, and under the anchored form it does not.
 */
function proxyMatches(key: string, url: string): boolean {
  return key.startsWith('^') ? new RegExp(key).test(url) : url.startsWith(key)
}

/** Every page path the route table declares, parameter patterns included. */
const PAGE_PATHS = [...new Set(routes.map((route) => route.path))]

/** The path prefixes the pages mount under, beside the route table's paths. */
const PAGE_PREFIXES = [
  CONSOLE_PREFIX,
  INVITATION_PREFIX,
  ACCOUNT_CALLBACK_PATH,
  AUTH_CALLBACK_PATH,
]

describe('the backend prefix manifest', () => {
  it('lists only concrete, slash-leading prefixes', () => {
    expect(BACKEND_PREFIXES.length).toBeGreaterThan(0)
    for (const prefix of BACKEND_PREFIXES) {
      expect(prefix, `${prefix} is not an absolute path`).toMatch(/^\//)
      // The root is the fallback endpoint's claim, not an API namespace: it
      // would sit above every page path.
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
    // One entry per prefix: no hand-written key rides beside the generated
    // ones, and none is dropped.
    expect(Object.keys(proxy)).toHaveLength(BACKEND_PREFIXES.length)
    for (const prefix of BACKEND_PREFIXES) {
      const key = proxyKeyFor(prefix)
      expect(key.startsWith('^'), `${key} is not an anchored pattern`).toBe(true)
      expect(proxy[key]).toEqual({ target: 'http://backend.test' })
      // Both URL shapes the app sends: a descendant path and a query.
      expect(proxyMatches(key, `${prefix}/x`), `${key} misses ${prefix}/x`).toBe(
        true,
      )
      expect(proxyMatches(key, `${prefix}?q`), `${key} misses ${prefix}?q`).toBe(
        true,
      )
      // A longer first segment is a different namespace.
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
