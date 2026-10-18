/**
 * The one-time "complete account setup" notice, host-held.
 *
 * The signal is the server's personal-tenant ensure answer: when it reports
 * `created: true` the account has just received its personal tenant, and
 * when the same session's GET /auth/me shows setup incomplete (no identifier
 * usable as a login name, or no password) the home page offers the way to
 * finish it. The sign-in screen's registration mode is not the trigger -- an
 * account opened implicitly by an ordinary SMS sign-in gets the same notice
 * -- it only decides where a completed registration lands.
 *
 * The trigger is decided where the ensure answer arrives (TenantFrame,
 * src/app.tsx), and the home page consumes it through this context: both are
 * inside one tenant frame, and the fact must survive a navigation away from
 * the home page and back within the same session. Once the notice has
 * appeared, the home page re-reads the account's current profile and stops
 * offering it as soon as setup is complete -- the trigger is one-time, the
 * wish to finish setup is not. Dismissing it is remembered for the session
 * too; mounting the provider under a new identity resets both.
 */

import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
} from 'react'
import type { ReactElement, ReactNode } from 'react'

/** What the home page reads: whether to offer the notice and how to close it. */
export interface PersonalTenantNotice {
  /** The account just received its personal tenant and setup is incomplete. */
  readonly eligible: boolean
  /** Eligible and not dismissed yet. */
  readonly visible: boolean
  /** Close it for the rest of this identity's session. */
  dismiss(): void
}

const notEligible: PersonalTenantNotice = {
  eligible: false,
  visible: false,
  dismiss: () => undefined,
}

const PersonalTenantNoticeContext =
  createContext<PersonalTenantNotice>(notEligible)

/**
 * Holds the eligibility the ensure answer produced. `eligible` is a fact
 * about the identity, so a provider mounted under a new identity (the host
 * keys it by the principal) starts with no dismissal and the new fact.
 */
export function PersonalTenantNoticeProvider({
  eligible,
  children,
}: {
  readonly eligible: boolean
  readonly children: ReactNode
}): ReactElement {
  const [dismissed, setDismissed] = useState(false)
  const dismiss = useCallback(() => {
    setDismissed(true)
  }, [])
  const value = useMemo<PersonalTenantNotice>(
    () => ({ eligible, visible: eligible && !dismissed, dismiss }),
    [eligible, dismissed, dismiss],
  )
  return (
    <PersonalTenantNoticeContext.Provider value={value}>
      {children}
    </PersonalTenantNoticeContext.Provider>
  )
}

/** The notice state of the tenant frame this page renders inside. */
export function usePersonalTenantNotice(): PersonalTenantNotice {
  return useContext(PersonalTenantNoticeContext)
}
