import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterAll, afterEach, beforeEach, describe, expect, it } from 'vitest'
import axe from 'axe-core'
import { page } from 'vitest/browser'
import type { RequestFn, RequestOptions } from '@speed/api-client'
import { getNotificationInboxGetUnreadCountQueryKey } from '@speed/api-sdk'
import { bindRequestFn } from '@speed/api-sdk/runtime'
import { tenantQueryKey } from '@speed/auth-core'
import { setCurrentTenant, setTenantName, setTenantNames } from '../chrome.js'
import { clearSelfTenants } from '../tenant-self.js'
import { PlatformPermissions } from '../platform-permissions.js'
import { renderDemoApp } from '../test-utils/render.js'
import { createBackend } from './backends.js'
import {
  ERROR_PLACEHOLDER_BY_PATH,
  MODES,
  SCAN_TENANT_ID,
  settle,
  staffSession,
  SWEEP_PATHS,
} from './route-table.js'

const RULE = 'label-content-name-mismatch'

const UNREAD_PATH = '/api/v1/notifications/messages/unread-count'

const BADGE_MAX = 99

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

let evaluatedNodes = 0

function describeFindings(rules: readonly axe.Result[]): string {
  return rules
    .map((rule) => {
      const targets = rule.nodes.map((node) => node.target.join(' ')).join(', ')
      return `  - ${rule.id}: ${rule.help} (${targets || 'no target'})`
    })
    .join('\n')
}

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

async function scanRoute(
  path: string,
  mode: (typeof MODES)[number],
): Promise<void> {
  const unscripted: string[] = []
  bindRequestFn(createBackend(mode, (url) => unscripted.push(url)))
  window.history.replaceState({}, '', path)
  const session = staffSession()
  const { container } = renderDemoApp({
    session,
    wrap: (app) => (
      <PlatformPermissions session={session}>{app}</PlatformPermissions>
    ),
  })
  expect(container.innerHTML, `route ${path} [${mode}] rendered no markup`).not.toBe('')
  await settle()
  expect(
    unscripted,
    `route ${path} [${mode}] issued read(s) the scripted backend does not answer`,
  ).toEqual([])
  const errorPlaceholder =
    mode === 'error' ? ERROR_PLACEHOLDER_BY_PATH[path] : undefined
  if (errorPlaceholder !== undefined) {
    await screen.findByText(errorPlaceholder)
    await settle()
  }
  await expectLabelInName(`route ${path} [${mode}]`)
}

beforeEach(async () => {
  await page.viewport(1280, 900)
  window.history.replaceState({}, '', '/')
  clearSelfTenants()
  setCurrentTenant(SCAN_TENANT_ID)
  setTenantNames(new Map())
  setTenantName(SCAN_TENANT_ID, 'business')
})

afterEach(() => {
  setCurrentTenant(null)
  setTenantNames(new Map())
})

describe('route-level Label in Name in Chromium', () => {
  for (const path of SWEEP_PATHS) {
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

  it('scans an admin route in the sidebar shell form', async () => {
    const unscripted: string[] = []
    bindRequestFn(createBackend('data', (url) => unscripted.push(url)))
    window.history.replaceState({}, '', '/admin/tenants')
    const { container } = renderDemoApp({
      session: staffSession(),
      shellLayout: 'sidebar',
    })
    expect(container.innerHTML).not.toBe('')
    await settle()
    expect(unscripted).toEqual([])
    await expectLabelInName('route /admin/tenants [data, sidebar]')
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
    await user.click(screen.getByTestId('account-entry-trigger'))
    const menu = await screen.findByRole('menu')
    const paper = menu.parentElement as HTMLElement
    await waitFor(() => {
      expect(getComputedStyle(paper).opacity).toBe('1')
    })
    const { flagged, evaluated } = await labelInName(menu)
    evaluatedNodes += evaluated
    if (flagged.length > 0) {
      expect.fail(
        `the account menu has ${flagged.length} Label in Name finding(s):\n` +
          describeFindings(flagged),
      )
    }
    expect(within(menu).getAllByRole('menuitem').length).toBeGreaterThan(0)
    expect(evaluated, 'the rule judged no node in the open menu').toBeGreaterThan(0)
  })

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
      expect(queryClient.getQueryState(unreadKey)?.status).toBe('success')
      await expectLabelInName(`shell bell [${unread} unread]`)
    })
  }

  afterAll(() => {
    expect(
      evaluatedNodes,
      'the Label in Name rule evaluated no node across the whole sweep',
    ).toBeGreaterThan(0)
  })
})
