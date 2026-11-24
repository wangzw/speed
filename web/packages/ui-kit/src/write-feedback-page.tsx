/** One page's coordination of its feedback scopes, so at most one result shows. */

import { createContext, useContext, useMemo, useRef } from 'react'
import type { ReactNode } from 'react'

/** What a page hands its feedback scopes: registration and sibling clearing. */
export interface WriteFeedbackPageCoordination {
  /** Registers one scope's clear callback and returns the unregister. */
  readonly register: (owner: symbol, clear: () => void) => () => void
  /** Clears every registered scope except the owner's. */
  readonly clearOthers: (owner: symbol) => void
}

const WriteFeedbackPageContext =
  createContext<WriteFeedbackPageCoordination | null>(null)

/** The shared coordination, or `null` outside a provider. */
export function useWriteFeedbackPageCoordination(): WriteFeedbackPageCoordination | null {
  return useContext(WriteFeedbackPageContext)
}

/** Provides one page's feedback coordination to the scopes below it. */
export function WriteFeedbackPageProvider({
  children,
}: {
  readonly children: ReactNode
}) {
  const scopes = useRef(new Map<symbol, () => void>())
  const coordination = useMemo<WriteFeedbackPageCoordination>(
    () => ({
      register(owner, clear) {
        scopes.current.set(owner, clear)
        return () => {
          if (scopes.current.get(owner) === clear) {
            scopes.current.delete(owner)
          }
        }
      },
      clearOthers(owner) {
        for (const [key, clear] of scopes.current) {
          if (key !== owner) {
            clear()
          }
        }
      },
    }),
    [],
  )
  return (
    <WriteFeedbackPageContext.Provider value={coordination}>
      {children}
    </WriteFeedbackPageContext.Provider>
  )
}
