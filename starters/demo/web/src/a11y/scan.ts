/**
 * The route-level accessibility scan harness.
 *
 * This is the page tier of the workspace's component scans: where a package
 * suite scans one component in isolation, this runs axe over a whole rendered
 * document -- the assembled application at one route, chrome and routed page
 * together -- so a defect that only appears in the composition (a landmark the
 * page and the frame each render, a heading order that breaks across the
 * boundary, a control the chrome and the page name the same) is scanned where
 * it actually lives.
 *
 * Rule tags and thresholds. The gate runs the WCAG 2.1 level AA success
 * criteria -- `wcag2a`, `wcag2aa`, `wcag21a`, `wcag21aa` (A plus AA, level A
 * applying at every level above it). Best-practice rules are NOT part of the
 * gate: they are a separate advisory pass (`runBestPracticePass`), reported so
 * a signal is not lost but never failing. The caller decides fail/warn by the
 * finding's IMPACT, not its tag: `serious` and `critical` fail the gate,
 * `moderate` and `minor` are warned and registered (see `isFailing`). A rule
 * whose impact axe leaves null is treated as a warning, reported rather than
 * silently dropped.
 *
 * What axe covers, and what it does not. axe is a MECHANICAL check: it can
 * prove only the subset of accessibility that is decidable from the DOM -- the
 * industry working estimate is 30-40% of the WCAG success criteria. It does
 * not judge reading order, whether an accessible name is a GOOD name, whether
 * a heading level is the RIGHT one for its page, or whether two controls on a
 * page share a name that confuses a screen reader. This suite's own history is
 * the proof: the component scans were green when a human review still found a
 * heading-order inversion and duplicate inline-control names those scans could
 * not see. The gate's value is REGRESSION -- a defect once removed cannot
 * silently return -- never a claim that a page is accessible. See the skill's
 * Accessibility section.
 *
 * jsdom caveats. color-contrast is disabled: jsdom performs no layout or color
 * computation, so a contrast result there is neither trustworthy nor
 * actionable (contrast lives in the theme and is verified browser-side).
 * axe's page-context checks (page-has-heading-one, landmark-one-main) probe
 * for an open modal through a visibility filter that needs real geometry and,
 * failing that, `document.elementsFromPoint`; jsdom implements neither, so the
 * probe throws and every such check would report "incomplete" -- passing by
 * indeterminacy no matter what rendered. The elementsFromPoint polyfill below
 * (an empty hit stack, the truthful answer for an engine that lays nothing
 * out) restores determinacy, so a page that renders no heading fails with a
 * real page-has-heading-one violation rather than passing incomplete.
 */

import axe from 'axe-core'

/** axe's impact vocabulary, or null when axe assigns none. */
export type AxeImpact = 'minor' | 'moderate' | 'serious' | 'critical' | null

/** One axe result reduced to what the gate reports and decides on. */
export interface A11yFinding {
  /** The axe rule id. */
  readonly rule: string
  /** The rule's impact, or null when axe left it unset. */
  readonly impact: AxeImpact
  /** How many nodes the rule flagged. */
  readonly nodes: number
  /** The rule's one-line help text. */
  readonly help: string
}

/** One scan's outcome: the violations found, plus the indeterminate results. */
export interface A11yScanResult {
  readonly violations: readonly A11yFinding[]
  readonly incomplete: readonly A11yFinding[]
}

/** WCAG 2.1 level AA: the A and AA success criteria, the gate's rule set. */
export const WCAG_AA_TAGS = [
  'wcag2a',
  'wcag2aa',
  'wcag21a',
  'wcag21aa',
] as const

/** The advisory rule set: reported, never failing. */
export const BEST_PRACTICE_TAGS = ['best-practice'] as const

function toFinding(result: axe.Result): A11yFinding {
  return {
    rule: result.id,
    impact: (result.impact ?? null) as AxeImpact,
    nodes: result.nodes.length,
    help: result.help,
  }
}

/**
 * Restores determinacy to axe's modal probe (and its page-context checks)
 * under jsdom; see the header. The probe call detects the throwing
 * not-implemented stub `document.elementsFromPoint` ships as -- in a real DOM
 * the method answers and is left alone.
 */
function polyfillElementsFromPoint(): void {
  try {
    document.elementsFromPoint(0, 0)
  } catch {
    document.elementsFromPoint = () => []
  }
}

/** Gives the bare jsdom document the two attributes page-level rules demand. */
function ensureDocumentDefaults(): void {
  polyfillElementsFromPoint()
  document.title = document.title || 'test document'
  document.documentElement.lang = document.documentElement.lang || 'en'
}

/**
 * Runs axe over the current document, scoped to the given rule tags, with
 * color-contrast disabled (jsdom cannot compute it -- see the header).
 */
export async function scanPage(
  tags: readonly string[] = WCAG_AA_TAGS,
): Promise<A11yScanResult> {
  ensureDocumentDefaults()
  const result = await axe.run(document, {
    runOnly: { type: 'tag', values: [...tags] },
    rules: { 'color-contrast': { enabled: false } },
  })
  return {
    violations: result.violations.map(toFinding),
    incomplete: result.incomplete.map(toFinding),
  }
}

/** The advisory best-practice pass over the current document. */
export async function runBestPracticePass(): Promise<A11yScanResult> {
  return scanPage(BEST_PRACTICE_TAGS)
}

/**
 * Whether a finding fails the gate: axe's two highest impacts do. Everything
 * else -- moderate, minor, and a null impact -- is warned and registered.
 */
export function isFailing(finding: A11yFinding): boolean {
  return finding.impact === 'serious' || finding.impact === 'critical'
}

/** One finding as a report line: rule, impact, node count, help. */
export function formatFinding(finding: A11yFinding): string {
  return `  - ${finding.rule} [${finding.impact ?? 'no-impact'}, ${finding.nodes} node(s)] ${finding.help}`
}

/** A list of findings as a newline-joined report block. */
export function formatFindings(findings: readonly A11yFinding[]): string {
  return findings.map(formatFinding).join('\n')
}
