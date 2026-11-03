/**
 * The console's invitation-link seam.
 *
 * The invitation surface issues an invitation whose acceptance token comes
 * back exactly once; the operator has to hand the invitee a link to the
 * host's acceptance landing (the demo host lands one at /invitations/:token).
 * The admin-ui package is host-agnostic -- it does not know that path --
 * so the host supplies a builder through the shell (AdminShell's
 * `invitationLink`), which the shell publishes to its routed pages through
 * this context; the invitation page reads it to show the issued link.
 *
 * With no provider (a page rendered on its own, as a unit test renders one)
 * the context is absent and the page falls back to showing the bare token,
 * so an issued acceptance token is never silently dropped.
 */

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
