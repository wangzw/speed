/**
 * The accept-invitation page: the host route an invitee's link lands on.
 *
 * An administrator issues an invitation from the admin, which hands the
 * invitee a link to /invitations/:token. This page is that link's landing.
 * The token grants joining the organization but does not prove the invited
 * address: the inviter holds the link too. The host therefore gates the
 * landing on a session the way the admin area does -- an anonymous viewer
 * meets the host's sign-in / register surface (and re-renders onto the
 * accept screen the moment the session flips) -- and the acceptance binds
 * to that signed-in account only when it holds the invited address as a
 * proven email (pkg/authn/invite judges that). A signed-in viewer meets
 * auth-ui's AcceptInvitationScreen, whose two invitation operations this
 * page binds to the app's generated SDK surface (src/app-api, the platform
 * document's operations). When the account has not proved the address the
 * screen shows its proof step and sends the viewer to the account page's
 * add-email form; the invitation stays pending, so the link they started
 * from accepts after the proof. On acceptance the page announces the new
 * membership to the tenant home, resolves the invited tenant's name from the
 * caller tenant read, records that tenant as the current one, and navigates
 * there (src/app-notice.tsx): the home page shows the confirmation the
 * journey asks for, which the acceptance's own page cannot keep because the
 * navigation unmounts it.
 *
 * The accept answer names the written membership's tenant by identifier only,
 * so the name the request surface must carry is not in it. The page reads
 * the caller tenant list once after the acceptance to resolve it. A failed
 * read still lands the viewer home but leaves the current tenant unknown:
 * writing a tenant the host cannot name would send every tenant-scoped
 * request behind a name no source holds.
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
import { useTranslation } from '@speed/i18n'
import { useNavigate } from '@speed/route-kit'
import { authnInviteAccept, authnInviteAcceptView } from '../app-api/index.js'
import type { AuthnInviteView } from '../app-api/index.js'
import { useAppNotice } from '../app-notice.js'
import { useAppServices } from '../app-services.js'
import {
  getCurrentTenant,
  setCurrentTenant,
  } from '../chrome.js'
import { ACCOUNT_PATH } from '../paths.js'
import { DEMO_NAMESPACE } from '../resources.js'
import { loadSelfTenants } from '../tenant-self.js'
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
  // The tenant the acceptance wrote, as its answer named it. The screen's own
  // onAccepted callback takes no argument, so the tenant identifier is
  // captured where the acceptance resolves and read back when it lands.
  const acceptedTenantName = useRef<string | null>(null)

  // The screen drives these two operations; they reach the backend through
  // the generated surface over the request function the assembly bound
  // (src/main.tsx). The object is stable across renders -- the screen reads
  // it in an effect -- so it is memoized once. The acceptance records the
  // tenant it wrote; the answer carries no name, which the landing resolves
  // separately.
  const invitations = useMemo<InvitationOperations>(
    () => ({
      readInvitation: (value) => authnInviteAcceptView(value).then(toSummary),
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

/** Narrows the generated acceptView answer to the screen's own summary. */
function toSummary(view: AuthnInviteView) {
  return {
    tenant: view.invitation?.tenant ?? '',
    email: view.invitation?.email ?? '',
    status: view.state,
    expiresAt: view.invitation?.expires_at ?? '',
  }
}
