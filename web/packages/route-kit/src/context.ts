/**
 * The registration surface handed down from RouteProvider to the consuming
 * components and hooks. Internal: `RouteProvider` is the only writer, and
 * the public entry exports the consumer side (RouteView, useNavItems) rather
 * than this context.
 */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'
import type { RouteContext, RoutePresence, RouteTable } from './types.js'

export interface RouteKitContextValue {
  readonly table: RouteTable
  readonly fallback: (context: RouteContext) => ReactNode
  readonly presence?: RoutePresence
  readonly pending?: (context: RouteContext) => ReactNode
  readonly failed?: (context: RouteContext) => ReactNode
  /**
   * The deployment prefix the provider's router was installed under, handed
   * on so derived hrefs are real URLs. Absent means the origin root.
   */
  readonly basename?: string
}

export const RouteKitContext = createContext<RouteKitContextValue | undefined>(
  undefined,
)

/**
 * The table and fallback in scope, or a fail-fast error when read outside a
 * RouteProvider -- a hook with no registration surface has nothing to read,
 * so this reports the composition mistake rather than returning an empty one.
 */
export function useRouteKitContext(): RouteKitContextValue {
  const value = useContext(RouteKitContext)
  if (value === undefined) {
    throw new Error(
      'A route-kit component or hook was used outside a <RouteProvider>.',
    )
  }
  return value
}
