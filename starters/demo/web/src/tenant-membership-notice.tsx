/**
 * The one-shot "you have joined the tenant" confirmation, host-held.
 *
 * An invitation is accepted on the public landing (/invitations/:token), which
 * renders outside both shells, and the acceptance then navigates to the tenant
 * home inside the tenant frame. The confirmation the journey asks for has to
 * outlive that route change, so the state lives above both: the app view root
 * (src/app.tsx) mounts the provider, the landing announces through it when an
 * acceptance lands, and the home page reads and dismisses it. The shape mirrors
 * the personal-tenant notice's in-memory, dismissible one
 * (src/personal-tenant-notice.tsx); it is not a write-feedback slot, because the
 * write settled on a page that no longer exists by the time the confirmation
 * is rendered.
 *
 * The notice belongs to the acting principal: a sign-out or an identity swap
 * clears it, so a confirmation never reaches the next identity. Dismissal
 * lasts while that identity stays mounted: leaving the home page and
 * returning in the same session does not bring the notice back.
 */

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react'
import type { ReactElement, ReactNode } from 'react'
import { useAuthState } from '@speed/auth-core'

/** What the home page reads and the landing writes. */
export interface TenantMembershipNotice {
  /** An acceptance landed and the confirmation has not been dismissed. */
  readonly visible: boolean
  /** Record that the viewer just became a member of the invited tenant. */
  announce(): void
  /** Close the confirmation for the rest of this identity's session. */
  dismiss(): void
}

const noNotice: TenantMembershipNotice = {
  visible: false,
  announce: () => undefined,
  dismiss: () => undefined,
}

const TenantMembershipNoticeContext =
  createContext<TenantMembershipNotice>(noNotice)

/**
 * Holds the confirmation for the acting principal until it is dismissed or the
 * principal changes. The provider stays mounted across the route change and
 * the identity change; clearing is state, never a remount, because remounting
 * the subtree would also drop the tenant frame's own state (an impersonation
 * notice, for one).
 */
export function TenantMembershipNoticeProvider({
  principal,
  children,
}: {
  /** The acting account identifier; empty while no session is authenticated. */
  readonly principal: string
  readonly children: ReactNode
}): ReactElement {
  const [announcedFor, setAnnouncedFor] = useState<string | null>(null)
  const [dismissed, setDismissed] = useState(false)
  // A different identity makes an earlier announcement stale; the cleared
  // state is written after the render that already reads it as invisible.
  useEffect(() => {
    setAnnouncedFor(null)
    setDismissed(false)
  }, [principal])
  const announce = useCallback(() => {
    setAnnouncedFor(principal)
    setDismissed(false)
  }, [principal])
  const dismiss = useCallback(() => {
    setDismissed(true)
  }, [])
  const visible = announcedFor === principal && !dismissed
  const value = useMemo<TenantMembershipNotice>(
    () => ({ visible, announce, dismiss }),
    [visible, announce, dismiss],
  )
  return (
    <TenantMembershipNoticeContext.Provider value={value}>
      {children}
    </TenantMembershipNoticeContext.Provider>
  )
}

/**
 * Mounts the provider above the shell switch and reports the acting principal
 * to it. The anonymous state has no principal, so it carries no notice either.
 */
export function TenantMembershipNoticeHost({
  children,
}: {
  readonly children: ReactNode
}): ReactElement {
  const snapshot = useAuthState()
  const principal =
    snapshot.state === 'authenticated'
      ? (snapshot.principal?.account_id ?? '')
      : ''
  return (
    <TenantMembershipNoticeProvider principal={principal}>
      {children}
    </TenantMembershipNoticeProvider>
  )
}

/** The membership confirmation of the app view this component renders inside. */
export function useTenantMembershipNotice(): TenantMembershipNotice {
  return useContext(TenantMembershipNoticeContext)
}
