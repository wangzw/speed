/**
 * The route-level accessibility gate.
 *
 * It walks the application's own route table -- the copied product's `routes`,
 * so a page added there is scanned without this file changing -- and renders
 * the assembled app at each route's path, sweeping the tenant shell and the
 * admin shell alike. Every route is swept in the three states a page reaches
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

import { screen, within } from '@testing-library/react'
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
import { adminUiResources } from '@speed/admin-ui'
import { authUiResources } from '@speed/auth-ui'
import type { AppShellShellLayout } from '@speed/layout-kit'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { renderDemoApp } from '../test-utils/render.js'
import {
  setCurrentTenant,
  setTenantName,
  setTenantNames,
} from '../chrome.js'
import { clearSelfTenants } from '../tenant-self.js'
import { ACCOUNT_SECTION_PATHS, ACCOUNT_SECTIONS } from '../paths.js'
import { PlatformPermissions } from '../platform-permissions.js'
import { createBackend } from './backends.js'
import type { BackendMode } from './backends.js'
import {
  ERROR_PLACEHOLDER_BY_PATH,
  EXPECTED_HEADING_BY_PATH,
  EXPECTED_ROUTE_PATHS,
  MODES,
  ORGANIZATION_MEMBERS_ROLE_DEEP_LINK,
  ROUTE_PATHS,
  SCAN_TENANT_ID,
  settle,
  staffSession,
  SWEEP_PATHS,
} from './route-table.js'
import { formatFinding, formatFindings, isFailing, runBestPracticePass, scanPage } from './scan.js'
import type { A11yFinding, A11yScanResult } from './scan.js'

/** One scanned route/state combination's violations, for the closing report. */
interface ScanRecord {
  readonly path: string
  /** The backend state the route was scanned in, or 'anonymous'. */
  readonly mode: BackendMode | 'anonymous'
  readonly findings: readonly A11yFinding[]
}

const records: ScanRecord[] = []

/**
 * The two rules that land in axe's indeterminate bucket for reasons this
 * harness can decide, asserted absent for every scanned state -- the sweep's
 * routes and the states only a driven case reaches alike.
 *
 * label-content-name-mismatch is deliberately absent: jsdom answers the
 * rule's canvas getContext probe with null, so axe records "error occurred"
 * and the rule is indeterminate for every node -- neither a pass nor a
 * failure is observable here (see ./scan.ts's header). The sweep keeps
 * printing its incomplete result, selector included, as a hint; the browser
 * tier (routes.browser.test.tsx) settles the rule in Chromium. Turning the
 * hint into an assertion here would gate on a verdict jsdom cannot reach.
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

/** The accessible name a named section/region landmark exposes. */
function regionAccessibleName(element: Element): string {
  const label = element.getAttribute('aria-label')
  if (label !== null) {
    return label.trim()
  }
  const ids = element.getAttribute('aria-labelledby')
  if (ids === null) {
    return ''
  }
  return ids
    .split(/\s+/)
    .map((id) => document.getElementById(id)?.textContent?.trim() ?? '')
    .join(' ')
    .trim()
}

/**
 * Fails when two region landmarks in the scanned document share one
 * accessible name. A region's name is what a screen reader announces in the
 * landmark list, so two regions both called "Two-step verification" are
 * indistinguishable there; axe's landmark-unique reports the same defect,
 * but only in the advisory best-practice pass, so this assertion holds every
 * swept route and state to it instead of trusting a warning nobody reads.
 */
