/**
 * axe-core assertions for the workspace's component and page tests -- the
 * jsdom unit suites and the Chromium browser tier.
 *
 * Runs axe over the whole document and fails the test on any violation
 * (component-level renders are small enough that scanning the document
 * is fine). color-contrast is disabled by default, and the same disabled
 * set applies in both tiers: jsdom does no layout or color computation,
 * so contrast results there are neither trustworthy nor actionable, and
 * the browser tier shares this helper's rule set instead of growing a
 * contrast gate of its own -- contrast lives in the theme and is
 * verified visually/browser-side. Tests that specifically probe
 * contrast affordances (colors roles, text colors on surfaces) assert
 * the theme values instead.
 *
 * jsdom capability note -- why incomplete results are handled the way
 * they are below, and what "determinate" means here. axe's page-context
 * checks (page-has-heading-one, landmark-one-main) probe for an open
 * modal before evaluating their real question, through a visibility
 * filter that needs real geometry and, failing that,
 * `document.elementsFromPoint`. jsdom has neither, so the probe THROWS
 * and every such check reports "incomplete" -- indeterminate, never
 * failing, no matter what the component rendered. A component scan
 * rendered with no page-heading context would therefore pass by
 * indeterminacy: page-has-heading-one could never make any test fail,
 * and isolated widget renders are exactly that shape. The helper
 * restores determinacy two ways: it polyfills elementsFromPoint with an
 * empty hit stack -- the truthful answer for a document jsdom lays out
 * not at all -- and it detects a really open overlay itself (a visible
 * `[aria-modal="true"]` paper or any other visible MUI Modal-based
 * overlay root, see openOverlays) to reproduce the browser's
 * passForModal exemption, because the modal-open part of axe's own
 * probe cannot run under jsdom. The consequence component suites must
 * live with: a scan whose document contains no h1 FAILS with a
 * page-has-heading-one violation. A test that renders a heading-bearing
 * component in isolation must supply the h1 ancestor context (and,
 * where the component's heading level depends on the page, the level
 * that continues the order); where a component legitimately renders
 * without one the suite must assert that heading situation explicitly
 * in the render, never by disabling the rule silently. A scan taken
 * while a modal is really open is exempt from the page-context rules
 * exactly as a browser run would be (an open-dialog scan relies on that
 * shape); a closed but keepMounted drawer (visibility: hidden) is NOT a
 * modal and still faces the real page rules.
 *
 * Two rule tiers, chosen by the re-export in each package's
 * test-utils/axe.ts:
 *
 * - expectNoAxeViolations is the widget tier: `region` and the
 *   page-level main-landmark rule (landmark-one-main) stay disabled --
 *   the same rationale as region's, which is landmark containment, not
 *   widget semantics: "landmark containment is a page-level concern;
 *   the units under test are components, not full app pages".
 *   `page-has-heading-one` is deliberately NOT part of that carve-out,
 *   because these components DO render real heading elements whose
 *   levels only make sense relative to a page heading -- the scan must
 *   know that context to be honest.
 * - expectNoAxeViolationsForPage is the page tier: `region` and
 *   landmark-one-main stay ENABLED, for packages whose units are
 *   page-level chrome with repeated landmarks (banner, nav, main) --
 *   the frame renders a real `main`, so the scans can and must answer
 *   those rules, and a page-shaped scan's document IS a page, whose
 *   host content must supply the `main` and any page heading the chrome
 *   does not render itself.
 *
 * aria-hidden-focus under an open overlay: axe's static analysis cannot
 * tell whether an overlay's focus trap keeps the aria-hidden background
 * out of the tab order, so it reports the rule as incomplete in both
 * tiers. The browser tier can answer the question the rule is asking, so
 * it does: when the topmost open overlay is an aria-modal MUI Dialog or
 * Drawer, the helper presses Tab and Shift+Tab through more steps than
 * the overlay has tabbable elements, and fails the scan if focus leaves
 * the overlay or lands on one of the rule's incomplete targets. A trap
 * that holds makes the rule determined, and nothing is reported.
 * Overlays that Tab would dismiss -- MUI Menu and Select, whose root is a
 * Modal but whose paper is not aria-modal -- cannot be exercised that
 * way; their indeterminacy is still REPORTED through console.warn,
 * naming the reason. Rules that cannot decide in the scan's environment
 * at all (jsdom lays nothing out and has no canvas 2D context, so
 * text-measurement rules such as label-content-name-mismatch cannot
 * answer) are reported the same way and do not fail the scan. The report
 * names the environment it ran in.
 *
 * The open-overlay page-context exemption is asserted rather than
 * announced. While an overlay is open the helper first checks that every
 * element beside it is hidden from assistive technology -- aria-hidden
 * on everything the overlay does not contain, the ARIA-forbidden tags
 * aside, exactly MUI's own contract. The exemption is only sound when
 * the background really is hidden, so a page that does not hide it fails
 * the scan; when it does, the page-context rules are exempted without a
 * console.warn.
 */

