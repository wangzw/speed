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

import { act } from '@testing-library/react'
import { afterAll, afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MockInstance } from 'vitest'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { routes } from '../routes.js'
import { renderDemoApp } from '../test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from '../test-utils/session.js'
import {
  DEFAULT_TENANT_ID,
  setCurrentTenantId,
  setTenantAliases,
} from '../chrome.js'
import { createBackend } from './backends.js'
import type { BackendMode } from './backends.js'
import { formatFinding, formatFindings, isFailing, runBestPracticePass, scanPage } from './scan.js'
import type { A11yFinding } from './scan.js'

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
 * the business routes (the home page, the account page, the object store, the
 * notification centre, the binding callback), the auth and invitation
 * landings, and the console area under CONSOLE_PREFIX, every `:param`
 * concretized to `sample` as `concretize` does.
 */
const EXPECTED_ROUTE_PATHS = [
  '/',
  '/account',
  '/account/setup',
  '/account/callback',
  '/objects',
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

const records: ScanRecord[] = []

/**
 * Lets the page's data reads settle before the scan: the scripted backend
 * resolves immediately, so a microtask and a macrotask flushed inside act are
 * enough for react-query to hand the page its data and React to re-render.
 */
async function settle(): Promise<void> {
  await act(async () => {
    await Promise.resolve()
    await new Promise((resolve) => setTimeout(resolve, 0))
    await Promise.resolve()
  })
}

/**
 * Renders one route in one state, scans it, and records the outcome. Returns
 * the outcome so a caller can assert on it.
 */
async function scanRoute(
  path: string,
  mode: BackendMode,
): Promise<readonly A11yFinding[]> {
  const unscripted: string[] = []
  bindRequestFn(createBackend(mode, (url) => unscripted.push(url)))
  window.history.replaceState({}, '', path)
  const { container } = renderDemoApp({ session: staffSession() })
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
  const result = await scanPage()
  // The sweep must not surface color-contrast: it is excluded from the gate's
  // rule set (jsdom performs no color computation, so it is indeterminate for
  // every node -- see UNSWEEPABLE_RULES in ./scan.ts). A re-added contrast run
  // would show up here as a regression rather than as the noise it once was.
  expect(
    result.incomplete.some((finding) => finding.rule === 'color-contrast'),
    `route ${path} [${mode}] swept color-contrast, which jsdom cannot decide`,
  ).toBe(false)
  for (const finding of result.incomplete) {
    console.warn(`[route-a11y] ${path} [${mode}] indeterminate: ${formatFinding(finding)}`)
  }
  records.push({ path, mode, findings: result.violations })
  return result.violations
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

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  setCurrentTenantId(DEFAULT_TENANT_ID)
  setTenantAliases(new Map())
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
    for (const finding of result.incomplete) {
      console.warn(`[route-a11y] / (anonymous) indeterminate: ${formatFinding(finding)}`)
    }
    records.push({ path: '/', mode: 'anonymous', findings: result.violations })
    const failing = result.violations.filter(isFailing)
    warnNonFailing('/', 'anonymous', result.violations.filter((f) => !isFailing(f)))
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
    records.push({ path: '/no-such-route', mode: 'data', findings: result.violations })
    const failing = result.violations.filter(isFailing)
    warnNonFailing('/no-such-route', 'data', result.violations.filter((f) => !isFailing(f)))
    if (failing.length > 0) {
      expect.fail(
        `route /no-such-route [data] has ${failing.length} serious/critical axe violation(s):\n` +
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
