/**
 * The registration surface's consistency check. A route table is the one
 * place a product names its pages, so a broken table is a programmer error
 * caught at assembly (RouteProvider runs this before its children render),
 * never a runtime surprise: a view that silently never renders, or a path
 * that quietly shadows another.
 *
 * It fails on the three configuration mistakes the design calls out -- a
 * path that cannot be a path, a declaration with no view, a duplicate or
 * ambiguous pattern -- and on a missing fallback. It never inspects
 * `label` or `permission`: those are the host's data, not this package's
 * concern.
 */

import { patternsConflict } from './pathing.js'
import type { RouteContext, RouteTable } from './types.js'

/** The fallback render function, as it arrives at runtime (a JS host may omit it). */
type Fallback = ((context: RouteContext) => unknown) | undefined

/**
 * Throw if the table cannot be safely rendered, naming the offending
 * declaration. A valid table passes silently, so the check discriminates
 * only real defects.
 */
export function assertRouteTable(table: RouteTable, fallback: Fallback): void {
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
}
