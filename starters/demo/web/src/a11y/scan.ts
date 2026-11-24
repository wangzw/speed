import axe from 'axe-core'

export type AxeImpact = 'minor' | 'moderate' | 'serious' | 'critical' | null

export interface A11yFinding {
  readonly rule: string
  readonly impact: AxeImpact
  readonly nodes: number
  readonly help: string
  readonly targets: readonly string[]
}

export interface A11yScanResult {
  readonly violations: readonly A11yFinding[]
  readonly incomplete: readonly A11yFinding[]
}

export const WCAG_AA_TAGS = [
  'wcag2a',
  'wcag2aa',
  'wcag21a',
  'wcag21aa',
] as const

export const BEST_PRACTICE_TAGS = ['best-practice'] as const

export const PAGE_STRUCTURE_RULES = [
  'page-has-heading-one',
  'landmark-one-main',
  'landmark-no-duplicate-main',
  'landmark-main-is-top-level',
  'region',
  'heading-order',
] as const

/** Rules left out of the sweep: color-contrast is indeterminate under jsdom, which has no layout or canvas. */
export const UNSWEEPABLE_RULES = ['color-contrast'] as const

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
    targets: result.nodes.map((node) => node.target.join(' ')),
  }
}

// jsdom lacks elementsFromPoint; axe's modal probe throws on it, so answer an empty list.
function polyfillElementsFromPoint(): void {
  try {
    document.elementsFromPoint(0, 0)
  } catch {
    document.elementsFromPoint = () => []
  }
}

// Page-level rules need a title and a lang; jsdom's document starts without them.
function ensureDocumentDefaults(): void {
  polyfillElementsFromPoint()
  document.title = document.title || 'test document'
  document.documentElement.lang = document.documentElement.lang || 'en'
}

async function runAxe(runOnly: axe.RunOptions['runOnly']): Promise<A11yScanResult> {
  ensureDocumentDefaults()
  const result = await axe.run(document, { runOnly })
  return {
    violations: result.violations.map(toFinding),
    incomplete: result.incomplete.map(toFinding),
  }
}

export async function scanPage(): Promise<A11yScanResult> {
  return runAxe({ type: 'rule', values: gateRuleIds() })
}

export async function runBestPracticePass(): Promise<A11yScanResult> {
  return runAxe({ type: 'tag', values: [...BEST_PRACTICE_TAGS] })
}

export function isFailing(finding: A11yFinding): boolean {
  if ((PAGE_STRUCTURE_RULES as readonly string[]).includes(finding.rule)) {
    return true
  }
  return finding.impact === 'serious' || finding.impact === 'critical'
}

export function formatFinding(finding: A11yFinding): string {
  const where =
    finding.targets.length > 0 ? ` at ${finding.targets.join(', ')}` : ''
  return `  - ${finding.rule} [${finding.impact ?? 'no-impact'}, ${finding.nodes} node(s)] ${finding.help}${where}`
}

export function formatFindings(findings: readonly A11yFinding[]): string {
  return findings.map(formatFinding).join('\n')
}
