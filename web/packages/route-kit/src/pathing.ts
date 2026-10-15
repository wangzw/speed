/**
 * Path algebra: the pure helpers behind matching, nav selection and the
 * registration-surface consistency check. Pattern matching itself rides
 * react-router-dom's `matchPath` (the package is a bridge onto an existing
 * router, not a second one); everything around it -- literal prefixes,
 * "sits below", and the conflict test -- is defined here so the three
 * consumers share one definition.
 *
 * Nothing here touches React or the router's live state: given a path
 * string and a table, the result is a value.
 */

import { matchPath } from 'react-router-dom'
import type {
  RouteContext,
  RouteDeclaration,
  RouteParams,
  RouteTable,
} from './types.js'

/** One matched declaration: the page and the context its view renders with. */
export interface RouteMatch {
  readonly declaration: RouteDeclaration
  readonly context: RouteContext
}

/**
 * A path's non-empty segments: `/cases/123` -> `['cases', '123']`, `/` ->
 * `[]`, `/cases/` -> `['cases']`. Leading and trailing empty segments drop,
 * so a trailing slash does not change a path's shape.
 */
export function splitPath(path: string): readonly string[] {
  return path.split('/').filter((segment) => segment.length > 0)
}

/** Whether a pattern segment is a `:name` capture rather than a literal. */
export function isParamSegment(segment: string): boolean {
  return segment.startsWith(':')
}

/**
 * A pattern's literal path: its leading literal segments, joined. Everything
 * from the first `:name` on is dropped, so `/cases/:caseId` -> `/cases`,
 * `/cases` -> `/cases`, `/:id` -> `/`. This is the prefix a sub-route sits
 * below for the purpose of nav selection.
 */
export function literalPath(pattern: string): string {
  const literal: string[] = []
  for (const segment of splitPath(pattern)) {
    if (isParamSegment(segment)) break
    literal.push(segment)
  }
  return `/${literal.join('/')}`
}

/**
 * Whether `path` sits strictly below `base` on a segment boundary:
 * `/cases/123` is below `/cases`, but `/casesx` is not, `/cases` itself is
 * not (a full match is `matchesPattern`'s job), and `/` is not treated as an
 * ancestor at all. That last carve-out keeps the design's two clauses
 * consistent: a declaration selects a nav entry when the path sits below its
 * literal path, *and* an unmatched URL selects nothing -- a root-hidden
 * ancestor would otherwise make `/` selected for every URL.
 */
export function isUnder(path: string, base: string): boolean {
  if (base === '/') return false
  return path.startsWith(`${base}/`)
}

/** Whether a concrete path matches a pattern in full (end-anchored). */
export function matchesPattern(pattern: string, pathname: string): boolean {
  return matchPath({ path: pattern, end: true }, pathname) !== null
}

/**
 * Whether two patterns can match the same URL. Patterns of different segment
 * counts never overlap (a full match fixes the count); for equal counts, two
 * patterns overlap when every position is compatible -- equal literals, or a
 * `:name` on at least one side at that position. This catches both a
 * literal duplicate (`/cases` twice) and a structural ambiguity
 * (`/cases/:id` vs `/cases/:caseId`, or `/cases/new` vs `/cases/:id`).
 */
export function patternsConflict(a: string, b: string): boolean {
  const left = splitPath(a)
  const right = splitPath(b)
  if (left.length !== right.length) return false
  return left.every((segment, index) => {
    const other = right[index]
    if (other === undefined) return false
    return isParamSegment(segment) || isParamSegment(other) || segment === other
  })
}

/**
 * The first declaration whose pattern matches `pathname` in full, or
 * `undefined` when none does. First-declaration-wins is deliberate: a table
 * is a list, and its order is the tie-break the consistency check leaves
 * intact by rejecting genuinely overlapping patterns.
 */
export function matchDeclaration(
  table: RouteTable,
  pathname: string,
): RouteMatch | undefined {
  for (const declaration of table) {
    const matched = matchPath({ path: declaration.path, end: true }, pathname)
    if (matched !== null) {
      return {
        declaration,
        context: { path: pathname, params: stringParams(matched.params) },
      }
    }
  }
  return undefined
}

/** react-router's params keep `undefined` for absent keys; a RouteParams does not. */
function stringParams(params: Record<string, string | undefined>): RouteParams {
  const result: Record<string, string> = {}
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined) result[key] = value
  }
  return result
}
