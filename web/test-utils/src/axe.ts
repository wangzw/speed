/**
 * The axe-core assertion harness shared by the workspace's component, page and
 * browser tests: the widget and page tiers plus the heading-order probe.
 *
 * A jsdom scan whose document has no h1 ancestor fails on page-has-heading-one;
 * supply the h1 ancestor context in the render rather than disabling the rule
 * silently.
 */

import { expect } from 'vitest'
import axe from 'axe-core'

export const BROWSER_DECIDED_RULES = [
  'aria-hidden-focus',
  'label-content-name-mismatch',
] as const

export type BrowserDecidedRule = (typeof BROWSER_DECIDED_RULES)[number]

export interface BrowserDetermination {
  readonly file: string
  readonly testName: string
  readonly rules: readonly BrowserDecidedRule[]
}

export const BROWSER_DETERMINATIONS = {
  totpManualDisclosure: {
    file: 'packages/ui-kit/src/components/TotpEnrollmentPanel.browser.test.tsx',
    testName:
      'TotpEnrollmentPanel Label in Name in Chromium > settles Label in Name with the manual disclosure open',
    rules: ['label-content-name-mismatch'],
  },
  flowStepManualDisclosure: {
    file: 'packages/admin-ui/src/internal/flow-step.browser.test.tsx',
    testName:
      'FlowStep Label in Name in Chromium > settles Label in Name with the FlowStep manual disclosure open',
    rules: ['label-content-name-mismatch'],
  },
  flowStepAccountManualDisclosure: {
    file: 'packages/account-ui/src/internal/flow-step.browser.test.tsx',
    testName:
      'FlowStep Label in Name in Chromium > settles Label in Name with the account FlowStep manual disclosure open',
    rules: ['label-content-name-mismatch'],
  },
  flowStepFieldsManualDisclosure: {
    file: 'packages/auth-ui/src/internal/flow-step.browser.test.tsx',
    testName:
      'FlowStepFields Label in Name in Chromium > settles Label in Name with the FlowStepFields manual-entry disclosure open',
    rules: ['label-content-name-mismatch'],
  },
  notificationBellUnread: {
    file: 'packages/notification-ui/src/NotificationBell.browser.test.tsx',
    testName:
      'NotificationBell Label in Name in Chromium > settles Label in Name for the bell with an unread count',
    rules: ['label-content-name-mismatch'],
  },
  accountsDetailPanel: {
    file: 'packages/admin-ui/src/AccountsPage.browser.test.tsx',
    testName:
      'AccountsPage focus traps in Chromium > keeps the detail panel focus trap holding over a hidden page',
    rules: ['aria-hidden-focus'],
  },
  accountsOneTimeSecret: {
    file: 'packages/admin-ui/src/AccountsPage.browser.test.tsx',
    testName:
      'AccountsPage focus traps in Chromium > keeps the one-time secret focus trap holding over a hidden page',
    rules: ['aria-hidden-focus'],
  },
  appShellMobileDrawer: {
    file: 'packages/layout-kit/src/components/AppShell.browser.test.tsx',
    testName:
      'AppShell geometry in Chromium > keeps the temporary drawer focus trap holding on the mobile layout',
    rules: ['aria-hidden-focus'],
  },
  appShellSidebarDrawer: {
    file: 'packages/layout-kit/src/components/AppShell.browser.test.tsx',
    testName:
      'AppShell sidebar layout in Chromium > shows the brand once at 400px and pins the open drawer bottom row, with no accessibility violations',
    rules: ['aria-hidden-focus'],
  },
} as const satisfies Record<string, BrowserDetermination>

export type BrowserDeterminationKey = keyof typeof BROWSER_DETERMINATIONS

export interface AxeAssertionOptions {
  readonly disabledRules?: readonly string[]
  readonly decidedInBrowser?: BrowserDeterminationKey
}

const PAGE_CONTEXT_RULES: readonly string[] = ['page-has-heading-one', 'landmark-one-main']

// jsdom ships elementsFromPoint as a stub that throws, which makes axe's
// modal probe leave the page-context rules incomplete; answering "nothing
// there" is truthful for an engine that lays nothing out.
function polyfillElementsFromPoint(): void {
  try {
    document.elementsFromPoint(0, 0)
  } catch {
    document.elementsFromPoint = () => []
  }
}

