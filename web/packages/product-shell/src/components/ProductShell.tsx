/**
 * The tenant-facing assembly shell: a three-branch view machine over an
 * auth-core session. It branches on the session snapshot and performs no
 * session operations, navigation or requests.
 */

import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import { useAuthState } from '@speed/auth-core'
import { SessionEndedScreen } from '@speed/auth-ui'
import { useTranslation } from '@speed/i18n'
import { AppShell } from '@speed/layout-kit'
import type { AppShellProps } from '@speed/layout-kit'
import {
  AnnouncementRegion,
  focusPageLanding,
  visuallyHiddenSx,
} from '@speed/ui-kit'
import { PRODUCT_SHELL_NAMESPACE } from '../resources.js'

type AppShellChromeProps = Pick<
  AppShellProps,
  | 'navItems'
  | 'header'
  | 'headerActions'
  | 'userMenu'
  | 'banner'
  | 'navLabel'
  | 'navHeader'
  | 'navFooter'
  | 'mobileOpen'
  | 'onMobileOpenChange'
  | 'sidebarCollapsed'
  | 'onSidebarCollapsedChange'
  | 'sidebarWidth'
  | 'shellLayout'
  | 'sx'
>

/** The actions a sessionEnded render function receives. */
export interface ProductShellSessionEndedActions {
  /** Returns the viewer to the sign-in branch; a no-op when no `signIn`
   * view was supplied. */
  readonly returnToSignIn: () => void
}

/** The ended branch's slot: a node rendered as-is, or a render function
 * given the actions the default ended screen wires. */
export type ProductShellSessionEndedSlot =
  | ReactNode
  | ((actions: ProductShellSessionEndedActions) => ReactNode)

/** The props of ProductShell: the AppShell chrome props passed through,
 * plus the three slots. */
export interface ProductShellProps extends AppShellChromeProps {
  /** The host's sign-in surface for a fresh or unattached visitor.
   * Omitted, that branch renders nothing. */
  readonly signIn?: ReactNode
  /** The view for a session that ended mid-use. Omitted, auth-ui's
   * SessionEndedScreen renders with its action wired to `returnToSignIn`. */
  readonly sessionEnded?: ProductShellSessionEndedSlot
  /** The app content, rendered inside the frame's main landmark while
   * the snapshot is authenticated. */
  readonly children: ReactNode
}

type Branch = 'frame' | 'ended' | 'signIn'

/** Renders the AppShell frame, the session-ended view, or the sign-in
 * view, chosen from the session snapshot. */
export function ProductShell({
  navItems,
  header,
  headerActions,
  userMenu,
  banner,
  navLabel,
  navHeader,
  navFooter,
  mobileOpen,
  onMobileOpenChange,
  sidebarCollapsed,
  onSidebarCollapsedChange,
  sidebarWidth,
  shellLayout,
  sx,
  signIn,
  sessionEnded,
  children,
}: ProductShellProps) {
  const snapshot = useAuthState()
  const [reachedApp, setReachedApp] = useState(false)
  const { t, i18n } = useTranslation(PRODUCT_SHELL_NAMESPACE)
  const branchContainerRef = useRef<HTMLDivElement | null>(null)
  const previousBranchRef = useRef<Branch | null>(null)

  useEffect(() => {
    if (snapshot.state === 'authenticated') {
      setReachedApp(true)
    }
  }, [snapshot.state])

  const branch: Branch =
    snapshot.state === 'authenticated'
      ? 'frame'
      : reachedApp
        ? 'ended'
        : 'signIn'

  useEffect(() => {
    const previous = previousBranchRef.current
    previousBranchRef.current = branch
    if (previous === null || previous === branch) {
      return
    }
    const container = branchContainerRef.current
    if (container === null) {
      return
    }
    const active = document.activeElement
    if (
      active !== null &&
      active !== container &&
      container.contains(active)
    ) {
      return
    }
    if (focusPageLanding(container) === null) {
      container.focus()
    }
  }, [branch])

  const announcementsRegistered = i18n.hasResourceBundle(
    i18n.language ?? '',
    PRODUCT_SHELL_NAMESPACE,
  )

  const returnToSignIn = (): void => {
    if (signIn !== undefined) {
      setReachedApp(false)
    }
  }

  if (snapshot.state === 'authenticated') {
    return (
      <Box ref={branchContainerRef} tabIndex={-1}>
        <AppShell
          navItems={navItems}
          header={header}
          headerActions={headerActions}
          userMenu={userMenu}
          banner={banner}
          navLabel={navLabel}
          navHeader={navHeader}
          navFooter={navFooter}
          mobileOpen={mobileOpen}
          onMobileOpenChange={onMobileOpenChange}
          sidebarCollapsed={sidebarCollapsed}
          onSidebarCollapsedChange={onSidebarCollapsedChange}
          sidebarWidth={sidebarWidth}
          shellLayout={shellLayout}
          sx={sx}
        >
          {children}
        </AppShell>
      </Box>
    )
  }
  if (reachedApp) {
    return (
      <Box ref={branchContainerRef} component="main" tabIndex={-1}>
        {announcementsRegistered && (
          <AnnouncementRegion
            message={t('announcements.sessionEnded')}
            sx={visuallyHiddenSx}
          />
        )}
        {sessionEnded !== undefined ? (
          typeof sessionEnded === 'function' ? (
            sessionEnded({ returnToSignIn })
          ) : (
            sessionEnded
          )
        ) : (
          <SessionEndedScreen onSignIn={returnToSignIn} />
        )}
      </Box>
    )
  }
  if (signIn === undefined) {
    return null
  }
  return (
    <Box ref={branchContainerRef} tabIndex={-1}>
      {signIn}
    </Box>
  )
}