import { expect } from 'vitest'
import axe from 'axe-core'

export interface AxeAssertionOptions {
  /** Extra axe rules to disable beyond color-contrast. */
  readonly disabledRules?: readonly string[]
}

/**
 * Page-context rules: their whole question is "does this PAGE have an
 * h1 / a main landmark", answerable only against the document the
 * component is rendered into. With the elementsFromPoint polyfill below
 * they are determinate in jsdom -- an incomplete result on one of them
 * means the determination itself broke down, and the scan must fail
 * (naming the missing context) rather than pass by indeterminacy. A
 * rule the caller disabled deliberately never runs, so it never lands
 * here. (axe 4.13 ships the main-landmark page rule under the id
 * `landmark-one-main`; `page-has-main` is only that rule's inner check
 * id.)
 */
const PAGE_CONTEXT_RULES: readonly string[] = ['page-has-heading-one', 'landmark-one-main']

/**
 * Restores determinacy to axe's modal probe under jsdom. axe's
 * has-descendant family of page checks (and landmark-one-main) first
 * ask "is a modal open" -- a probe that consults
 * `document.elementsFromPoint` when no semantic modal (role=dialog /
 * aria-modal) is present. jsdom ships elementsFromPoint only as a
 * not-implemented stub that throws, and lays nothing out, so the probe
 * throws and the page rules report incomplete without the polyfill.
 * The polyfill answers "nothing at that point" -- the only truthful
 * answer for an engine that lays nothing out -- which lets the checks
 * reach their real question. The probe call below detects the throwing
 * stub (in a real DOM the method answers normally and is left alone).
 */
function polyfillElementsFromPoint(): void {
  try {
    document.elementsFromPoint(0, 0)
  } catch {
    document.elementsFromPoint = () => []
  }
}

/**
 * Body-child tags that cannot carry aria-hidden -- the ARIA conformance
 * list MUI's own modal manager skips -- plus a hidden input. The
 * background check below exempts them: they are not content an assistive
 * technology could reach, and the attribute would be invalid on them.
 */
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

/**
 * Whether an element with this tag (and, for an input, this type) may
 * carry aria-hidden. Pure, so the conformance list is pinned without a
 * DOM.
 */
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

/** The overlay families whose focus trap real key presses can exercise. */
export type OverlayKind = 'dialog' | 'drawer' | 'other'

/**
 * Maps the marker classes on an overlay root to the family it belongs
 * to. Pure, so the mapping is pinned without a DOM; the DOM-side caller
 * hands in the class list of the nearest MUI dialog/drawer root.
 */
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

/** An overlay the scan found open in the document. */
interface OpenOverlay {
  /** The overlay's outermost element: the scope its focus trap owns. */
  readonly root: HTMLElement
  /** The visible aria-modal element inside it, when there is one. */
  readonly ariaModal: HTMLElement | null
  readonly kind: OverlayKind
}

function isShown(element: HTMLElement): boolean {
  const style = window.getComputedStyle(element)
  return style.display !== 'none' && style.visibility !== 'hidden'
}

/**
 * Every overlay currently open in the document, bottom to top, judged the
 * way a browser would: an overlay is MUI's Modal root (dialog, drawer,
 * menu, popover, select) or any visible aria-modal element without one;
 * a closed but keepMounted overlay is `visibility: hidden` and is not
 * open. axe exempts the page-context rules for a really open dialog
 * through its own passForModal option, and its modal probe first looks
 * for a visible `[aria-modal="true"]` element; jsdom cannot run that
 * probe (see polyfillElementsFromPoint), so the helper asks the question
 * itself over the DOM. The last entry is the topmost overlay -- the trap
 * that owns the keyboard.
 */
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

