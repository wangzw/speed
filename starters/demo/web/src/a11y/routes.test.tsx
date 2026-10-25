/**
 * The route-level accessibility gate.
 *
 * It walks the application's own route table -- the copied product's `routes`,
 * so a page added there is scanned without this file changing -- and renders
 * the assembled app at each route's path, sweeping the tenant shell and the
 * console shell alike. Every route is swept in the three states a page reaches
 * (the scripted backends in ./backends.ts): populated, empty and error. Each
 * render is scanned by ./scan.ts and judged by impact:
 *
 *   - a `serious` or `critical` axe violation fails the gate, naming the route
 *     and state and listing the findings by rule id, impact and node count;
 *   - a `moderate`, `minor` or no-impact violation is warned and registered
 *     (never silently dropped), and the whole run prints a per-route report.
 *
 * The sweep also fails on an i18n missing-key warning. The harness registers
 * every namespace the app's views render from (src/test-utils/render.tsx), and
 * a page whose namespace registration is dropped still renders -- the key
 * itself takes the text's place -- so without this guard a lost registration
 * is only stderr noise and the route stays "accessible". A warning the i18n
 * package prefixes `[speed-i18n] missing translation key` fails the case that
 * emitted it, naming the key and namespace.
 *
 * This is the page tier above the packages' component scans: the units here
 * are whole routes -- chrome and page together -- so a landmark, heading or
 * naming defect that only appears when the two compose is caught where it
 * lives. axe covers only the mechanically decidable part of WCAG (see
 * ./scan.ts and the skill's Accessibility section); this gate proves no
 * regression, never that a page is accessible.
 */

import { act, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterAll, afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MockInstance } from 'vitest'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import type {
  AuthnInviteAccept,
  AuthnInviteView,
  AuthnProfileProfile,
} from '@speed/api-sdk'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { accountUiResources } from '@speed/account-ui'
import { authUiResources } from '@speed/auth-ui'
import { consoleUiResources } from '@speed/console-ui'
import type { AppShellShellLayout } from '@speed/layout-kit'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { routes } from '../routes.js'
import { renderDemoApp } from '../test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from '../test-utils/session.js'
import {
  setCurrentTenantId,
  setTenantAliases,
} from '../chrome.js'
import { createBackend } from './backends.js'
import type { BackendMode } from './backends.js'
import { formatFinding, formatFindings, isFailing, runBestPracticePass, scanPage } from './scan.js'
import type { A11yFinding, A11yScanResult } from './scan.js'

/**
 * Every platform permission code a console page's route guard checks, so the
 * staff session passes every guard and each console page renders inside the
 * frame rather than the no-permission surface. The set is drawn from the
 * console-ui declarations' own permission codes.
 */