const FOCUSABLE_CONTROL_SELECTOR = [
  'a[href]',
  'button',
  'input',
  'select',
  'textarea',
  'iframe',
  'summary',
  'audio[controls]',
  'video[controls]',
  '[contenteditable]:not([contenteditable="false"])',
  '[tabindex]',
].join(', ')

// aria-hidden takes a node out of the accessibility tree without removing it
// from the tab order, so it is deliberately not consulted here.
function isReachableControl(candidate: Element): boolean {
  if (candidate.hasAttribute('disabled')) {
    return false
  }
  if (
    candidate.tagName === 'INPUT' &&
    candidate.getAttribute('type') === 'hidden'
  ) {
    return false
  }
  return true
}

function hasFocusableControl(element: Element): boolean {
  if (element.matches(FOCUSABLE_CONTROL_SELECTOR) && isReachableControl(element)) {
    return true
  }
  for (const candidate of element.querySelectorAll(FOCUSABLE_CONTROL_SELECTOR)) {
    if (isReachableControl(candidate)) {
      return true
    }
  }
  return false
}

function isExemptTooltipPortal(element: Element): boolean {
  return (
    element.parentElement === document.body &&
    element.getAttribute('role') === 'tooltip' &&
    !hasFocusableControl(element)
  )
}

// The ARIA conformance list MUI's own modal manager skips, plus a hidden
// input.
const ARIA_HIDDEN_FORBIDDEN_TAGS: ReadonlySet<string> = new Set([
  'TEMPLATE',
  'SCRIPT',
  'STYLE',
  'LINK',
  'MAP',
  'META',
  'NOSCRIPT',
  'PICTURE',
  'COL',
  'COLGROUP',
  'PARAM',
  'SLOT',
  'SOURCE',
  'TRACK',
])

export function isAriaHiddenForbiddenTag(
  tagName: string,
  inputType: string | null = null,
): boolean {
  const tag = tagName.toUpperCase()
  return (
    ARIA_HIDDEN_FORBIDDEN_TAGS.has(tag) ||
    (tag === 'INPUT' && inputType === 'hidden')
  )
}

export type OverlayKind = 'dialog' | 'drawer' | 'other'

export function overlayKindFromClasses(
  classes: readonly string[],
): OverlayKind {
  if (classes.includes('MuiDialog-root')) {
    return 'dialog'
  }
  if (classes.includes('MuiDrawer-root')) {
    return 'drawer'
  }
  return 'other'
}

interface OpenOverlay {
  readonly root: HTMLElement
  readonly ariaModal: HTMLElement | null
  readonly kind: OverlayKind
}

function isShown(element: HTMLElement): boolean {
  const style = window.getComputedStyle(element)
  return style.display !== 'none' && style.visibility !== 'hidden'
}

function openOverlays(): OpenOverlay[] {
  const overlays: OpenOverlay[] = []
  const seen = new Set<HTMLElement>()
  for (const element of document.querySelectorAll<HTMLElement>(
    '[aria-modal="true"], .MuiModal-root',
  )) {
    if (element.getAttribute('aria-hidden') === 'true' || !isShown(element)) {
      continue
    }
    const root = element.closest<HTMLElement>('.MuiModal-root') ?? element
    if (seen.has(root)) {
      continue
    }
    seen.add(root)
    const marker = root.matches('[aria-modal="true"]')
      ? root
      : root.querySelector<HTMLElement>('[aria-modal="true"]')
    const ariaModal = marker !== null && isShown(marker) ? marker : null
    const familyRoot =
      ariaModal?.closest('.MuiDialog-root, .MuiDrawer-root') ?? null
    overlays.push({
      root,
      ariaModal,
      kind:
        familyRoot === null
          ? 'other'
          : overlayKindFromClasses([...familyRoot.classList]),
    })
  }
  return overlays
}

