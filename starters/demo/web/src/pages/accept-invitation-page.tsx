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
import { focusPageLanding } from '@speed/ui-kit'
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
  focusFellWithPageSwap,
  isPageHeading,
} from '../focus-handoff.js'
import { ACCOUNT_PATH } from '../paths.js'
import { DEMO_NAMESPACE } from '../resources.js'
import { loadSelfTenants, selfTenantName, useSelfTenantState } from '../tenant-self.js'
import { TENANT_HOME_PATH } from './not-found-page.js'
import { SignInView } from './sign-in-view.js'

export interface AcceptInvitationPageProps {
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
  const [authMode, setAuthMode] = useState<SignInMode | null>(null)
  const signInSurface = useRef<HTMLDivElement | null>(null)
  const landing = useRef<HTMLDivElement | null>(null)
  const restoreLandingFocus = useRef(false)
  const acceptedTenantName = useRef<string | null>(null)

  const closeSignIn = useCallback((): void => {
    // Record whether the swap took the focus before closing; a place the operator chose keeps it.
    restoreLandingFocus.current = focusFellWithPageSwap(signInSurface.current)
    setAuthMode(null)
  }, [])

  useEffect(() => {
    if (authMode !== null || !restoreLandingFocus.current) {
      return
    }
    restoreLandingFocus.current = false
    const container = landing.current
    if (container === null) {
      return
    }
    if (focusPageLanding(container) === null) {
      container.focus()
    }
  }, [authMode])

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
      const target = focusPageLanding(container)
      if (target === null || !isPageHeading(target)) {
        return
      }
      observer.disconnect()
    })
    observer.observe(container, { childList: true, subtree: true })
    return () => {
      observer.disconnect()
    }
  }, [authMode])

  const invitations = useMemo<InvitationOperations>(
    () => ({
      readInvitation: (value) => authnInviteAcceptView(value).then(toSummary),
      readEligibility: (value) =>
        authnInviteEligibility(value).then(toEligibility),
      acceptInvitation: (value) =>
        authnInviteAccept(value, {}).then((answer) => {
          acceptedTenantName.current = answer.tenant
          return undefined
        }),
    }),
    [],
  )

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
        evictTenantQueries(queryClient, previous)
        setCurrentTenant(null)
      }
      navigate(TENANT_HOME_PATH)
    })
  }, [notice, navigate, queryClient, t])

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
          onProveEmail={() => navigate(`${ACCOUNT_PATH}?change=email`)}
        />
      </Card>
    </Box>
  )
}

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

function toEligibility(answer: AuthnInviteEligibility): InvitationEligibility {
  return { state: answer.state, emailProved: answer.email_proved }
}
