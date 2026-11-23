/**
 * The accept-invitation page: the host route an invitee's link lands on.
 *
 * An administrator issues an invitation from the admin, which hands the
 * invitee a link to /invitations/:token. This page is that link's landing.
 * The token grants joining the organization but does not prove the invited
 * address: the inviter holds the link too. The page therefore renders the
 * public read to everyone -- an anonymous viewer sees the invitation itself,
 * the inviter, the role and the masked address, with sign-in and
 * create-account actions -- rather than a generic sign-in page. The two
 * actions open the host's own sign-in surface in place, so the URL and the
 * invitation token survive the session flip; a completed password
 * registration still takes the address-verification detour and returns here
 * through the remembered landing (src/invitation-return.ts).
 *
 * A signed-in viewer meets auth-ui's AcceptInvitationScreen, whose
 * invitation operations this page binds to the app's generated SDK surface
 * (src/app-api, the platform document's operations). The screen also reads
 * the guarded eligibility operation, so an account that has not proved the
 * invited address meets the proof step on arrival; the proof links to the
 * account page's add-email form, and the invitation stays pending, so the
 * link they started from accepts afterwards. An accepted link opened again
 * says whether this session already belongs to the organization and offers
 * the way in, answered from the caller tenant list the app reads on mount.
 * On acceptance the page announces the new membership to the tenant home,
 * records the invited tenant as the current one, and navigates there
 * (src/app-notice.tsx): the home page shows the confirmation the journey asks
 * for, which the acceptance's own page cannot keep because the navigation
 * unmounts it.
 *
 * The accept answer names the written membership's tenant. The page still
 * reads the caller tenant list once after the acceptance: the list is what
 * the switcher and the tenant-scoped request surface use, and a failed read
 * leaves the current tenant unknown -- writing a tenant no source names would
 * send every tenant-scoped request behind a name the host cannot show.
 *
 * The route is public and sits outside both shells (the invitee arrives
 * before any session exists), so the app renders its page directly
 * (src/app.tsx) rather than through the shells' content area.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactElement } from 'react'
import Box from '@mui/material/Box'
import Card from '@mui/material/Card'
import { evictTenantQueries, useAuthState } from '@speed/auth-core'
import { AcceptInvitationScreen } from '@speed/auth-ui'
import type {
  InvitationEligibility,
  InvitationOperations,
  InvitationSummary,
} from '@speed/auth-ui'
import type { SignInMode } from '@speed/auth-ui'
import { useTranslation } from '@speed/i18n'
import { useNavigate } from '@speed/route-kit'
import {
  authnInviteAccept,
  authnInviteAcceptView,
  authnInviteEligibility,
} from '../app-api/index.js'
import type {
  AuthnInviteEligibility,
  AuthnInviteView,
} from '../app-api/index.js'
import { useAppNotice } from '../app-notice.js'
import { useAppServices } from '../app-services.js'
import { getCurrentTenant, setCurrentTenant } from '../chrome.js'
import {
  findPageHeading,
  focusFellWithPageSwap,
  focusPageEntry,
} from '../focus-landing.js'
import { ACCOUNT_PATH } from '../paths.js'
import { DEMO_NAMESPACE } from '../resources.js'
import { loadSelfTenants, selfTenantName, useSelfTenantState } from '../tenant-self.js'
import { TENANT_HOME_PATH } from './not-found-page.js'
import { SignInView } from './sign-in-view.js'

export interface AcceptInvitationPageProps {
  /** The invitation token from the matched route. */
  readonly token: string
}

