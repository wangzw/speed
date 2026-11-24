/** The registration surface handed down from RouteProvider; internal, since the public entry exports the consumer side instead. */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'
import type { RouteContext, RoutePresence, RouteTable } from './types.js'

export interface RouteKitContextValue {
  readonly table: RouteTable
  readonly fallback: (context: RouteContext) => ReactNode
  readonly presence?: RoutePresence
  readonly pending?: (context: RouteContext) => ReactNode
  readonly failed?: (context: RouteContext) => ReactNode
  readonly basename?: string
}

export const RouteKitContext = createContext<RouteKitContextValue | undefined>(
  undefined,
)

export function useRouteKitContext(): RouteKitContextValue {
  const value = useContext(RouteKitContext)
  if (value === undefined) {
    throw new Error(
      'A route-kit component or hook was used outside a <RouteProvider>.',
    )
  }
  return value
}