function assertBackgroundAriaHidden(overlay: OpenOverlay): void {
  const keepChain = new Set<Element>()
  for (
    let current: Element | null = overlay.root.parentElement;
    current !== null && current !== document.body;
    current = current.parentElement
  ) {
    keepChain.add(current)
  }
  const offenders: Element[] = []
  const collect = (parent: Element): void => {
    for (const child of parent.children) {
      if (
        child === overlay.root ||
        isAriaHiddenForbiddenTag(child.tagName, child.getAttribute('type')) ||
        isExemptTooltipPortal(child)
      ) {
        continue
      }
      if (keepChain.has(child)) {
        collect(child)
        continue
      }
      if (child.getAttribute('aria-hidden') !== 'true') {
        offenders.push(child)
      }
    }
  }
  collect(document.body)
  if (offenders.length > 0) {
    const summary = offenders
      .map((element) => `<${element.tagName.toLowerCase()}>`)
      .join(', ')
    expect.fail(
      `axe page-context exemption refused: an ${overlay.kind === 'other' ? 'overlay' : overlay.kind} is open, but its background is not aria-hidden. Elements outside it without aria-hidden="true": ${summary}`,
    )
  }
}

function summarizeTargets(rule: axe.Result): string {
  const targets = rule.nodes.map((node) => node.target.join(' ')).join(', ')
  return `${rule.id}: ${rule.help} (${targets || 'no target'})`
}

// Do not import `vitest/browser` here: it augments Vitest's matchers with an
// older jest-dom set, and this module is compiled into every jsdom program.
export type AxeKeyboardDriver = (keys: string) => Promise<void>

function keyboardDriver(): AxeKeyboardDriver | undefined {
  return (
    globalThis as { __speedAxeKeyboardDriver?: AxeKeyboardDriver }
  ).__speedAxeKeyboardDriver
}

export function isBrowserTier(
  driver: AxeKeyboardDriver | undefined = keyboardDriver(),
): boolean {
  return typeof driver === 'function'
}

function describeEnvironment(): string {
  return isBrowserTier() ? 'the browser' : 'the jsdom'
}

function reportNonCriticalIncomplete(
  incomplete: readonly axe.Result[],
  overlay: OpenOverlay | null,
): void {
  for (const rule of incomplete) {
    const untestedTrap =
      rule.id === 'aria-hidden-focus' &&
      overlay !== null &&
      overlay.kind === 'other'
    console.warn(
      `[test-utils/axe] indeterminate in ${describeEnvironment()} (reported, not asserted): ${summarizeTargets(rule)}${untestedTrap ? '; the open overlay is not an aria-modal dialog or drawer, so its focus trap was not exercised' : ''}`,
    )
  }
}

async function runAxeScan(options: axe.RunOptions = {}): Promise<axe.AxeResults> {
  polyfillElementsFromPoint()
  document.title = document.title || 'test document'
  document.documentElement.lang = document.documentElement.lang || 'en'
  return axe.run(document, options)
}

function tabbableElements(root: HTMLElement): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(FOCUSABLE_CONTROL_SELECTOR)].filter((element) => {
    if (element.hasAttribute('disabled')) {
      return false
    }
    if (element.getAttribute('aria-hidden') === 'true') {
      return false
    }
    if (
      element.tagName === 'INPUT' &&
      element.getAttribute('type') === 'hidden'
    ) {
      return false
    }
    if (element.tabIndex < 0) {
      return false
    }
    if (element.getClientRects().length === 0) {
      return false
    }
    return window.getComputedStyle(element).visibility !== 'hidden'
  })
}

function describeActiveElement(element: Element | null): string {
  if (!(element instanceof HTMLElement)) {
    return 'no element'
  }
  const label =
    element.getAttribute('aria-label') ?? element.textContent?.trim() ?? ''
  const suffix = label === '' ? '' : ` "${label.slice(0, 60)}"`
  return `<${element.tagName.toLowerCase()}${suffix}>`
}

function realKeyboard(): AxeKeyboardDriver {
  const driver = keyboardDriver()
  if (driver === undefined) {
    expect.fail(
      'the axe focus-trap check needs the browser tier\'s real keyboard, but its setup did not install the driver',
    )
  }
  return driver
}

async function verifyFocusTrap(overlay: OpenOverlay): Promise<void> {
  const pressKeys = realKeyboard()
  const steps = tabbableElements(overlay.root).length + 2
  const directions = [
    { keys: '{Tab}', label: 'Tab' },
    { keys: '{Shift>}{Tab}{/Shift}', label: 'Shift+Tab' },
  ]
  for (const direction of directions) {
    for (let step = 0; step < steps; step += 1) {
      await pressKeys(direction.keys)
      const active = document.activeElement
      if (!(active instanceof HTMLElement) || !overlay.root.contains(active)) {
        expect.fail(
          `axe aria-hidden-focus is undecided and the open ${overlay.kind}'s focus trap does not hold: ${direction.label} moved focus to ${describeActiveElement(active)}, outside the overlay`,
        )
      }
    }
  }
}

