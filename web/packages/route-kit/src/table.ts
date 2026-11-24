/**
 * The registration surface's consistency check, run at assembly: it fails on an
 * invalid path, a missing view, a duplicate or ambiguous pattern, a missing
 * fallback, or a gated table without its presence inputs.
 */

import { isMoreSpecific, patternsConflict } from './pathing.js'
import { isGated } from './presence.js'
import type { RouteContext, RoutePresence, RouteTable } from './types.js'

type Fallback = ((context: RouteContext) => unknown) | undefined

export interface RouteGating {
  readonly presence?: RoutePresence
  readonly pending?: (context: RouteContext) => unknown
  readonly failed?: (context: RouteContext) => unknown
}

export function assertRouteTable(
  table: RouteTable,
  fallback: Fallback,
  gating: RouteGating = {},
): void {
  if (typeof fallback !== 'function') {
    throw new Error(
      'RouteProvider requires a fallback render function for unmatched paths.',
    )
  }

  table.forEach((declaration, index) => {
    if (
      typeof declaration.path !== 'string' ||
      !declaration.path.startsWith('/')
    ) {
      throw new Error(
        `Route declaration #${index} has an invalid path (${String(
          declaration.path,
        )}); a path must be a string starting with "/".`,
      )
    }
    if (typeof declaration.view !== 'function') {
      throw new Error(
        `Route declaration for path "${declaration.path}" has no view function.`,
      )
    }
  })

  table.forEach((declaration, index) => {
    table.slice(index + 1).forEach((other) => {
      if (!patternsConflict(declaration.path, other.path)) {
        return
      }
      // A literal declared before the param pattern it narrows resolves by order; the reverse shadows it.
      if (isMoreSpecific(declaration.path, other.path)) {
        return
      }
      const kind = declaration.path === other.path ? 'Duplicate' : 'Ambiguous'
      throw new Error(
        `${kind} route paths "${declaration.path}" and "${other.path}" can match the same URL.`,
      )
    })
  })

  const gated = table.find(isGated)
  if (gated === undefined) return

  const missing: string[] = []
  if (gating.presence === undefined) missing.push('presence')
  if (typeof gating.pending !== 'function') missing.push('pending')
  if (typeof gating.failed !== 'function') missing.push('failed')
  if (missing.length > 0) {
    throw new Error(
      `RouteProvider requires ${missing.join(', ')} because route "${gated.path}" declares required features; a gated table needs the presence predicate plus the pending and failed views.`,
    )
  }
}