function expectUniqueRegionNames(label: string): void {
  const seen = new Map<string, string>()
  const duplicates: string[] = []
  for (const element of document.querySelectorAll(
    'section[aria-label], section[aria-labelledby], [role="region"][aria-label], [role="region"][aria-labelledby]',
  )) {
    const name = regionAccessibleName(element)
    if (name === '') {
      continue
    }
    const first = seen.get(name)
    const where = `<${element.tagName.toLowerCase()}>`
    if (first !== undefined) {
      duplicates.push(`"${name}" on ${where} and ${first}`)
    } else {
      seen.set(name, where)
    }
  }
  expect(
    duplicates,
    `route ${label} has region landmarks sharing one accessible name`,
  ).toEqual([])
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
  expectUniqueRegionNames(label)
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
  // The permission attach is part of the production assembly: the organization
  // admin pages admit only against the tenant-tagged set it publishes, so the
  // sweep mounts it the way a real page load does.
  const session = staffSession()
  const { container } = renderDemoApp({
    session,
    shellLayout,
    wrap: (app) => (
      <PlatformPermissions session={session}>{app}</PlatformPermissions>
    ),
  })
  // A route that rendered no markup at all is a broken sweep, not an
  // accessible page: fail rather than scan an empty document.
  expect(container.innerHTML, `route ${path} [${mode}] rendered no markup`).not.toBe('')
  const errorPlaceholder =
    mode === 'error' ? ERROR_PLACEHOLDER_BY_PATH[path] : undefined
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
  if (errorPlaceholder !== undefined) {
    // The route's own error copy must be on screen before the scan. A page
    // reaches its error state asynchronously: settle() can return on a wait
    // view that is momentarily stable while a later read is still failing
    // (the organization surface's admission reads settle in a later batch).
    // Wait for the copy itself, then settle again so the scan reads the
    // stable failure rather than a tree still being replaced.
    await screen.findByText(errorPlaceholder)
    await settle()
  } else {
    // A organization route in its data or empty state must render its own page
    // title. Without this, a silent redirect to the tenant home would scan a
    // clean home page and pass the sweep while the route itself never
    // rendered.
    const expectedHeading = EXPECTED_HEADING_BY_PATH[path]
    if (expectedHeading !== undefined) {
      expect(
        await screen.findByRole('heading', {
          level: 1,
          name: expectedHeading,
        }),
        `route ${path} [${mode}] did not render its page title heading`,
      ).toBeInTheDocument()
    }
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
 * admin; only the missing-key channel is captured, and afterEach fails the
 * case when it caught anything.
 */
const missingKeys: string[] = []
let warnSpy: MockInstance

beforeEach(() => {
  window.history.replaceState({}, '', '/')
  // The host's cells are module-level and survive a case: clear the caller
  // tenant rows and re-record the scan tenant's kind before every route, so a
  // route's state does not depend on which mode or route ran before it. The
  // organization admission reads that recorded kind, and the empty backend's
  // caller-tenant answer lists no rows.
  clearSelfTenants()
  setCurrentTenant(SCAN_TENANT_ID)
  setTenantNames(new Map())
  setTenantName(SCAN_TENANT_ID, 'business')
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

  it('sweeps the members page role-filter deep link beside the route table', () => {
    // The deep link is the only address at which the role filter's selected
    // state and its clear control render, so a sweep that dropped it would
    // scan the page without the filter's own accessibility surface.
    expect(SWEEP_PATHS).toContain(ORGANIZATION_MEMBERS_ROLE_DEEP_LINK)
    // The page title and its organization menu entry name the same page;
    // both read their wording from the package bundles, so this guards
    // against the two drifting onto different labels.
    const bundles = adminUiResources['en-US'] as unknown as {
      readonly invitations: { readonly title: string }
      readonly organization: { readonly nav: { readonly invitations: string } }
    }
    expect(bundles.invitations.title).toBe(bundles.organization.nav.invitations)
  })

  it('names the organization roles page and its menu entry with the same label', () => {
    // The roles page titles itself from `organization.nav.roles`, the same
    // key its organization menu entry reads, rather than the platform-only
    // `permissions.title`. The sweep's expected heading comes from the same
    // bundle, so this case guards the route-table entry against drifting
    // back onto the platform label.
    const bundles = adminUiResources['en-US'] as unknown as {
      readonly organization: { readonly nav: { readonly roles: string } }
    }
    expect(EXPECTED_HEADING_BY_PATH['/organization/admin/roles']).toBe(
      bundles.organization.nav.roles,
    )
  })

  for (const path of SWEEP_PATHS) {
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

  // The account settings is one page per pane, so each pane's route is
  // scanned in the tenant frame's sidebar chrome. One case per pane keeps
  // each scan on a fresh document -- two compositions in one case would put
  // two main landmarks on the page -- and the panes are the only routes whose
  // content the default topbar sweep never composes with that chrome.
  for (const section of ACCOUNT_SECTIONS) {
    it(`scans the account settings pane ${section} in the sidebar shell form`, async () => {
      await scanSidebarRoute(ACCOUNT_SECTION_PATHS[section])
    })
  }

  it('scans an admin route in the sidebar shell form', async () => {
    // The admin is the second shell; it takes the same layout through a
    // different frame, and its grouped navigation is the shape the tenant
    // frame does not render.
    await scanSidebarRoute('/admin/tenants')
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
      state: 'pending',
      invitation: {
        tenant_display_name: 'Acme',
        invited_by_name: 'Test User',
        email: 'i***@example.com',
        expires_at: '2027-01-01T00:00:00Z',
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
      if (url === `${invitationPath}/eligibility`) {
        return Promise.resolve({ state: 'pending', email_proved: true } as T)
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
      demoEnUS.home.notice.membershipTitle,
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
      color_mode: '',
      locale: 'en-US',
      time_zone: 'UTC',
      identifiers: [],
      links: [],
      verification: { state: 'pending', email: '', since: null },
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