const CONSOLE_PERMISSION_CODES = [
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

/** A platform-staff session holding every console permission code. */
function staffSession() {
  return createTestSession(
    authenticatedSnapshot({ system: [...CONSOLE_PERMISSION_CODES] }),
  )
}

/** The three states each route is swept in. */
const MODES: readonly BackendMode[] = ['data', 'empty', 'error']

/**
 * Replaces a declaration's `:name` parameter with a concrete segment, so a
 * path pattern becomes a URL the router matches and the backend answers.
 */
function concretize(pattern: string): string {
  return pattern.replace(/:[A-Za-z_][A-Za-z0-9_]*/g, 'sample')
}

/** The distinct URL paths the app's route table declares. */
const ROUTE_PATHS = [...new Set(routes.map((route) => concretize(route.path)))]

/**
 * The concrete paths the composition ships, pinned so a route that drops out
 * of the table cannot leave the sweep silently -- the coverage guard below
 * asserts each of these is still enumerated. Drawn from the app's route table:
 * the business routes (the home page, the account page that also carries the
 * email verification, the setup landing, the notification centre, the
 * binding callback), the auth and invitation landings, and the
 * console area under CONSOLE_PREFIX, every `:param` concretized to `sample` as
 * `concretize` does.
 */
const EXPECTED_ROUTE_PATHS = [
  '/',
  '/account',
  '/account/setup',
  '/account/callback',
  '/notifications',
  '/sign-in/callback',
  '/invitations/sample',
  '/console',
  '/console/tenants',
  '/console/tenants/sample',
  '/console/accounts',
  '/console/sessions',
  '/console/impersonations',
  '/console/permissions',
  '/console/invitations',
  '/console/sso',
  '/console/settings',
  '/console/sms',
  '/console/audit',
  '/console/audit/sample',
] as const

/** One scanned route/state combination's violations, for the closing report. */
interface ScanRecord {
  readonly path: string
  /** The backend state the route was scanned in, or 'anonymous'. */
  readonly mode: BackendMode | 'anonymous'
  readonly findings: readonly A11yFinding[]
}

/**
 * The error-placeholder title each route renders in error mode, read from the
 * packages' own bundles.
 *
 * In error mode every read rejects, but a page reaches its error state only
 * once react-query reports the failures; a scan taken before that flush
 * records a half-rendered tree. The sweep asserts this copy is on screen
 * before scanning these routes, so a pending page is never scanned as if it
 * were the error state.
 */
const ERROR_PLACEHOLDER_BY_PATH: Readonly<Record<string, string>> = {
  '/account': (
    accountUiResources['en-US'] as unknown as {
      readonly profile: { readonly error: { readonly title: string } }
    }
  ).profile.error.title,
  '/console/audit': (
    consoleUiResources['en-US'] as unknown as {
      readonly audit: { readonly error: { readonly title: string } }
    }
  ).audit.error.title,
}

const records: ScanRecord[] = []

/**
 * How many settle ticks a render may take before the sweep calls it hung.
 * Each tick flushes pending microtasks and one timer/animation-frame callback
 * inside act; a stable page returns after two consecutive identical ticks,
 * so the cap only bounds a page that never stops changing.
 */
const SETTLE_TICK_CAP = 50

/**
 * Lets the page settle before the scan: the scripted backend resolves
 * immediately, but react-query hands the page its data -- and, in error
 * mode, flushes its failure notification -- on a batched macrotask that can
 * land after a fixed one-flush wait. Scanning then would record a pending
 * tree whose headings and placeholders have not been committed yet, and
 * axe's cached heading table can no longer be matched against the subtrees
 * it is about to be replaced with.
 *
 * The wait is therefore driven by the render itself: each tick flushes the
 * pending work inside act and snapshots the document, and the loop returns
 * only after two consecutive ticks render the same structure. A page that
 * never stabilises within SETTLE_TICK_CAP fails the case with the cap named,
 * rather than scanning whatever happened to be on screen.
 */
async function settle(): Promise<void> {
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

/**
 * The two rules that land in axe's indeterminate bucket for reasons this
 * harness can decide, asserted absent for every scanned state -- the sweep's
 * routes and the states only a driven case reaches alike.
 *
 * label-content-name-mismatch is deliberately absent: jsdom answers the
 * rule's canvas getContext probe with null, so axe records "error occurred"
 * and the rule is indeterminate for every node -- neither a pass nor a
 * failure is observable here (see ./scan.ts's header). The browser tier
 * settles it in Chromium; this gate keeps reporting its incomplete result
 * rather than turning it into an assertion it cannot decide.
 */
function expectNoDecidableIncomplete(
  result: A11yScanResult,
  label: string,
): void {
  // The sweep must not surface color-contrast: it is excluded from the gate's
  // rule set (jsdom performs no color computation, so it is indeterminate for
  // every node -- see UNSWEEPABLE_RULES in ./scan.ts). A re-added contrast run
  // would show up here as a regression rather than as the noise it once was.
  expect(
    result.incomplete.some((finding) => finding.rule === 'color-contrast'),
    `${label} swept color-contrast, which jsdom cannot decide`,
  ).toBe(false)
  // aria-prohibited-attr lands in axe's indeterminate bucket only because jsdom
  // does not compute an element's role; the question itself is decidable on a
  // static tree. Asserting its absence turns the reported warning into a gate:
  // the one node it named was a generic span carrying aria-label.
  expect(
    result.incomplete.some((finding) => finding.rule === 'aria-prohibited-attr'),
    `${label} put an ARIA attribute on an element that does not permit it`,
  ).toBe(false)
}

/**
 * The one exit every scanned route and driven case goes through: asserts the
 * decidable-indeterminate rules absent, prints every remaining indeterminate
 * result, and records the violation set for the closing report. No scan may
 * skip it -- a case that only looked at violations would drop a real
 * incomplete result silently.
 *
 * `label` names the route/state in the findings, e.g. `/account [error]` or
 * `/ [data, membership confirmation]`; `path` and `mode` are the record's
 * own coordinates for the report.
 */
function recordScan(
  label: string,
  path: string,
  mode: BackendMode | 'anonymous',
  result: A11yScanResult,
): readonly A11yFinding[] {
  expectNoDecidableIncomplete(result, `route ${label}`)
  for (const finding of result.incomplete) {
    console.warn(`[route-a11y] ${label} indeterminate: ${formatFinding(finding)}`)
  }
  records.push({ path, mode, findings: result.violations })
  return result.violations
}

/**
 * Renders one route in one state, scans it, and records the outcome. Returns
 * the outcome so a caller can assert on it.
 */
async function scanRoute(
  path: string,
  mode: BackendMode,
  shellLayout?: AppShellShellLayout,
): Promise<readonly A11yFinding[]> {
  const unscripted: string[] = []
  bindRequestFn(createBackend(mode, (url) => unscripted.push(url)))
  window.history.replaceState({}, '', path)
  const { container } = renderDemoApp({ session: staffSession(), shellLayout })
  // A route that rendered no markup at all is a broken sweep, not an
  // accessible page: fail rather than scan an empty document.
  expect(container.innerHTML, `route ${path} [${mode}] rendered no markup`).not.toBe('')
  await settle()
  // A read the scripted backend does not answer resolves `undefined`;
  // react-query only logs "Query data cannot be undefined" and the page
  // renders, and the sweep scans, a state no real backend produces. Fail the
  // route here so a table entry that drifts from the generated paths cannot
  // silently stop covering a read again.
  expect(
    unscripted,
    `route ${path} [${mode}] issued read(s) the scripted backend does not answer`,
  ).toEqual([])
  const errorPlaceholder =
    mode === 'error' ? ERROR_PLACEHOLDER_BY_PATH[path] : undefined
  if (errorPlaceholder !== undefined) {
    // The route's own error copy must be on screen before the scan: settle()
    // returns once the tree stops changing, and a tree that never reached its
    // error state would otherwise be scanned as if it were settled.
    expect(
      screen.queryByText(errorPlaceholder),
      `route ${path} [${mode}] has not reached its error state: ` +
        'settle() returned while the read was still pending',
    ).not.toBeNull()
  }
  const result = await scanPage()
  if (errorPlaceholder !== undefined) {
    // A heading table axe can no longer resolve means the scan read a tree
    // that was still being replaced under it (the failure notification landed
    // mid-scan), not a real heading-order defect.
    expect(
      result.incomplete.some((finding) => finding.rule === 'heading-order'),
      `route ${path} [${mode}] scanned a heading table axe could not resolve: ` +
        'the tree was still changing when the scan ran',
    ).toBe(false)
  }
  return recordScan(`${path} [${mode}]`, path, mode, result)
}

/** Warns and registers every non-failing (moderate/minor/no-impact) finding. */
function warnNonFailing(
  path: string,
  mode: BackendMode | 'anonymous',
  findings: readonly A11yFinding[],
): void {
  for (const finding of findings) {
    console.warn(`[route-a11y] ${path} [${mode}] advisory: ${formatFinding(finding)}`)
  }
}

/** The prefix @speed/i18n's default missing-key handler prints. */
const MISSING_KEY_WARNING = '[speed-i18n] missing translation key'

/**
 * The missing-key warnings the current case emitted. Every other warning --
 * the sweep's own advisory findings included -- passes through to the real
 * console; only the missing-key channel is captured, and afterEach fails the
 * case when it caught anything.
 */
const missingKeys: string[] = []
let warnSpy: MockInstance

/** The tenant every route is scanned under, with the alias the host has
 * recorded for it; the host holds no default roster, so a current tenant must
 * be nameable. */
const TENANT_ID = 'acme'

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  setCurrentTenantId(TENANT_ID)
  setTenantAliases(new Map([[TENANT_ID, TENANT_ID]]))
  missingKeys.length = 0
  const original = console.warn
  warnSpy = vi.spyOn(console, 'warn').mockImplementation((message, ...rest) => {
    const text = String(message)
    if (text.startsWith(MISSING_KEY_WARNING)) {
      missingKeys.push(text)
      return
    }
    original(message, ...rest)
  })
})

