/**
 * Path algebra: the pure helpers behind matching, nav selection and the table
 * consistency check. Pattern matching rides react-router-dom's `matchPath`;
 * the literal-prefix and conflict logic is defined here so consumers share it.
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

export function splitPath(path: string): readonly string[] {
  return path.split('/').filter((segment) => segment.length > 0)
}

export function isParamSegment(segment: string): boolean {
  return segment.startsWith(':')
}

export function literalPath(pattern: string): string {
  const literal: string[] = []
  for (const segment of splitPath(pattern)) {
    if (isParamSegment(segment)) break
    literal.push(segment)
  }
  return `/${literal.join('/')}`
}

export function isUnder(path: string, base: string): boolean {
  if (base === '/') return false
  return path.startsWith(`${base}/`)
}

export function matchesPattern(pattern: string, pathname: string): boolean {
  return matchPath({ path: pattern, end: true }, pathname) !== null
}

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

export function isMoreSpecific(a: string, b: string): boolean {
  const left = splitPath(a)
  const right = splitPath(b)
  if (left.length !== right.length) return false
  let narrowed = false
  for (let index = 0; index < left.length; index += 1) {
    const segment = left[index]
    const other = right[index]
    if (segment === undefined || other === undefined) return false
    if (isParamSegment(segment)) {
      // `a` accepts any value here, so it cannot be narrower than `b`.
      if (!isParamSegment(other)) return false
      continue
    }
    if (isParamSegment(other)) {
      narrowed = true
      continue
    }
    if (segment !== other) return false
  }
  return narrowed
}

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

function stringParams(params: Record<string, string | undefined>): RouteParams {
  const result: Record<string, string> = {}
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined) result[key] = value
  }
  return result
}
