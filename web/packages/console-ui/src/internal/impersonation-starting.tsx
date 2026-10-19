/**
 * The console's impersonation-starting seam.
 *
 * Starting an impersonation is the one console write that changes whose
 * identity the host's whole frame belongs to. A host that renders an
 * identity-dependent frame of its own -- a banner, a dialog, a side panel
 * that must speak about the operator until the switch lands -- needs to
 * know an attempt is in flight before the request goes out, and to be
 * released when the attempt does not land (refused, intercepted by the
 * step-up gate, or thrown).
 *
 * The host supplies the callback through the shell or the frameless
 * provider; the impersonation page's start form calls it synchronously
 * before the write and calls the returned release when the write is
 * refused, blocked by step-up, or throws. A write that lands is the host's
 * to release once its `onImpersonationChange('started')` action has
 * settled, because the host is the one that decides how the new identity
 * takes over.
 *
 * With no provider (a page rendered on its own, as a unit test renders one)
 * the seam is absent and the form simply fires the write: exactly the
 * behavior of a host that supplies no callback.
 */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'

/** Starts an attempt; the returned function releases it, when one is returned. */
export type ConsoleImpersonationStarting = () => void | (() => void)

const ConsoleImpersonationStartingContext = createContext<
  ConsoleImpersonationStarting | undefined
>(undefined)

export interface ConsoleImpersonationStartingProviderProps {
  /** The host's attempt hook, or undefined to leave the form self-contained. */
  readonly onImpersonationStarting?: ConsoleImpersonationStarting
  readonly children: ReactNode
}

/** Publishes the host's impersonation-starting hook to the pages it renders. */
export function ConsoleImpersonationStartingProvider({
  onImpersonationStarting,
  children,
}: ConsoleImpersonationStartingProviderProps) {
  return (
    <ConsoleImpersonationStartingContext.Provider
      value={onImpersonationStarting}
    >
      {children}
    </ConsoleImpersonationStartingContext.Provider>
  )
}

/** The host's impersonation-starting hook, or undefined when none is supplied. */
export function useConsoleImpersonationStarting():
  | ConsoleImpersonationStarting
  | undefined {
  return useContext(ConsoleImpersonationStartingContext)
}