export function AcceptInvitationPage({
  token,
}: AcceptInvitationPageProps): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const snapshot = useAuthState()
  const navigate = useNavigate()
  const notice = useAppNotice()
  const { queryClient } = useAppServices()
  const selfTenants = useSelfTenantState()
  // The sign-in surface the landing's two anonymous actions open, in the mode
  // the viewer picked. The page's URL does not change, so the token survives
  // the session flip and the landing re-renders as the signed-in one.
  const [authMode, setAuthMode] = useState<SignInMode | null>(null)
  // The in-place sign-in surface, and the landing it returns to: the surface
  // unmounts with the control the operator activated, so the landing takes
  // the focus the surface held -- see the effect below. Whether that swap is
  // what took the focus is decided before the surface closes.
  const signInSurface = useRef<HTMLDivElement | null>(null)
  const landing = useRef<HTMLDivElement | null>(null)
  const restoreLandingFocus = useRef(false)
  // The tenant the acceptance wrote, as its answer named it. The screen's own
  // onAccepted callback takes no argument, so the tenant identifier is
  // captured where the acceptance resolves and read back when it lands.
  const acceptedTenantName = useRef<string | null>(null)

  // Closes the in-place sign-in surface from either of its exits (the way
  // back, and a settled sign-in or registration). Its control unmounts with
  // it, so record whether the swap took the focus before the state change
  // removes the surface; a place the operator moved to keeps its focus.
  const closeSignIn = useCallback((): void => {
    restoreLandingFocus.current = focusFellWithPageSwap(signInSurface.current)
    setAuthMode(null)
  }, [])

  // The landing takes the focus the closed surface held. While the signed-in
  // read has not answered there is no heading yet, so the landing takes the
  // focus on its main landmark first, and moves it to the heading when the
  // read supplies one -- only while the landing still holds it. A place the
  // operator moved to keeps its focus.
  useEffect(() => {
    if (authMode !== null || !restoreLandingFocus.current) {
      return
    }
    restoreLandingFocus.current = false
    focusPageEntry(landing.current)
  }, [authMode])

  // The heading arrives with the signed-in invitation read, after the landing
  // has taken the focus on its landmark. Promote that focus to the heading
  // when a visible one appears, while the landing still holds it; a hiding
  // heading does not count, and a place the operator moved to disconnects the
  // observer instead.
  useEffect(() => {
    if (authMode !== null) {
      return
    }
    const container = landing.current
    if (container === null) {
      return
    }
    const observer = new MutationObserver(() => {
      if (document.activeElement !== container) {
        observer.disconnect()
        return
      }
      if (findPageHeading(container) === null) {
        return
      }
      observer.disconnect()
      focusPageEntry(container)
    })
    observer.observe(container, { childList: true, subtree: true })
    return () => {
      observer.disconnect()
    }
  }, [authMode])

  // The screen drives these operations; they reach the backend through
  // the generated surface over the request function the assembly bound
  // (src/main.tsx). The object is stable across renders -- the screen reads
  // it in an effect -- so it is memoized once. The acceptance records the
  // tenant it wrote; the caller tenant read below resolves the display name
  // and settles the switcher's list.
  const invitations = useMemo<InvitationOperations>(
    () => ({
      readInvitation: (value) => authnInviteAcceptView(value).then(toSummary),
      readEligibility: (value) =>
        authnInviteEligibility(value).then(toEligibility),
      // The caller is the request's signed-in identity; the route segment is
      // the token and the body carries nothing the acceptance reads.
      acceptInvitation: (value) =>
        authnInviteAccept(value, {}).then((answer) => {
          acceptedTenantName.current = answer.tenant
          return undefined
        }),
    }),
    [],
  )

  // The landing the acceptance's own answer cannot complete on its own: the
  // accepted tenant is identified by its name, and the landing confirms the
  // caller tenant list now holds it before switching. The announcement comes
  // first -- it is the confirmation the home page shows -- and the navigation
  // happens either way, including when the membership read does not name the
  // tenant.
  const landInInvitedTenant = useCallback((): void => {
    notice.announce(t('home.notice.membershipTitle'))
    const invited = acceptedTenantName.current
    void resolveInvitedTenant(invited).then((tenantName) => {
      const previous = getCurrentTenant()
      if (tenantName !== null) {
        if (previous !== null && previous !== tenantName) {
          evictTenantQueries(queryClient, previous)
        }
        setCurrentTenant(tenantName)
      } else if (previous !== null) {
        // No name for the invited tenant: the outgoing tenant is not this
        // landing's, so it must not stay current behind an unnameable
        // context. Drop it and let the frame settle from its own reads.
        evictTenantQueries(queryClient, previous)
        setCurrentTenant(null)
      }
      navigate(TENANT_HOME_PATH)
    })
  }, [notice, navigate, queryClient, t])

  // The accepted link's way into the organization. The screen only renders
  // the action when the caller tenant list names the tenant, so this switches
  // to a tenant the host can address; an unexpected unlisted name falls
  // through to the home page without writing a tenant no source holds.
  const openInvitedTenant = useCallback(
    (tenant: string): void => {
      if (selfTenantName(tenant) === null) {
        navigate(TENANT_HOME_PATH)
        return
      }
      const previous = getCurrentTenant()
      if (previous !== null && previous !== tenant) {
        evictTenantQueries(queryClient, previous)
      }
      setCurrentTenant(tenant)
      navigate(TENANT_HOME_PATH)
    },
    [navigate, queryClient],
  )

  // The accepted link's member fact comes from the caller tenant list, which
  // the app root reads once per identity. Until that read settles the answer
  // is unknown, and the screen waits: a member must not be told their own
  // organization's link was used while the list is still in flight. A failed
  // read leaves no membership fact to show, so the link falls back to the
  // used wording rather than waiting forever.
  const isMemberOf = useCallback(
    (tenant: string): boolean | undefined => {
      if (selfTenants.failed) {
        return false
      }
      if (!selfTenants.settled) {
        return undefined
      }
      return selfTenants.tenants.some((row) => row.name === tenant)
    },
    [selfTenants],
  )

  // The host's sign-in surface, in the mode the landing's action picked. It
  // keeps the landing: the session flip re-renders this page onto its
  // signed-in surface without navigating away from the token.
  if (authMode !== null) {
    return (
      <Box ref={signInSurface}>
        <SignInView
          keepLanding
          initialMode={authMode}
          onBack={closeSignIn}
          onSignedIn={closeSignIn}
        />
      </Box>
    )
  }

  return (
    <Box
      ref={landing}
      component="main"
      tabIndex={-1}
      sx={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        bgcolor: 'background.default',
        p: 2,
      }}
    >
      <Card variant="outlined" sx={{ width: '100%', maxWidth: 480, p: 4 }}>
        <AcceptInvitationScreen
          token={token}
          invitations={invitations}
          signedIn={snapshot.state === 'authenticated'}
          isMemberOf={isMemberOf}
          onOpenTenant={openInvitedTenant}
          onAccepted={landInInvitedTenant}
          onSignIn={() => setAuthMode('sign-in')}
          onRegister={() => setAuthMode('register')}
          // The account page hosts the email add-and-verify surface. The
          // change-email entry opens the add form directly, which is where
          // an account without the invited address proves it; the invitation
          // stays pending, so the visitor comes back to this link and
          // accepts.
          onProveEmail={() => navigate(`${ACCOUNT_PATH}?change=email`)}
        />
      </Card>
    </Box>
  )
}

