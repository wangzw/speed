/** The axe-core assertion harness for component, page and browser tests; a
 * jsdom page scan needs an h1 ancestor or page-has-heading-one fails, so supply
 * that ancestor in the render rather than disabling the rule. */

import { expect } from 'vitest'
import axe from 'axe-core'

export const BROWSER_DECIDED_RULES = [
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
  /**
   * Still in the document during its exit transition: MUI has closed it and
   * the background hook lifted `inert`, but MUI keeps the background
   * `aria-hidden` until the transition ends. It still hides the page context,
   * so the page-context rules stay exempt, but its background must not be
   * asserted inert -- that lift is the contract.
   */
  readonly exiting: boolean
}

function isShown(element: HTMLElement): boolean {
  const style = window.getComputedStyle(element)
  return style.display !== 'none' && style.visibility !== 'hidden'
}

// MUI keeps an exiting modal in the DOM until its exit transition ends and
// only then lifts the background's `aria-hidden`. The hook that marks the
// background inert lifts it in the closing commit, so a scan that lands in
// that window would see an "open" overlay over a background with no inert.
// The focus trap's start sentinel carries `tabindex=0` while its modal is
// open and `-1` once it closes, which is the exit signal available in the DOM.
function isExitingModal(root: HTMLElement): boolean {
  for (const testId of ['sentinelStart', 'sentinelEnd']) {
    const sentinel = root.querySelector<HTMLElement>(
      `[data-testid="${testId}"]`,
    )
    if (sentinel !== null) {
      return sentinel.getAttribute('tabindex') === '-1'
    }
  }
  return false
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
      exiting: isExitingModal(root),
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
  const missingAriaHidden: Element[] = []
  const missingInert: Element[] = []
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
        missingAriaHidden.push(child)
      }
      if (!child.hasAttribute('inert')) {
        missingInert.push(child)
      }
    }
  }
  collect(document.body)
  if (missingAriaHidden.length === 0 && missingInert.length === 0) {
    return
  }
  const summarize = (elements: readonly Element[]): string =>
    elements
      .map((element) => `<${element.tagName.toLowerCase()}>`)
      .join(', ')
  const details: string[] = []
  if (missingAriaHidden.length > 0) {
    details.push(`Without aria-hidden="true": ${summarize(missingAriaHidden)}`)
  }
  if (missingInert.length > 0) {
    details.push(`Without inert: ${summarize(missingInert)}`)
  }
  expect.fail(
    `axe page-context exemption refused: an ${overlay.kind === 'other' ? 'overlay' : overlay.kind} is open, but its background is not fully hidden. ${details.join('; ')}`,
  )
}

function summarizeTargets(rule: axe.Result): string {
  const targets = rule.nodes.map((node) => node.target.join(' ')).join(', ')
  return `${rule.id}: ${rule.help} (${targets || 'no target'})`
}

// Do not import `vitest/browser` here: it augments Vitest's matchers with an
// older jest-dom set, and this module is compiled into every jsdom program.
// The browser setup sets this marker; the jsdom setup never does, so a jsdom
// program that names a browser in its user agent is still the jsdom tier.
function browserTierMarker(): boolean {
  return (
    globalThis as { __speedAxeBrowserTier?: boolean }
  ).__speedAxeBrowserTier === true
}

export function isBrowserTier(
  marker: boolean | undefined = browserTierMarker(),
): boolean {
  return marker === true
}

function describeEnvironment(): string {
  return isBrowserTier() ? 'the browser' : 'the jsdom'
}

function reportNonCriticalIncomplete(
  incomplete: readonly axe.Result[],
): void {
  for (const rule of incomplete) {
    console.warn(
      `[test-utils/axe] indeterminate in ${describeEnvironment()} (reported, not asserted): ${summarizeTargets(rule)}`,
    )
  }
}

async function runAxeScan(options: axe.RunOptions = {}): Promise<axe.AxeResults> {
  polyfillElementsFromPoint()
  document.title = document.title || 'test document'
  document.documentElement.lang = document.documentElement.lang || 'en'
  return axe.run(document, options)
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
    // An exiting overlay still hides the page context (MUI keeps
    // `aria-hidden` until the transition ends), but the hook has already
    // lifted `inert` so focus can return; its background is not asserted.
    if (!topOverlay.exiting) {
      assertBackgroundAriaHidden(topOverlay)
    }
    for (const rule of PAGE_CONTEXT_RULES) {
      disabled.add(rule)
    }
  }
  const result = await runAxeScan({
    rules: Object.fromEntries([...disabled].map((rule) => [rule, { enabled: false }])),
  })
  if (result.violations.length > 0) {
    const summary = result.violations.map(summarizeTargets).join('\n')
    expect.fail(`axe violations found:\n${summary}`)
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
  if (decision !== undefined && isBrowserTier()) {
    const undecided = decision.rules.filter((rule) =>
      incomplete.some((result) => result.id === rule))
    if (undecided.length > 0) {
      expect.fail(
        'the browser case declared to decide '
          + `${undecided.join(', ')} left it incomplete`,
      )
    }
  }
  // The inert background is what makes this rule decidable; an incomplete
  // result means axe could not confirm the background is unreachable, so it
  // fails rather than joining the leftovers that are only reported.
  const indeterminateFocus = incomplete.filter(
    (rule) => rule.id === 'aria-hidden-focus',
  )
  if (indeterminateFocus.length > 0) {
    expect.fail(
      `axe could not determine aria-hidden-focus:\n`
        + indeterminateFocus.map(summarizeTargets).join('\n'),
    )
  }
  reportNonCriticalIncomplete(
    incomplete.filter((rule) => !decidedElsewhere.has(rule.id)),
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
    reportNonCriticalIncomplete(result.incomplete)
  }
  return result.violations
}
