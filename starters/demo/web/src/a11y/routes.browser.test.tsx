/**
 * The route-level Label in Name (WCAG 2.5.3) gate in a real browser.
 *
 * This is the browser leg of the jsdom route sweep (routes.test.tsx). It walks
 * the same route/state matrix (./route-table.ts) and renders the assembled app
 * in Chromium, because axe's `label-content-name-mismatch` reads a control's
 * visible text through the canvas text-measurement API: jsdom answers
 * `getContext('2d')` with null, so the rule is indeterminate for every node
 * there and the jsdom leg can only report it as a hint. Here the canvas is
 * real, the rule is determinate, and a mismatch between a control's visible
 * text and its accessible name fails the case.
 *
 * Scope. The sweep scans the whole rendered document -- chrome and page
 * together -- so the shell's own controls are settled where they compose with
 * real content: both shell layouts (the default top-bar form and the sidebar
 * form), the two frames' shared header controls (colour-mode toggle, account
 * menu), and the notification bell. The bell's three states
 * (one unread, none, above the badge cap) are driven explicitly, because a
 * single data profile reaches only the first two.
 *
 * Verdict. In Chromium an incomplete result means the rule could not decide
 * the node at all, so it fails the case rather than passing by
 * indeterminacy; a violation fails it with the node selectors named.
 */

import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterAll, afterEach, beforeEach, describe, expect, it } from 'vitest'
import axe from 'axe-core'
import { page } from 'vitest/browser'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { getNotificationInboxGetUnreadCountQueryKey } from '@speed/api-sdk'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { tenantQueryKey } from '@speed/auth-core'
import { setCurrentTenantId, setTenantAliases } from '../chrome.js'
import { renderDemoApp } from '../test-utils/render.js'
import { createBackend } from './backends.js'
import {
  ERROR_PLACEHOLDER_BY_PATH,
  MODES,
  ROUTE_PATHS,
  SCAN_TENANT_ID,
  settle,
  staffSession,
} from './route-table.js'

/** The rule this tier settles; the full page set belongs to the jsdom gate. */
const RULE = 'label-content-name-mismatch'

/** The unread-count read the bell renders and the driven states override. */
const UNREAD_PATH = '/api/v1/notifications/messages/unread-count'

/** The largest count the badge prints before switching to "99+". */
const BADGE_MAX = 99

/** One rule run reduced to the two buckets a verdict is decided on, plus how
 * many nodes the rule actually evaluated (a clean run over a page where the
 * rule is inapplicable proves nothing). */
async function labelInName(
  scope: Element,
): Promise<{ readonly flagged: readonly axe.Result[]; readonly evaluated: number }> {
  const result = await axe.run(scope, {
    runOnly: { type: 'rule', values: [RULE] },
  })
  return {
    flagged: [...result.violations, ...result.incomplete],
    evaluated: result.passes.reduce((total, rule) => total + rule.nodes.length, 0),
  }
}

/** The nodes the rule evaluated across the whole sweep, asserted non-zero at
 * the end so a run where the rule never applied cannot read as a pass. */
let evaluatedNodes = 0

/** The selectors of every node a rule flagged, for a failing report. */
function describeFindings(rules: readonly axe.Result[]): string {
  return rules
    .map((rule) => {
      const targets = rule.nodes.map((node) => node.target.join(' ')).join(', ')
      return `  - ${rule.id}: ${rule.help} (${targets || 'no target'})`
    })
    .join('\n')
}

/**
 * Scans the current document and fails the case when the rule flags any node.
 * A clean run is the only pass: there is no incomplete bucket in a browser.
 */
async function expectLabelInName(label: string): Promise<void> {
  const { flagged, evaluated } = await labelInName(document.body)
  evaluatedNodes += evaluated
  if (flagged.length > 0) {
    expect.fail(
      `${label} has ${flagged.length} Label in Name finding(s):\n` +
        describeFindings(flagged),
    )
  }
}

/**
 * Renders one route in one state and settles it before scanning -- the same
 * pending-tree guard the jsdom sweep applies (the error-mode placeholder must
 * be on screen, and a read the scripted backend does not answer fails the
 * route rather than being scanned as a state the real backend never
 * produces).
 */
async function scanRoute(
  path: string,
  mode: (typeof MODES)[number],
): Promise<void> {
  const unscripted: string[] = []
  bindRequestFn(createBackend(mode, (url) => unscripted.push(url)))
  window.history.replaceState({}, '', path)
  const { container } = renderDemoApp({ session: staffSession() })
  expect(container.innerHTML, `route ${path} [${mode}] rendered no markup`).not.toBe('')
  await settle()
  expect(
    unscripted,
    `route ${path} [${mode}] issued read(s) the scripted backend does not answer`,
  ).toEqual([])
  const errorPlaceholder =
    mode === 'error' ? ERROR_PLACEHOLDER_BY_PATH[path] : undefined
  if (errorPlaceholder !== undefined) {
    expect(
      screen.queryByText(errorPlaceholder),
      `route ${path} [${mode}] has not reached its error state`,
    ).not.toBeNull()
  }
  await expectLabelInName(`route ${path} [${mode}]`)
}