afterEach(() => {
  warnSpy.mockRestore()
  expect(
    missingKeys,
    'a swept route rendered a missing translation key: the harness must ' +
      'register every namespace its views read (src/test-utils/render.tsx)',
  ).toEqual([])
})

describe('route-level accessibility (WCAG 2.1 AA)', () => {
  it('enumerates the app route table', () => {
    // The sweep's coverage is the route table; a route that drops out of it is
    // scanned in no state and lets a defect live there silently. Pin every
    // concrete path the composition ships so any one disappearing fails here.
    // (The check requires the pinned paths be present, not that they be the
    // only ones: a newly added route is scanned without the list changing.)
    expect(ROUTE_PATHS).toEqual(expect.arrayContaining([...EXPECTED_ROUTE_PATHS]))
  })

  for (const path of ROUTE_PATHS) {
    for (const mode of MODES) {
      it(`${mode}: ${path}`, async () => {
        const findings = await scanRoute(path, mode)
        const failing = findings.filter(isFailing)
        warnNonFailing(path, mode, findings.filter((f) => !isFailing(f)))
        if (failing.length > 0) {
          expect.fail(
            `route ${path} [${mode}] has ${failing.length} serious/critical axe violation(s):\n` +
              formatFindings(failing),
          )
        }
        // The best-practice pass runs once per route (data mode only): it is
        // advisory -- reported, never failed -- so it does not gate.
        if (mode === 'data') {
          const advisory = await runBestPracticePass()
          for (const finding of advisory.violations) {
            console.warn(
              `[route-a11y] ${path} [best-practice] advisory: ${formatFinding(finding)}`,
            )
          }
        }
      })
    }
  }

  /**
   * Scans one route in the sidebar shell form and fails on a serious or
   * critical finding, exactly as the topbar sweep does. The sidebar form
   * has its own chrome (a complementary column, no app bar at md+) that
   * the default sweep never composes with a routed page.
   */
  async function scanSidebarRoute(path: string): Promise<void> {
    const findings = await scanRoute(path, 'data', 'sidebar')
    const failing = findings.filter(isFailing)
    warnNonFailing(path, 'data', findings.filter((f) => !isFailing(f)))
    if (failing.length > 0) {
      expect.fail(
        `route ${path} [data, sidebar] has ${failing.length} serious/critical axe violation(s):\n` +
          formatFindings(failing),
      )
    }
  }

  it('scans a tenant route in the sidebar shell form', async () => {
    // The account page carries the appearance section and sections of its
    // own, so the tenant frame's sidebar chrome is scanned against real
    // content rather than the empty frame.
    await scanSidebarRoute('/account')
  })

  it('scans a console route in the sidebar shell form', async () => {
    // The console is the second shell; it takes the same layout through a
    // different frame, and its grouped navigation is the shape the tenant
    // frame does not render.
    await scanSidebarRoute('/console/tenants')
  })

  it('scans the anonymous sign-in surface at the tenant root', async () => {
    const unscripted: string[] = []
    bindRequestFn(createBackend('data', (url) => unscripted.push(url)))
    window.history.replaceState({}, '', '/')
    const { container } = renderDemoApp()
    expect(container.innerHTML).not.toBe('')
    await settle()
    expect(
      unscripted,
      'the anonymous sign-in surface issued read(s) the scripted backend does not answer',
    ).toEqual([])
    const result = await scanPage()
    const findings = recordScan('/ (anonymous)', '/', 'anonymous', result)
    const failing = findings.filter(isFailing)
    warnNonFailing('/', 'anonymous', findings.filter((f) => !isFailing(f)))
    if (failing.length > 0) {
      expect.fail(
        `route / [anonymous] has ${failing.length} serious/critical axe violation(s):\n` +
          formatFindings(failing),
      )
    }
  })

  it('scans the not-found fallback inside the tenant shell', async () => {
    const unscripted: string[] = []
    bindRequestFn(createBackend('data', (url) => unscripted.push(url)))
    window.history.replaceState({}, '', '/no-such-route')
    const { container } = renderDemoApp({ session: staffSession() })
    expect(container.innerHTML).not.toBe('')
    await settle()
    expect(
      unscripted,
      'the not-found fallback issued read(s) the scripted backend does not answer',
    ).toEqual([])
    const result = await scanPage()
    const findings = recordScan(
      '/no-such-route [data]',
      '/no-such-route',
      'data',
      result,
    )
    const failing = findings.filter(isFailing)
    warnNonFailing('/no-such-route', 'data', findings.filter((f) => !isFailing(f)))
    if (failing.length > 0) {
      expect.fail(
        `route /no-such-route [data] has ${failing.length} serious/critical axe violation(s):\n` +
          formatFindings(failing),
      )
    }
  })

  it('scans the membership confirmation after an invitation is accepted', async () => {
    // The confirmation only exists after the landing's accept action settles
    // and the app navigates home, so the per-route sweep -- which renders each
    // route as it loads -- never reaches it. This case drives the click and
    // scans the tenant home with the confirmation on it.
    const authEn = authUiResources['en-US'] as unknown as {
      readonly acceptInvitation: { readonly accept: string }
    }
    const view: AuthnInviteView = {
      invitation: {
        id: 'invite-1',
        tenant: 'acme',
        email: 'invitee@example.com',
        invited_by: 'account-1',
        status: 'pending',
        created_at: '2026-01-01T00:00:00Z',
        expires_at: '2027-01-01T00:00:00Z',
        accepted_at: '0001-01-01T00:00:00Z',
      },
    }
    const accepted: AuthnInviteAccept = {
      account: 'account-1',
      tenant: 'acme',
      active: true,
      source: 'invite',
      sso_exempt: false,
    }
    const invitationPath = '/api/v1/auth/invitations/sample'
    const unscripted: string[] = []
    const base = createBackend('data', (url) => unscripted.push(url))
    const fn: RequestFn = <T,>(url: string, options?: RequestOptions) => {
      if (url === invitationPath) {
        return Promise.resolve(
          ((options?.method ?? 'GET') === 'POST' ? accepted : view) as T,
        )
      }
      return base<T>(url, options)
    }
    bindRequestFn(fn)
    window.history.replaceState({}, '', '/invitations/sample')
    const user = userEvent.setup()
    renderDemoApp({ session: staffSession() })
    await settle()
    await user.click(
      await screen.findByRole('button', { name: authEn.acceptInvitation.accept }),
    )
    // The app lands on the tenant home with the confirmation (the visible
    // block inside the shell's main landmark; the host's live region repeats
    // the sentence outside it); the scan below is only meaningful once that
    // state is really on screen.
    await within(screen.getByRole('main')).findByText(
      demoEnUS.home.membershipNotice.title,
    )
    // The home page mounts its own reads as it lands; wait for the tree to
    // stop changing so the scan reads the settled post-accept state rather
    // than a heading table mid-replacement.
    await settle()
    const result = await scanPage()
    // This is the only case that reaches the post-accept home state, so it
    // goes through the sweep's own exit: the decidable-indeterminate gate is
    // applied and any remaining incomplete result is printed, never dropped.
    const findings = recordScan(
      '/ [data, membership confirmation]',
      '/',
      'data',
      result,
    )
    const failing = findings.filter(isFailing)
    warnNonFailing('/', 'data', findings.filter((f) => !isFailing(f)))
    if (failing.length > 0) {
      expect.fail(
        `route / [data, membership confirmation] has ${failing.length} serious/critical axe violation(s):\n` +
          formatFindings(failing),
      )
    }
    expect(
      unscripted,
      'the accepted-invitation landing and its home page issued read(s) the scripted backend does not answer',
    ).toEqual([])
  })

  it('scans the pending verification notice when the account has no target address', async () => {
    // The sweep's data profile carries a pending target, and its empty
    // profile is `none`; neither state renders the add-email variant of the
    // notice. This case scripts the third state -- pending with no address,
    // the legal state after the last proved address is removed -- so the
    // notice and the add form on the account page are scanned where they
    // appear.
    const noTarget: AuthnProfileProfile = {
      account_id: 'account-1',
      kind: 'user',
      display_name: 'Test User',
      mfa_required: false,
      password_set: true,
      created_at: '2026-01-01T00:00:00Z',
      attributes: {},
      locale: 'en-US',
      time_zone: 'UTC',
      identifiers: [],
      links: [],
      verification: { state: 'pending', email: '', since: '' },
    }
    const unscripted: string[] = []
    const data = createBackend('data', (url) => unscripted.push(url))
    bindRequestFn(((url: string, init?: RequestOptions) =>
      url === '/api/v1/auth/me'
        ? Promise.resolve(noTarget)
        : data(url, init)) as RequestFn)
    window.history.replaceState({}, '', '/')
    const { container } = renderDemoApp({ session: staffSession() })
    expect(container.innerHTML).not.toBe('')
    await settle()
    expect(
      screen.getByText(demoEnUS.verificationBanner.pendingNoEmail),
    ).toBeInTheDocument()
    expect(
      unscripted,
      'the pending-verification notice issued read(s) the scripted backend does not answer',
    ).toEqual([])
    const result = await scanPage()
    const findings = recordScan(
      '/ [pending-no-target]',
      '/',
      'data',
      result,
    )
    const failing = findings.filter(isFailing)
    warnNonFailing('/', 'data', findings.filter((f) => !isFailing(f)))
    if (failing.length > 0) {
      expect.fail(
        `route / [pending-no-target] has ${failing.length} serious/critical axe violation(s):\n` +
          formatFindings(failing),
      )
    }
  })

  afterAll(() => {
    // The closing report: every scanned route/state and its violations by
    // rule id, impact and node count. Printed so a warning is visible in the
    // suite output, never only asserted away.
    const lines = [
      `[route-a11y] scanned ${records.length} route/state combination(s)`,
    ]
    let total = 0
    for (const record of records) {
      total += record.findings.length
      if (record.findings.length === 0) {
        continue
      }
      lines.push(`${record.path} [${record.mode}]:`)
      for (const finding of record.findings) {
        lines.push(formatFinding(finding))
      }
    }
    lines.push(
      total === 0
        ? '[route-a11y] no violations (the WCAG 2.1 AA gate holds)'
        : `[route-a11y] ${total} violation(s) across the swept routes`,
    )
    console.log(lines.join('\n'))
  })
})
