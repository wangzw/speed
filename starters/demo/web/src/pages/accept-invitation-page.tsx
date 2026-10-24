/**
 * The accept-invitation page: the host route an invitee's link lands on.
 *
 * An administrator issues an invitation from the console, which hands the
 * invitee a link to /invitations/:token. This page is that link's landing.
 * The accept surface is public and the token is the whole credential: the
 * membership lands on the invited address's account, not on whoever happens
 * to be signed in (pkg/authn/invite binds it by the invited address). The
 * host nevertheless gates the landing on a session the way the console area
 * does -- an anonymous viewer meets the host's sign-in / register surface
 * (and re-renders onto the accept screen the moment the session flips), so
 * the invitee arrives with an identity of their own -- but that gate is a
 * composition choice, not what authorizes or binds the acceptance. A
 * signed-in viewer meets auth-ui's AcceptInvitationScreen, whose two
 * invitation operations this page binds to the app's generated SDK surface
 * (src/app-api, the platform document's operations). On acceptance the page
 * announces the new membership to the tenant home, resolves the invited
 * tenant's alias from the caller tenant read, records that tenant as the
 * current one, and navigates there (src/tenant-membership-notice.tsx): the
 * home page shows the confirmation the journey asks for, which the
 * acceptance's own page cannot keep because the navigation unmounts it.
 *
 * The accept answer names the written membership's tenant by identifier only,
 * so the alias the request surface must carry is not in it. The page reads
 * the caller tenant list once after the acceptance to resolve it. A failed
 * read still lands the viewer home but leaves the current tenant unknown:
 * writing a tenant the host cannot name would send every tenant-scoped
 * request behind an alias no source holds.
 *
 * The route is public and sits outside both shells (the invitee arrives
 * before any session exists), so the app renders its page directly
 * (src/app.tsx) rather than through the shells' content area.
 */

import { useCallback, useMemo, useRef } from 'react'
import type { ReactElement } from 'react'
import Box from '@mui/material/Box'
import Card from '@mui/material/Card'
import { evictTenantQueries, useAuthState } from '@speed/auth-core'
import { AcceptInvitationScreen } from '@speed/auth-ui'
import type { InvitationOperations } from '@speed/auth-ui'
import { useNavigate } from '@speed/route-kit'
import { authnInviteAccept, authnInviteAcceptView } from '../app-api/index.js'
import type { AuthnInviteView } from '../app-api/index.js'
import { useAppServices } from '../app-services.js'
import {
  getCurrentTenantId,
  setCurrentTenantId,
  setTenantAlias,
} from '../chrome.js'
import { loadSelfTenants } from '../tenant-self.js'
import { useTenantMembershipNotice } from '../tenant-membership-notice.js'
import { TENANT_HOME_PATH } from './not-found-page.js'
import { SignInView } from './sign-in-view.js'

export interface AcceptInvitationPageProps {
  /** The invitation token from the matched route. */
  readonly token: string
}

export function AcceptInvitationPage({
  token,
}: AcceptInvitationPageProps): ReactElement {
  const snapshot = useAuthState()
  const navigate = useNavigate()
  const membershipNotice = useTenantMembershipNotice()
  const { queryClient } = useAppServices()
  // The tenant the acceptance wrote, as its answer named it. The screen's own
  // onAccepted callback takes no argument, so the tenant identifier is
  // captured where the acceptance resolves and read back when it lands.
  const acceptedTenantId = useRef<string | null>(null)

  // The screen drives these two operations; they reach the backend through
  // the generated surface over the request function the assembly bound
  // (src/main.tsx). The object is stable across renders -- the screen reads
  // it in an effect -- so it is memoized once. The acceptance records the
  // tenant it wrote; the answer carries no alias, which the landing resolves
  // separately.
  const invitations = useMemo<InvitationOperations>(
    () => ({
      readInvitation: (value) => authnInviteAcceptView(value).then(toSummary),
      acceptInvitation: (value, input) =>
        authnInviteAccept(value, {
          ...(input.displayName === undefined
            ? {}
            : { display_name: input.displayName }),
          ...(input.password === undefined ? {} : { password: input.password }),
        }).then((answer) => {
          acceptedTenantId.current = answer.tenant
          return undefined
        }),
    }),
    [],
  )

  // The landing the acceptance's own answer cannot complete: the alias is
  // resolved from the caller tenant read, then written together with the
  // tenant so every tenant-scoped request announces a name the server
  // resolves. The announcement comes first -- it is the confirmation the
  // home page shows -- and the navigation happens either way, including when
  // the alias read fails.
  const landInInvitedTenant = useCallback((): void => {
    membershipNotice.announce()
    const tenantId = acceptedTenantId.current
    void resolveInvitedTenantAlias(tenantId).then((alias) => {
      const previous = getCurrentTenantId()
      if (tenantId !== null && alias !== null) {
        if (previous !== null && previous !== tenantId) {
          evictTenantQueries(queryClient, previous)
        }
        setTenantAlias(tenantId, alias)
        setCurrentTenantId(tenantId)
      } else if (previous !== null) {
        // No name for the invited tenant: the outgoing tenant is not this
        // landing's, so it must not stay current behind an unnameable
        // context. Drop it and let the frame settle from its own reads.
        evictTenantQueries(queryClient, previous)
        setCurrentTenantId(null)
      }
      navigate(TENANT_HOME_PATH)
    })
  }, [membershipNotice, navigate, queryClient])

  // The host renders its sign-in / register surface ahead of the accept
  // screen so the invitee arrives with a session of their own; the accept
  // surface itself is public and the token is the whole credential -- the
  // membership binds to the invited address, not to this session. An
  // anonymous viewer meets the sign-in surface, and the session flip
  // re-renders this page onto the accept screen.
  if (snapshot.state !== 'authenticated') {
    // The URL carries the invitation token: a registration completed here
    // must stay on it, so the session flip renders this page's accept
    // screen rather than the app navigating to the register mode's landing.
    return <SignInView keepLanding />
  }

  return (
    <Box
      component="main"
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
          onAccepted={landInInvitedTenant}
        />
      </Card>
    </Box>
  )
}

/**
 * One read of the caller tenant list, narrowed to the invited tenant's alias.
 * Null when the read failed (the list did not settle) or does not name the
 * tenant: the landing must then leave the current tenant unknown rather than
 * announcing a name no source holds. The list already holds the tenant -- an
 * acceptance writes a membership -- so a settled read that omits it is the
 * same unusable answer as a failure.
 */
async function resolveInvitedTenantAlias(
  tenantId: string | null,
): Promise<string | null> {
  if (tenantId === null) {
    return null
  }
  const state = await loadSelfTenants()
  if (!state.settled) {
    return null
  }
  return state.tenants.find((tenant) => tenant.id === tenantId)?.alias ?? null
}

/** Narrows the generated acceptView answer to the screen's own summary. */
function toSummary(view: AuthnInviteView) {
  return {
    tenant: view.invitation.tenant,
    email: view.invitation.email,
    status: view.invitation.status,
    expiresAt: view.invitation.expires_at,
  }
}
