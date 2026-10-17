/**
 * The registration surface's consistency check. A route table is the one
 * place a product names its pages, so a broken table is a programmer error
 * caught at assembly (RouteProvider runs this before its children render),
 * never a runtime surprise: a view that silently never renders, or a path
 * that quietly shadows another.
 *
 * It fails on the three configuration mistakes the design calls out -- a
 * path that cannot be a path, a declaration with no view, a duplicate or
 * ambiguous pattern -- on a missing fallback, and on a gated table that
 * omits the feature-presence inputs it needs: a predicate and the two views
 * rendered while that predicate is pending or failed. It never inspects
 * `label` or `permission`: those are the host's data, not this package's
 * concern.
 */

import { patternsConflict } from './pathing.js'
import { isGated } from './presence.js'
import type { RouteContext, RoutePresence, RouteTable } from './types.js'

/** The fallback render function, as it arrives at runtime (a JS host may omit it). */
type Fallback = ((context: RouteContext) => unknown) | undefined

/** The feature-presence inputs a provider was given, as they arrive at runtime. */
export interface RouteGating {
  readonly presence?: RoutePresence
  readonly pending?: (context: RouteContext) => unknown
  readonly failed?: (context: RouteContext) => unknown
}

/**
 * Throw if the table cannot be safely rendered, naming the offending
 * declaration. A valid table passes silently, so the check discriminates
 * only real defects.
 */
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
      if (patternsConflict(declaration.path, other.path)) {
        const kind =
          declaration.path === other.path ? 'Duplicate' : 'Ambiguous'
        throw new Error(
          `${kind} route paths "${declaration.path}" and "${other.path}" can match the same URL.`,
        )
      }
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
