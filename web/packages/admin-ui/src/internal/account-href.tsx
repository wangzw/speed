/**
 * The console's account-settings-link seam.
 *
 * The step-up dialog explains that an account with no active factor must
 * register two-step verification first. The account settings page is the
 * host's route -- this package is host-agnostic and does not know its
 * path -- so the host supplies the destination through the shell
 * (AdminShell's `accountHref`), which the shell publishes to its routed
 * pages through this context; the dialog reads it to render the link.
 *
 * With no provider (a page rendered on its own, as a unit test renders
 * one) or no href, the context is absent and the dialog renders text
 * only: it never holds a host path of its own.
 */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'

const AdminAccountHrefContext = createContext<string | undefined>(undefined)

export interface AdminAccountHrefProviderProps {
  /**
   * The host's account settings page (a host-relative path or an absolute
   * URL), or undefined to leave the dialog text-only.
   */
  readonly accountHref?: string
  readonly children: ReactNode
}

/** Publishes the host's account-settings destination to the pages it renders. */
export function AdminAccountHrefProvider({
  accountHref,
  children,
}: AdminAccountHrefProviderProps) {
  return (
    <AdminAccountHrefContext.Provider value={accountHref}>
      {children}
    </AdminAccountHrefContext.Provider>
  )
}

/** The host's account-settings destination, or undefined when the shell has none. */
export function useAdminAccountHref(): string | undefined {
  return useContext(AdminAccountHrefContext)
}
