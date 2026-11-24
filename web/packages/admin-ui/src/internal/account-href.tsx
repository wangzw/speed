/** The console's account-settings-link seam. */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'

const AdminAccountHrefContext = createContext<string | undefined>(undefined)

export interface AdminAccountHrefProviderProps {
  /** The host's account settings page, or undefined to leave the dialog text-only. */
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