/**
 * Asserts the page behind `overlay` is hidden from assistive technology,
 * which is what makes the page-context exemption sound. Mirrors MUI's own
 * contract: every body child outside the overlay's ancestor chain carries
 * aria-hidden="true", the ARIA-forbidden tags aside. While ANY of these
 * overlays is open MUI aria-hides the page behind it, so the page-context
 * rules cannot be answered against content hidden from assistive tech --
 * the state passForModal exists to skip. A page that does not hide its
 * background could let a screen reader reach content the overlay covers,
 * so the scan fails instead of exempting the rules silently.
 */
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
      // The overlay and its contents are what stays readable; an ancestor
      // in the chain is stepped through so only its other children are
      // checked.
      if (
        child === overlay.root ||
        isAriaHiddenForbiddenTag(child.tagName, child.getAttribute('type'))
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

/**
 * Names the environment a scan ran in from its user agent. Pure, so the
 * mapping is exercised without a DOM: jsdom puts itself in the user
 * agent (`jsdom/<version>`), the browser tier serves a real browser whose
 * user agent names its own engine, and anything else is neither. The
 * result slots into the indeterminate report below.
 */
export function environmentLabel(userAgent: string): string {
  if (/jsdom/i.test(userAgent)) {
    return 'the jsdom'
  }
  if (/Chrome\/|Chromium\/|Firefox\/|Safari\//i.test(userAgent)) {
    return 'the browser'
  }
  return 'an unknown environment'
}

/**
 * Thin shell over environmentLabel: reads the user agent the scan is
 * running under, so an indeterminate report says where the rule could
 * not be decided.
 */
function describeEnvironment(): string {
  return environmentLabel(
    typeof navigator !== 'undefined' ? navigator.userAgent : '',
  )
}

function reportNonCriticalIncomplete(
  incomplete: readonly axe.Result[],
  overlay: OpenOverlay | null,
): void {
  for (const rule of incomplete) {
    // Deliberate, documented report (never a silent drop): this rule
    // cannot be determined in the environment the scan runs in (see the
    // header comment) and is not on the PAGE_CONTEXT_RULES list that must
    // fail when indeterminate. An aria-hidden-focus report under a menu
    // or select says why the focus trap was not exercised; a real
    // dialog/drawer's trap was, and never reaches here.
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
  // A bare test document (jsdom's, or a fresh browser page) has neither
  // attribute; supply the two page-level rules demand so they never fail
  // a test for the harness's own missing chrome.
  document.title = document.title || 'test document'
  document.documentElement.lang = document.documentElement.lang || 'en'
  return axe.run(document, options)
}

/** The tabbable elements inside an overlay's focus trap, in DOM order. */
function tabbableElements(root: HTMLElement): HTMLElement[] {
  const selector = [
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
  return [...root.querySelectorAll<HTMLElement>(selector)].filter((element) => {
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
    // A laid-out element has a client rect; a display:none or detached
    // one does not. This is the browser tier, where layout is real.
    if (element.getClientRects().length === 0) {
      return false
    }
    return window.getComputedStyle(element).visibility !== 'hidden'
  })
}

/** The elements axe named as an incomplete rule's targets. */
function targetElements(rule: axe.Result): Element[] {
  const found: Element[] = []
  for (const node of rule.nodes) {
    try {
      const element = document.querySelector(node.target.join(' '))
      if (element !== null) {
        found.push(element)
      }
    } catch {
      // A selector this engine will not parse names no target to check;
      // the scope assertion in verifyFocusTrap still covers the escape.
    }
  }
  return found
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

/**
 * The real-keyboard driver the browser setup installs
 * (web/test-utils/src/browser-setup.ts). The harness cannot import
 * `vitest/browser` itself: that module's declarations augment Vitest's
 * matchers with an older jest-dom set whose `toHaveStyle` rejects
 * kebab-case keys, and this shared module is compiled into every jsdom
 * program, where the augmentation would narrow unrelated assertions.
 * The setup runs only in the browser tier, so the import -- and its
 * augmentation -- stays there.
 */
export type AxeKeyboardDriver = (keys: string) => Promise<void>

function realKeyboard(): AxeKeyboardDriver {
  const driver = (
    globalThis as { __speedAxeKeyboardDriver?: AxeKeyboardDriver }
  ).__speedAxeKeyboardDriver
  if (driver === undefined) {
    expect.fail(
      'the axe focus-trap check needs the browser tier\'s real keyboard, but its setup did not install the driver',
    )
  }
  return driver
}

/**
 * Answers the question axe's aria-hidden-focus leaves open by exercising
 * the overlay's focus trap with real key presses: pressing Tab (and
 * Shift+Tab) more times than the overlay has tabbable elements would
 * leave a working trap cycling inside it. Each press is asserted to keep
 * focus inside the overlay and off the rule's incomplete targets. A
 * failure is a real defect -- the background is aria-hidden yet keyboard
 * focus reaches it -- so the scan fails instead of reporting.
 */
async function verifyFocusTrap(
  overlay: OpenOverlay,
  rule: axe.Result,
): Promise<void> {
  // The browser setup's driver drives the provider's real keyboard, so a
  // native Tab reaches MUI's FocusTrap. The jsdom leg never takes this
  // path (the caller checks the environment first).
  const pressKeys = realKeyboard()
  const targets = targetElements(rule)
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
      if (targets.some((target) => target.contains(active))) {
        expect.fail(
          `axe aria-hidden-focus is undecided and the open ${overlay.kind}'s focus trap does not hold: ${direction.label} left focus on an aria-hidden background target (${describeActiveElement(active)})`,
        )
      }
    }
  }
}

async function runAxeAssertions(
  pageTier: boolean,
  options: AxeAssertionOptions,
): Promise<void> {
  const { disabledRules = [] } = options
  // The widget-tier carve-out (see header): region and the page-level
  // main-landmark rules are page concerns, not widget semantics. The
  // scan still runs every rule that IS meaningful for an isolated
  // component, page-has-heading-one included. The page tier keeps the
  // two rules enabled: its scans can and must answer them.
  const disabled = new Set<string>([
    'color-contrast',
    ...(pageTier ? [] : ['region', 'landmark-one-main']),
    ...disabledRules,
  ])
  const overlays = openOverlays()
  const topOverlay = overlays.at(-1) ?? null
  if (topOverlay !== null) {
    // A really open overlay exempts the page-context rules in a browser
    // (axe's passForModal -- and while MUI aria-hides the page behind any
    // Modal-based overlay, the same reasoning holds for its menus and
    // popovers); jsdom cannot run axe's own modal probe, so the helper's
    // DOM-based detector (openOverlays) stands in. The exemption is
    // applied only after the background is asserted hidden: an open
    // overlay that leaves the page readable to assistive technology is a
    // defect, not a reason to skip the page rules.
    assertBackgroundAriaHidden(topOverlay)
    for (const rule of PAGE_CONTEXT_RULES) {
      disabled.add(rule)
    }
  }
  const result = await runAxeScan({
    rules: Object.fromEntries([...disabled].map((rule) => [rule, { enabled: false }])),
  })
  if (result.violations.length > 0) {
    // aria-hidden-focus verdicts depend on live focus management, which
    // static analysis cannot observe: while an open MUI overlay
    // (dialog/drawer/menu/popover -- all Modal-based) is showing, MUI
    // aria-hides the page behind it, and axe cannot tell whether the
    // overlay's focus trap keeps that content out of the tab order (axe
    // itself leaves the modal case incomplete -- measured in both jsdom
    // and Chromium). Under a really open overlay such a violation is
    // reported, not asserted; without one (nothing to trap focus) it
    // fails the scan like any other violation.
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
    describeEnvironment() === 'the browser'
  ) {
    await verifyFocusTrap(topOverlay, focusTrapRule)
    verifiedFocusTrap = focusTrapRule
  }
  reportNonCriticalIncomplete(
    incomplete.filter((rule) => rule !== verifiedFocusTrap),
    topOverlay,
  )
}

/**
 * Assert the current document has no axe violations, with
 * color-contrast disabled (see header) and the widget-tier carve-out
 * applied (see header). Page-context
 * rules left enabled are determinate (see header): a scan without
 * page-heading context fails rather than passing by indeterminacy, and
 * genuinely indeterminate non-page rules are reported, never silently
 * dropped.
 */
export async function expectNoAxeViolations(
  options: AxeAssertionOptions = {},
): Promise<void> {
  return runAxeAssertions(false, options)
}

/**
 * The page tier of expectNoAxeViolations (see header): for scans whose
 * document is a page -- page-level chrome with repeated landmarks and a
 * real `main` -- so `region` and landmark-one-main stay enabled and the
 * scan must answer them. Pass either rule in disabledRules for the rare
 * deliberately incomplete fragment.
 */
export async function expectNoAxeViolationsForPage(
  options: AxeAssertionOptions = {},
): Promise<void> {
  return runAxeAssertions(true, options)
}

/**
 * Run ONLY axe-core's heading-order rule over the current document and
 * return its violations, unlike expectNoAxeViolations (above) which runs
 * the full page-oriented rule set and asserts none exist. Components
 * whose heading level depends on the real page's own heading order
 * (EmptyState and the DataTable empty placeholder render a real heading
 * element whose correct level only the host knows) are otherwise tested
 * with no ancestor heading at all, so the full run above can never reach
 * a heading-order violation regardless of what level they render as.
 * Scoping to one rule lets those tests supply a real h1 ancestor and
 * assert on the skip directly, without adopting the page-level rules
 * (document-title, html-has-lang, region) the shared harness disables
 * for good reason (see its own header comment) but that are irrelevant
 * to heading-order anyway.
 */
export async function runHeadingOrderCheck(): Promise<readonly axe.Result[]> {
  const result = await runAxeScan({
    runOnly: { type: 'rule', values: ['heading-order'] },
  })
  if (result.incomplete.length > 0) {
    reportNonCriticalIncomplete(result.incomplete, null)
  }
  return result.violations
}
