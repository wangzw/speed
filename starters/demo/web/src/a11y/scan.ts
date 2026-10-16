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
 * applying at every level above it) -- PLUS the page-structure rules (see
 * `PAGE_STRUCTURE_RULES`: a level-one heading, a single main landmark, landmark
 * containment, heading order). Those four carry only axe's best-practice tag,
 * so a tag-only gate would never run them; the gate runs them by rule id and
 * fails on them by rule, because a page-structure defect is exactly the
 * composition-tier signal this tier exists to catch. The remaining
 * best-practice rules stay out: they are a separate advisory pass
 * (`runBestPracticePass`), reported so a signal is not lost but never failing.
 * For every other rule the caller decides fail/warn by the finding's IMPACT,
 * not its tag: `serious` and `critical` fail the gate, `moderate` and `minor`
 * are warned and registered (see `isFailing`). A rule whose impact axe leaves
 * null is treated as a warning, reported rather than silently dropped. The
 * net threshold is "a serious/critical WCAG violation, or any page-structure
 * violation" -- the page-structure rules fail regardless of impact, since
 * axe rates them only moderate.
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
 * jsdom caveats. color-contrast is excluded from the rule set (see
 * UNSWEEPABLE_RULES): jsdom performs no layout or color computation, so a
 * contrast result there is neither trustworthy nor actionable -- the rule is
 * indeterminate for every node, never a violation or a pass. (The named nodes
 * measured in this gate were the AppBar brand text at `/` and a breadcrumb
 * link at `/console/audit/sample [error]`, both chrome text over an
 * emotion-styled background jsdom cannot resolve.) Contrast is a
 * browser-side / manual check, never mechanically decidable here.
 * axe's page-context checks (page-has-heading-one, landmark-one-main) probe
 * for an open modal through a visibility filter that needs real geometry and,
 * failing that, `document.elementsFromPoint`; jsdom implements neither, so the
 * probe throws and every such check would report "incomplete" -- passing by
 * indeterminacy no matter what rendered. The elementsFromPoint polyfill below
 * (an empty hit stack, the truthful answer for an engine that lays nothing
 * out) restores determinacy for those checks, and since the gate runs
 * page-has-heading-one and landmark-one-main as page-structure rules, a page
 * that renders no heading fails the gate with a real page-has-heading-one
 * violation rather than passing incomplete.
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

/**
 * The page-structure rules the gate runs and fails on beside the WCAG tags.
 *
 * axe 4.14 tags all four best-practice -- none carries a `wcag*` tag (measured:
 * page-has-heading-one, landmark-one-main and heading-order carry
 * `cat.semantics` + best-practice, region carries `cat.keyboard` + best-practice)
 * -- so a tag-only gate never runs them: they fall into the advisory pass and
 * warn, and the composition-tier defect the sweep claims to catch (a heading
 * order that breaks across the frame boundary, a page with no main landmark)
 * slips past the failing threshold. Running them here as gate rules closes
 * that gap. Their axe impact is only moderate, so the gate fails them by rule
 * id, not by impact (see `isFailing`).
 */
export const PAGE_STRUCTURE_RULES = [
  'page-has-heading-one',
  'landmark-one-main',
  'region',
  'heading-order',
] as const

/**
 * The rules the gate never runs under jsdom, excluded from the rule set below.
 *
 * `color-contrast` is the one: jsdom performs no layout or color computation,
 * so axe cannot resolve any element's effective foreground/background and
 * every contrast node comes back INDETERMINATE (never a violation, never a
 * pass). The `rules: { 'color-contrast': { enabled: false } }` option does not
 * turn it off -- when `runOnly` is present axe runs exactly the rules it
 * selects, and the `rules` disable is ignored -- so the rule must be dropped
 * from the selected set instead (see `gateRuleIds`). Measured before this
 * exclusion, the two named nodes were the AppBar brand text (`/` anonymous)
 * and a breadcrumb/back link (`.MuiLink-underlineHover`, `/console/audit/sample
 * [error]`) -- both chrome text over an emotion-styled background jsdom cannot
 * compute. Color contrast is a browser-side / manual check (see the class
 * header), never mechanically decidable here; a regression in it cannot be
 * caught by this gate and is out of its scope, not silently passing.
 */
export const UNSWEEPABLE_RULES = ['color-contrast'] as const

/**
 * The rule ids the gate runs: every rule the WCAG AA tags select, minus the
 * jsdom-undecidable ones (`UNSWEEPABLE_RULES`), plus the page-structure rules.
 * axe expands only one runOnly selector at a time (its `runOnly` is either a
 * tag set or a rule list), so the WCAG set is resolved to rule ids through
 * axe's own metadata and the lists are merged.
 */
function gateRuleIds(): string[] {
  const wcag = axe.getRules([...WCAG_AA_TAGS]).map((rule) => rule.ruleId)
  const unsweepable = new Set<string>(UNSWEEPABLE_RULES)
  return [
    ...new Set([
      ...wcag.filter((id) => !unsweepable.has(id)),
      ...PAGE_STRUCTURE_RULES,
    ]),
  ]
}

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
 * Runs axe over the current document under the given selector. color-contrast
 * is kept out of the selector itself (see UNSWEEPABLE_RULES and gateRuleIds):
 * jsdom cannot compute colors, so the rule would only ever report the
 * indeterminate results this gate has no answer for.
 */
async function runAxe(runOnly: axe.RunOptions['runOnly']): Promise<A11yScanResult> {
  ensureDocumentDefaults()
  const result = await axe.run(document, { runOnly })
  return {
    violations: result.violations.map(toFinding),
    incomplete: result.incomplete.map(toFinding),
  }
}

/**
 * Runs the gate: the WCAG 2.1 level AA rules plus the page-structure rules
 * (see `PAGE_STRUCTURE_RULES`), over the current document.
 */
export async function scanPage(): Promise<A11yScanResult> {
  return runAxe({ type: 'rule', values: gateRuleIds() })
}

/** The advisory best-practice pass over the current document. */
export async function runBestPracticePass(): Promise<A11yScanResult> {
  return runAxe({ type: 'tag', values: [...BEST_PRACTICE_TAGS] })
}

/**
 * Whether a finding fails the gate. A page-structure finding always does: it
 * is the composition-tier signal the gate exists to catch, and axe rates it
 * only moderate, so an impact-only threshold would warn and pass. Every other
 * finding fails when axe rates it `serious` or `critical`; the rest -- moderate,
 * minor, and a null impact -- is warned and registered.
 */
export function isFailing(finding: A11yFinding): boolean {
  if ((PAGE_STRUCTURE_RULES as readonly string[]).includes(finding.rule)) {
    return true
  }
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
