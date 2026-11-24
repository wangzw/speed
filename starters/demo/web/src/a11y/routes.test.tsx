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

interface ScanRecord {
  readonly path: string
  readonly mode: BackendMode | 'anonymous'
  readonly findings: readonly A11yFinding[]
}

const records: ScanRecord[] = []

function expectNoDecidableIncomplete(
  result: A11yScanResult,
  label: string,
): void {
  expect(
    result.incomplete.some((finding) => finding.rule === 'color-contrast'),
    `${label} swept color-contrast, which jsdom cannot decide`,
  ).toBe(false)
  expect(
    result.incomplete.some((finding) => finding.rule === 'aria-prohibited-attr'),
    `${label} put an ARIA attribute on an element that does not permit it`,
  ).toBe(false)
}

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

async function scanRoute(
  path: string,
  mode: BackendMode,
  shellLayout?: AppShellShellLayout,
): Promise<readonly A11yFinding[]> {
  const unscripted: string[] = []
  bindRequestFn(createBackend(mode, (url) => unscripted.push(url)))
  window.history.replaceState({}, '', path)
  const session = staffSession()
  const { container } = renderDemoApp({
    session,
    shellLayout,
    wrap: (app) => (
      <PlatformPermissions session={session}>{app}</PlatformPermissions>
    ),
  })
  expect(container.innerHTML, `route ${path} [${mode}] rendered no markup`).not.toBe('')
  const errorPlaceholder =
    mode === 'error' ? ERROR_PLACEHOLDER_BY_PATH[path] : undefined
  await settle()
  expect(
    unscripted,
    `route ${path} [${mode}] issued read(s) the scripted backend does not answer`,
  ).toEqual([])
  if (errorPlaceholder !== undefined) {
    await screen.findByText(errorPlaceholder)
    await settle()
  } else {
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
    expect(
      result.incomplete.some((finding) => finding.rule === 'heading-order'),
      `route ${path} [${mode}] scanned a heading table axe could not resolve: ` +
        'the tree was still changing when the scan ran',
    ).toBe(false)
  }
  return recordScan(`${path} [${mode}]`, path, mode, result)
}

function warnNonFailing(
  path: string,
  mode: BackendMode | 'anonymous',
  findings: readonly A11yFinding[],
): void {
  for (const finding of findings) {
    console.warn(`[route-a11y] ${path} [${mode}] advisory: ${formatFinding(finding)}`)
  }
}

const MISSING_KEY_WARNING = '[speed-i18n] missing translation key'

const missingKeys: string[] = []
let warnSpy: MockInstance

beforeEach(() => {
  window.history.replaceState({}, '', '/')
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
    expect(ROUTE_PATHS).toEqual(expect.arrayContaining([...EXPECTED_ROUTE_PATHS]))
  })

  it('sweeps the members page role-filter deep link beside the route table', () => {
    expect(SWEEP_PATHS).toContain(ORGANIZATION_MEMBERS_ROLE_DEEP_LINK)
    const bundles = adminUiResources['en-US'] as unknown as {
      readonly invitations: { readonly title: string }
      readonly organization: { readonly nav: { readonly invitations: string } }
    }
    expect(bundles.invitations.title).toBe(bundles.organization.nav.invitations)
  })

  it('names the organization roles page and its menu entry with the same label', () => {
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

  for (const section of ACCOUNT_SECTIONS) {
    it(`scans the account settings pane ${section} in the sidebar shell form`, async () => {
      await scanSidebarRoute(ACCOUNT_SECTION_PATHS[section])
    })
  }

  it('scans an admin route in the sidebar shell form', async () => {
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
    await within(screen.getByRole('main')).findByText(
      demoEnUS.home.notice.membershipTitle,
    )
    await settle()
    const result = await scanPage()
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
