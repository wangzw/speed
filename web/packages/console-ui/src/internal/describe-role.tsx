/**
 * The console's role-description seam.
 *
 * A role row carries a description key (`description_key`) that only the
 * host can resolve: the key names an entry in the host's own resource
 * bundle, which this package does not ship. The host supplies the
 * resolver through the shell or the frameless provider, which publishes it
 * to the routed pages through this context; the permissions page reads it
 * to render a role's description instead of the raw key.
 *
 * With no provider (a page rendered on its own, as a unit test renders one)
 * the context is absent and the page falls back to showing the raw key, so
 * a role never renders an empty description.
 */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'
import type { ConsoleRole } from '../operations.js'

/** Resolves a role's description key, with the role it belongs to. */
export type ConsoleDescribeRole = (
  descriptionKey: string,
  role: ConsoleRole,
) => ReactNode

const ConsoleDescribeRoleContext = createContext<ConsoleDescribeRole | undefined>(
  undefined,
)

export interface ConsoleDescribeRoleProviderProps {
  /** The host's resolver, or undefined to leave the raw key on screen. */
  readonly describeRole?: ConsoleDescribeRole
  readonly children: ReactNode
}

/** Publishes the host's role-description resolver to the pages it renders. */
export function ConsoleDescribeRoleProvider({
  describeRole,
  children,
}: ConsoleDescribeRoleProviderProps) {
  return (
    <ConsoleDescribeRoleContext.Provider value={describeRole}>
      {children}
    </ConsoleDescribeRoleContext.Provider>
  )
}

/** The host's role-description resolver, or undefined when none is supplied. */
export function useConsoleDescribeRole(): ConsoleDescribeRole | undefined {
  return useContext(ConsoleDescribeRoleContext)
}
