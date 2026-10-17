/**
 * The console's impersonation-change seam.
 *
 * Starting or ending an impersonation changes whose identity every later
 * read belongs to. Only the host holds the query cache, the session and
 * the host-attached state that depend on it, so the console pages report
 * the change and the host does the identity work: it evicts the cache,
 * re-reads the session and the identity-dependent host state, and moves
 * the page where the change says it should go. The host supplies the
 * action through the shell (ConsoleShell's `onImpersonationChange`),
 * which the shell publishes to its routed pages through this context.
 *
 * With no provider (a page rendered on its own, as a unit test renders
 * one) the seam is absent and the page only refreshes its own reads:
 * exactly the behavior of a host that supplies no callback.
 */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'

/** Which identity edge the page just settled. */
export type ConsoleImpersonationChange = 'started' | 'ended'

const ConsoleImpersonationChangeContext = createContext<
  ((change: ConsoleImpersonationChange) => void | Promise<void>) | undefined
>(undefined)

export interface ConsoleImpersonationChangeProviderProps {
  /** The host's action, or undefined to leave the page self-contained. */
  readonly onImpersonationChange?: (
    change: ConsoleImpersonationChange,
  ) => void | Promise<void>
  readonly children: ReactNode
}

/** Publishes the host's identity-change action to the pages it renders. */
export function ConsoleImpersonationChangeProvider({
  onImpersonationChange,
  children,
}: ConsoleImpersonationChangeProviderProps) {
  return (
    <ConsoleImpersonationChangeContext.Provider value={onImpersonationChange}>
      {children}
    </ConsoleImpersonationChangeContext.Provider>
  )
}

/** The host's identity-change action, or undefined when the shell has none. */
export function useConsoleImpersonationChange():
  | ((change: ConsoleImpersonationChange) => void | Promise<void>)
  | undefined {
  return useContext(ConsoleImpersonationChangeContext)
}
