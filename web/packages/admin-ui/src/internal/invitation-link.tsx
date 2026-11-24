/** The console's invitation-link seam. */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'

const AdminInvitationLinkContext = createContext<
  ((token: string) => string) | undefined
>(undefined)

export interface AdminInvitationLinkProviderProps {
  /**
   * Builds the acceptance link (a host-relative path or an absolute URL)
   * for a token, or undefined to leave the page with the bare token.
   */
  readonly invitationLink?: (token: string) => string
  readonly children: ReactNode
}

/** Publishes the host's invitation-link builder to the pages it renders. */
export function AdminInvitationLinkProvider({
  invitationLink,
  children,
}: AdminInvitationLinkProviderProps) {
  return (
    <AdminInvitationLinkContext.Provider value={invitationLink}>
      {children}
    </AdminInvitationLinkContext.Provider>
  )
}

/** The host's invitation-link builder, or undefined when the shell has none. */
export function useAdminInvitationLink(): ((token: string) => string) | undefined {
  return useContext(AdminInvitationLinkContext)
}