beforeEach(async () => {
  // The desktop branch is the one the jsdom stub forces (matchMedia reports
  // matches for every query); the real browser needs the same width for the
  // two shells to compose as they do in the jsdom sweep.
  await page.viewport(1280, 900)
  window.history.replaceState({}, '', '/')
  setCurrentTenantId(SCAN_TENANT_ID)
  setTenantAliases(new Map([[SCAN_TENANT_ID, SCAN_TENANT_ID]]))
})

afterEach(() => {
  setCurrentTenantId(null)
  setTenantAliases(new Map())
})

describe('route-level Label in Name in Chromium', () => {
  for (const path of ROUTE_PATHS) {
    for (const mode of MODES) {
      it(`${mode}: ${path}`, async () => {
        await scanRoute(path, mode)
      })
    }
  }

  it('scans a tenant route in the sidebar shell form', async () => {
    const unscripted: string[] = []
    bindRequestFn(createBackend('data', (url) => unscripted.push(url)))
    window.history.replaceState({}, '', '/account')
    const { container } = renderDemoApp({
      session: staffSession(),
      shellLayout: 'sidebar',
    })
    expect(container.innerHTML).not.toBe('')
    await settle()
    expect(unscripted).toEqual([])
    await expectLabelInName('route /account [data, sidebar]')
  })

  it('scans a console route in the sidebar shell form', async () => {
    const unscripted: string[] = []
    bindRequestFn(createBackend('data', (url) => unscripted.push(url)))
    window.history.replaceState({}, '', '/console/tenants')
    const { container } = renderDemoApp({
      session: staffSession(),
      shellLayout: 'sidebar',
    })
    expect(container.innerHTML).not.toBe('')
    await settle()
    expect(unscripted).toEqual([])
    await expectLabelInName('route /console/tenants [data, sidebar]')
  })

  it('scans the anonymous sign-in surface', async () => {
    const unscripted: string[] = []
    bindRequestFn(createBackend('data', (url) => unscripted.push(url)))
    window.history.replaceState({}, '', '/')
    const { container } = renderDemoApp()
    expect(container.innerHTML).not.toBe('')
    await settle()
    expect(unscripted).toEqual([])
    await expectLabelInName('route / [anonymous]')
  })

  it('scans the account menu opened over the page', async () => {
    bindRequestFn(createBackend('data'))
    window.history.replaceState({}, '', '/')
    renderDemoApp({ session: staffSession() })
    await settle()
    const user = userEvent.setup()
    await user.click(screen.getByTestId('user-menu-trigger'))
    const menu = await screen.findByRole('menu')
    // The menu items are the controls whose visible text must be inside their
    // accessible name; the page behind the open menu is aria-hidden, so the
    // menu itself is the meaningful scope.
    const { flagged, evaluated } = await labelInName(menu)
    evaluatedNodes += evaluated
    if (flagged.length > 0) {
      expect.fail(
        `the account menu has ${flagged.length} Label in Name finding(s):\n` +
          describeFindings(flagged),
      )
    }
    expect(within(menu).getAllByRole('menuitem').length).toBeGreaterThan(0)
  })

  /**
   * The bell's three states in the assembled shell. The count read is gated
   * and released explicitly, so the scan can only run once the answer has
   * landed -- at zero unread the placeholder and the settled badge carry the
   * same accessible name, and a name-based wait would scan the placeholder.
   */
  for (const unread of [1, 0, BADGE_MAX + 1] as const) {
    it(`scans the shell bell at ${unread} unread`, async () => {
      let release!: () => void
      const gate = new Promise<void>((resolve) => {
        release = resolve
      })
      const base = createBackend('data')
      const fn = (<T,>(url: string, options?: RequestOptions) => {
        if (url === UNREAD_PATH) {
          return gate.then(
            () => ({ unread }) as T,
          )
        }
        return base<T>(url, options)
      }) as RequestFn
      bindRequestFn(fn)
      window.history.replaceState({}, '', '/')
      const { queryClient } = renderDemoApp({ session: staffSession() })
      await settle()
      const unreadKey = tenantQueryKey(
        SCAN_TENANT_ID,
        ...getNotificationInboxGetUnreadCountQueryKey(),
      )
      expect(
        queryClient.getQueryState(unreadKey)?.status,
        'the unread read must be in flight before it is released',
      ).not.toBe('success')
      release()
      await waitFor(() => {
        expect(queryClient.getQueryState(unreadKey)?.status).toBe('success')
      })
      await settle()
      // The scan waits on the read itself, never on a name: at zero unread the
      // accessible name is identical in the placeholder and settled states.
      expect(queryClient.getQueryState(unreadKey)?.status).toBe('success')
      await expectLabelInName(`shell bell [${unread} unread]`)
    })
  }

  afterAll(() => {
    // A sweep where the rule never evaluated a node would be green whatever
    // the pages rendered; the cases above must have judged real controls.
    expect(
      evaluatedNodes,
      'the Label in Name rule evaluated no node across the whole sweep',
    ).toBeGreaterThan(0)
  })
})
