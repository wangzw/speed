/**
 * WriteFeedbackPageProvider: one page's coordination of its feedback
 * scopes.
 *
 * A feedback scope owns its own slot (see `useWriteFeedback`), and without
 * a provider the slots of one page never touch each other. A host that
 * wants at most one result on screen mounts this provider once, above its
 * pages: every scope registers its clear callback on mount and unregisters
 * on unmount, and a scope that starts a write or settles one asks the page
 * to clear the other scopes' slots first. `reset` does not go through the
 * page -- cancelling an attempt or changing a context clears only the
 * scope's own slot.
 *
 * The provider holds no slot and renders nothing beyond its children: the
 * slots stay in the components that call the hook, and this context is the
 * one seam through which one scope may clear another.
 */

import { createContext, useContext, useMemo, useRef } from 'react'
import type { ReactNode } from 'react'

/** What a page hands its feedback scopes: registration and sibling
 * clearing. Absent when no provider is mounted. */
export interface WriteFeedbackPageCoordination {
  /**
   * Registers one scope's clear callback and returns the unregister. A
   * scope re-registers when the coordination changes, so a remount under
   * the same provider replaces its entry.
   */
  readonly register: (owner: symbol, clear: () => void) => () => void
  /** Clears every registered scope except the owner's. */
  readonly clearOthers: (owner: symbol) => void
}

const WriteFeedbackPageContext =
  createContext<WriteFeedbackPageCoordination | null>(null)

/** The coordination a page's feedback scopes share; `null` outside a
 * provider, which leaves each scope's slot to its own attempts. */
export function useWriteFeedbackPageCoordination(): WriteFeedbackPageCoordination | null {
  return useContext(WriteFeedbackPageContext)
}

/** Provides one page's feedback coordination to the scopes below it.
 * Mount it once per page surface, above the routes. */
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
