/**
 * Structural guard: the three flow-step renderer copies carry the same
 * step-rendering contract.
 *
 * `src/internal/flow-step.tsx` exists once in `auth-ui`, `account-ui` and
 * `console-ui`. The copies are deliberate -- each package owns its step
 * vocabulary and its props -- but they share one rendering contract: the
 * set of step kinds they render, the answer properties they report
 * upward, the render primitives they build a step from, and the WebAuthn
 * seam they drive. A change to how a step renders has to land in all
 * three in the same change (web/AGENTS.md, each package's AGENTS.md).
 *
 * This suite reads the three source files rather than mounting them: the
 * props and the wording differ by design, so a behavioural comparison
 * would flag intended differences. The extracted contract is compared
 * across the copies, and a failure names the copy that drifted and the
 * value it carries.
 *
 * The host is `console-ui` because it is one of the three copies and its
 * suite already reads files through node:fs and has the Node types.
 * Nothing here renders, so the case does not depend on the DOM.
 */

import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const packagesDir = join(
  dirname(fileURLToPath(import.meta.url)),
  '..',
  '..',
  '..',
)

/** One copy of the renderer and the one kind it renders without a literal. */
interface Copy {
  readonly name: string
  readonly source: string
  /**
   * The kind the copy renders in its fallthrough arm rather than behind an
   * explicit `case`/`===` literal: `auth-ui`'s `step.next.kind` switch
   * renders a redirect step in `default`, while `account-ui` and
   * `console-ui` render a challenge step in the branch that follows their
   * explicit `if`s. The parser cannot see an arm that names no literal, so
   * the table states it instead of letting the sets diverge by syntax.
   */
  readonly fallthroughKind: string
}

/** What a copy renders and reports, read from its source. */
interface Contract {
  /** The `next.kind`/`kind` values the copy has a branch for. */
  readonly kinds: readonly string[]
  /** The answer properties the copy reports upward (`factor`, `kind`, ...). */
  readonly answerFields: readonly string[]
  /** The `@mui/material` primitives a rendered step is built from. */
  readonly primitives: readonly string[]
  /** The `@speed/auth-core/webauthn` helpers the passkey arm drives. */
  readonly webauthnHelpers: readonly string[]
}

/** The contract fields the copies must agree on. */
type Field = 'kinds' | 'answerFields' | 'primitives' | 'webauthnHelpers'

const COPIES: readonly Copy[] = [
  { name: 'auth-ui', source: readSource('auth-ui'), fallthroughKind: 'redirect' },
  { name: 'account-ui', source: readSource('account-ui'), fallthroughKind: 'challenge' },
  { name: 'console-ui', source: readSource('console-ui'), fallthroughKind: 'challenge' },
]

function readSource(name: string): string {
  return readFileSync(
    join(packagesDir, name, 'src', 'internal', 'flow-step.tsx'),
    'utf8',
  )
}

/** Case labels of the switch that discriminates `step.next.kind`, stopping at
 * the switch's closing brace so a later switch's labels are not read. */
function switchKinds(source: string): string[] {
  const lines = source.split('\n')
  const start = lines.findIndex((line) =>
    /^\s*switch \(step\.next\.kind\) \{$/.test(line),
  )
  if (start === -1) {
    return []
  }
  const header = lines[start] ?? ''
  const indent = header.length - header.trimStart().length
  const kinds: string[] = []
  for (let i = start + 1; i < lines.length; i += 1) {
    const line = lines[i] ?? ''
    const lineIndent = line.length - line.trimStart().length
    if (line.trim() !== '' && lineIndent <= indent && line.trimStart().startsWith('}')) {
      break
    }
    const literal = /^\s*case '([a-z]+)':/.exec(line)?.[1]
    if (literal !== undefined) {
      kinds.push(literal)
    }
  }
  return kinds
}

/** The step kinds a copy has a branch for (see Copy.fallthroughKind). */
function kindsOf(source: string, fallthroughKind: string): string[] {
  const kinds = new Set<string>(switchKinds(source))
  for (const match of source.matchAll(/step(?:\.next)?\.kind === '([a-z]+)'/g)) {
    const literal = match[1]
    if (literal !== undefined) {
      kinds.add(literal)
    }
  }
  kinds.add(fallthroughKind)
  return [...kinds].sort()
}

/** The property names a copy puts in the answer it reports upward. */
function answerFieldsOf(source: string): string[] {
  const fields = new Set<string>()
  for (const match of source.matchAll(/\{\s*(factor|kind|code|credential)\s*[:},]/g)) {
    const property = match[1]
    if (property !== undefined) {
      fields.add(property)
    }
  }
  return [...fields].sort()
}

/** The `@mui/material` components a copy renders a step with. */
function primitivesOf(source: string): string[] {
  const primitives = new Set<string>()
  for (const match of source.matchAll(/import \w+ from '@mui\/material\/(\w+)'/g)) {
    const component = match[1]
    if (component !== undefined) {
      primitives.add(component)
    }
  }
  return [...primitives].sort()
}

/** The WebAuthn helpers a copy imports from the ceremony seam. */
function webauthnHelpersOf(source: string): string[] {
  const imported = /import \{([^}]*)\} from '@speed\/auth-core\/webauthn'/.exec(source)?.[1]
  if (imported === undefined) {
    return []
  }
  return imported
    .split(',')
    .map((name) => name.trim())
    .filter((name) => name !== '')
    .sort()
}

function contractOf(copy: Copy): Contract {
  return {
    kinds: kindsOf(copy.source, copy.fallthroughKind),
    answerFields: answerFieldsOf(copy.source),
    primitives: primitivesOf(copy.source),
    webauthnHelpers: webauthnHelpersOf(copy.source),
  }
}

/** A message naming the copy that differs, or `''` when all copies agree. */
function driftMessage(field: Field): string {
  const groups = new Map<string, string[]>()
  for (const copy of COPIES) {
    const value = JSON.stringify(contractOf(copy)[field])
    const members = groups.get(value) ?? []
    members.push(copy.name)
    groups.set(value, members)
  }
  if (groups.size === 1) {
    return ''
  }
  const ranked = [...groups.entries()].sort(
    ([, left], [, right]) => right.length - left.length,
  )
  const lines = ranked.map(
    ([value, members]) => `  ${members.join(' and ')}: ${value}`,
  )
  const majority = ranked[0]
  const baseline = majority === undefined ? '' : majority[1].join(' and ')
  return (
    `the flow-step copies disagree on ${field} ` +
    `(majority: ${baseline});\n${lines.join('\n')}`
  )
}

describe('flow-step renderer copies', () => {
  it('renders the same set of step kinds', () => {
    expect(driftMessage('kinds')).toBe('')
  })

  it('reports the same answer properties', () => {
    expect(driftMessage('answerFields')).toBe('')
  })

  it('builds a step from the same render primitives', () => {
    expect(driftMessage('primitives')).toBe('')
  })

  it('drives the passkey ceremony through the same seam', () => {
    expect(driftMessage('webauthnHelpers')).toBe('')
  })
})
