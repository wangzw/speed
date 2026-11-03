/**
 * The route/state matrix both accessibility sweeps walk.
 *
 * The jsdom gate (routes.test.tsx) and the browser gate
 * (routes.browser.test.tsx) must sweep the same routes in the same states:
 * otherwise the browser leg could settle WCAG 2.5.3 on a route set that
 * drifted from the one the jsdom gate covers. The route list is derived from
 * the app's own route table (the copied product's `routes`) and the state
 * list is fixed, so a page added there is scanned by both without either file
 * changing.
 */

import { act } from '@testing-library/react'
import { accountUiResources } from '@speed/account-ui'
import { adminUiResources } from '@speed/admin-ui'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { routes } from '../routes.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from '../test-utils/session.js'
import type { BackendMode } from './backends.js'

export type { BackendMode }

/**
 * Every platform permission code an admin page's route guard checks, so the
 * staff session passes every guard and each admin page renders inside the
 * frame rather than the no-permission surface. The set is drawn from the
 * admin-ui declarations' own permission codes.
 */
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

/** A platform-staff session holding every admin permission code. */
export function staffSession() {
  return createTestSession(
    authenticatedSnapshot({ system: [...ADMIN_PERMISSION_CODES] }),
  )
}

/** The three states each route is swept in. */
export const MODES: readonly BackendMode[] = ['data', 'empty', 'error']

/**
 * Replaces a declaration's `:name` parameter with a concrete segment, so a
 * path pattern becomes a URL the router matches and the backend answers.
 */
export function concretize(pattern: string): string {
  return pattern.replace(/:[A-Za-z_][A-Za-z0-9_]*/g, 'sample')
}

/** The distinct URL paths the app's route table declares. */
export const ROUTE_PATHS = [
  ...new Set(routes.map((route) => concretize(route.path))),
]

/**
 * The concrete paths the composition ships, pinned so a route that drops out
 * of the table cannot leave the sweep silently -- the coverage guard in
 * routes.test.tsx asserts each of these is still enumerated. Drawn from the
 * app's route table: the business routes (the home page, the account settings'
 * group address and its profile, security, sessions and preferences panes, the
 * notification centre, the binding callback), the auth and invitation
 * landings, the admin area under ADMIN_PREFIX, and the workspace admin area
 * under WORKSPACE_ADMIN_PREFIX, every `:param` concretized to `sample` as
 * `concretize` does.
 */
export const EXPECTED_ROUTE_PATHS = [
  '/',
  '/account',
  '/account/profile',
  '/account/security',
  '/account/sessions',
  '/account/preferences',
  '/account/callback',
  '/notifications',
  '/sign-in/callback',
  '/invitations/sample',
  '/password-reset',
  '/admin',
  '/admin/tenants',
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
  '/workspace/admin/invitations',
  '/workspace/admin/sso',
] as const

/**
 * The error-placeholder title each route renders in error mode, read from the
 * packages' own bundles.
 *
 * In error mode every read rejects, but a page reaches its error state only
 * once react-query reports the failures; a scan taken before that flush
 * records a half-rendered tree. Both sweeps assert this copy is on screen
 * before scanning these routes, so a pending page is never scanned as if it
 * were the error state.
 */
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
  // The workspace surface's admission failure: its caller-tenants and
  // permission reads both refuse in error mode, and the page renders the
  // retryable failure (with its level-one heading) rather than a spinner.
  // Asserting the copy here makes the sweep prove it scanned that state.
  '/workspace/admin/invitations': demoEnUS.workspace.accessFailed.title,
  '/workspace/admin/sso': demoEnUS.workspace.accessFailed.title,
}

/**
 * How many settle ticks a render may take before a sweep calls it hung.
 * Each tick flushes pending microtasks and one timer/animation-frame callback
 * inside act; a stable page returns after two consecutive identical ticks,
 * so the cap only bounds a page that never stops changing.
 */
const SETTLE_TICK_CAP = 50

/**
 * Lets a page settle before it is scanned: the scripted backend resolves
 * immediately, but react-query hands the page its data -- and, in error mode,
 * flushes its failure notification -- on a batched macrotask that can land
 * after a fixed one-flush wait. Scanning then would record a pending tree
 * whose headings and placeholders have not been committed yet.
 *
 * The wait is therefore driven by the render itself: each tick flushes the
 * pending work inside act and snapshots the document, and the loop returns
 * only after two consecutive ticks render the same structure. A page that
 * never stabilises within the cap fails the case with the cap named, rather
 * than scanning whatever happened to be on screen.
 */
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

/** The tenant every sweep scans under, with the alias the host has recorded
 * for it; the host holds no default roster, so a current tenant must be
 * nameable. */
export const SCAN_TENANT_ID = 'acme'