async function runAxeAssertions(
  pageTier: boolean,
  options: AxeAssertionOptions,
): Promise<void> {
  const { disabledRules = [], decidedInBrowser } = options
  const decision =
    decidedInBrowser === undefined
      ? undefined
      : BROWSER_DETERMINATIONS[decidedInBrowser]
  const decidedElsewhere =
    decision !== undefined && !isBrowserTier()
      ? new Set(decision.rules)
      : new Set<string>()
  const disabled = new Set<string>([
    'color-contrast',
    ...(pageTier ? [] : ['region', 'landmark-one-main']),
    ...disabledRules,
  ])
  const overlays = openOverlays()
  const topOverlay = overlays.at(-1) ?? null
  if (topOverlay !== null) {
    assertBackgroundAriaHidden(topOverlay)
    for (const rule of PAGE_CONTEXT_RULES) {
      disabled.add(rule)
    }
  }
  const result = await runAxeScan({
    rules: Object.fromEntries([...disabled].map((rule) => [rule, { enabled: false }])),
  })
  if (result.violations.length > 0) {
    const focusTrapDependent = result.violations.filter(
      (rule) => rule.id === 'aria-hidden-focus' && topOverlay !== null,
    )
    const failing = result.violations.filter(
      (rule) => !focusTrapDependent.includes(rule),
    )
    for (const rule of focusTrapDependent) {
      console.warn(
        `[test-utils/axe] aria-hidden-focus under an open MUI overlay (reported, not asserted): ${summarizeTargets(rule)}`,
      )
    }
    if (failing.length > 0) {
      const summary = failing.map(summarizeTargets).join('\n')
      expect.fail(`axe violations found:\n${summary}`)
    }
  }
  const incomplete = result.incomplete
  const indeterminatePageContext = incomplete.filter((rule) =>
    PAGE_CONTEXT_RULES.includes(rule.id),
  )
  if (indeterminatePageContext.length > 0) {
    const summary = indeterminatePageContext.map(summarizeTargets).join('\n')
    expect.fail(
      `axe could not determine page-context rule(s):\n${summary}\n` +
        'Provide the page context the rule asks about (an h1 ancestor / a main ' +
        'landmark) in the render, or disable the rule explicitly in disabledRules.',
    )
  }
  const focusTrapRule =
    topOverlay !== null && topOverlay.kind !== 'other'
      ? incomplete.find((rule) => rule.id === 'aria-hidden-focus')
      : undefined
  let verifiedFocusTrap: axe.Result | undefined
  if (
    focusTrapRule !== undefined &&
    topOverlay !== null &&
    isBrowserTier()
  ) {
    await verifyFocusTrap(topOverlay)
    verifiedFocusTrap = focusTrapRule
  }
  if (decision !== undefined && isBrowserTier()) {
    const undecided = decision.rules.filter((rule) =>
      incomplete.some(
        (result) => result.id === rule && result !== verifiedFocusTrap,
      ))
    if (undecided.length > 0) {
      expect.fail(
        'the browser case declared to decide '
          + `${undecided.join(', ')} left it incomplete`,
      )
    }
  }
  reportNonCriticalIncomplete(
    incomplete.filter(
      (rule) => rule !== verifiedFocusTrap && !decidedElsewhere.has(rule.id),
    ),
    topOverlay,
  )
}

export async function expectNoAxeViolations(
  options: AxeAssertionOptions = {},
): Promise<void> {
  return runAxeAssertions(false, options)
}

export async function expectNoAxeViolationsForPage(
  options: AxeAssertionOptions = {},
): Promise<void> {
  return runAxeAssertions(true, options)
}

export async function runHeadingOrderCheck(): Promise<readonly axe.Result[]> {
  const result = await runAxeScan({
    runOnly: { type: 'rule', values: ['heading-order'] },
  })
  if (result.incomplete.length > 0) {
    reportNonCriticalIncomplete(result.incomplete, null)
  }
  return result.violations
}