/**
 * One read of the caller tenant list, narrowed to the invited tenant's name.
 * Null when the read failed (the list did not settle) or does not name the
 * tenant: the landing must then leave the current tenant unknown rather than
 * announcing a name no source holds. The list already holds the tenant -- an
 * acceptance writes a membership -- so a settled read that omits it is the
 * same unusable answer as a failure.
 */
async function resolveInvitedTenant(
  invited: string | null,
): Promise<string | null> {
  if (invited === null) {
    return null
  }
  const state = await loadSelfTenants()
  if (!state.settled) {
    return null
  }
  return state.tenants.some((tenant) => tenant.name === invited)
    ? invited
    : null
}

/** Narrows the generated acceptView answer to the screen's own summary. Both a
 * pending and an accepted link carry the organization's name and display name;
 * the summary keeps them apart so the screen can address the organization by
 * its display name while using the name for membership and navigation. A
 * terminal read answers the state alone, so every field but it is empty. */
function toSummary(view: AuthnInviteView): InvitationSummary {
  const link = view.invitation
  return {
    tenant: link?.tenant ?? '',
    tenantDisplayName: link?.tenant_display_name ?? '',
    email: link?.email ?? '',
    status: view.state,
    expiresAt: link?.expires_at ?? '',
    invitedByName: link?.invited_by_name,
    role: link?.role,
  }
}

/** Narrows the generated eligibility answer to the screen's own shape. */
function toEligibility(answer: AuthnInviteEligibility): InvitationEligibility {
  return { state: answer.state, emailProved: answer.email_proved }
}
