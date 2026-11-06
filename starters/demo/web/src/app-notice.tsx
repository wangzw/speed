/**
 * The host-held one-shot notice shown on the tenant home.
 *
 * Two journeys end on a page that did not perform their write: an invitation
 * accepted on the public landing (/invitations/:token), which renders outside
 * both shells, navigates to the tenant home inside the tenant frame; and a
 * landed ownership transfer or organization deletion leaves the profile page
 * for the tenant home because the caller is no longer a member. The
 * confirmation the journey asks for has to outlive that route change, so the
 * state lives above both: the app view root (src/app.tsx) mounts the
 * provider, the acting page announces through it, and the home page reads and
 * dismisses it. It is held in memory and dismissible; it is not a
 * write-feedback slot, because the write settled on a page that no longer
 * exists by the time the confirmation is rendered.
 *
 * The notice belongs to the acting principal: a sign-out or an identity swap
 * clears it, so a confirmation never reaches the next identity. Dismissal
 * lasts while that identity stays mounted: leaving the home page and
 * returning in the same session does not bring the notice back.
 *
 * A later announcement replaces an earlier one, both as the visible block and
 * as the live region's text.
 *
 * The announcement's live region lives here too, not on the home page: a live
 * region announces only content changes that follow its own insertion, and
 * the home page mounts with the notice already visible (the announcing page
 * fills it before it navigates), so a region rendered there would be born
 * together with its text. This provider is mounted from the first render of
 * the app, so its region is already in the tree, empty, when an announcement
 * fills it.
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
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import { useAuthState } from '@speed/auth-core'
import { useTranslation } from '@speed/i18n'
import { visuallyHiddenSx } from '@speed/ui-kit'
import { DEMO_NAMESPACE } from './resources.js'

/** What the home page reads and the announcing pages write. */
export interface AppNotice {
  /** An announcement landed, has not been dismissed, and belongs to the
   * acting identity. */
  readonly visible: boolean
  /** The text the announcing action translated when it called `announce`. */
  readonly message: string
  /** Record a confirmation the home page shows once the viewer lands there. */
  announce(message: string): void
  /** Close the notice for the rest of this identity's session. */
  dismiss(): void
}

/** The announcement one identity has made, and whether it was dismissed. */
interface Announcement {
  readonly principal: string
  readonly message: string
}

const noNotice: AppNotice = {
  visible: false,
  message: '',
  announce: () => undefined,
  dismiss: () => undefined,
}

const AppNoticeContext = createContext<AppNotice>(noNotice)

/**
 * Holds the confirmation for the acting principal until it is dismissed or the
 * principal changes. The provider stays mounted across the route change and
 * the identity change; clearing is state, never a remount, because remounting
 * the subtree would also drop the tenant frame's own state (an impersonation
 * notice, for one).
 */
export function AppNoticeProvider({
  principal,
  children,
}: {
  /** The acting account identifier; empty while no session is authenticated. */
  readonly principal: string
  readonly children: ReactNode
}): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const [announcement, setAnnouncement] = useState<Announcement | null>(null)
  const [dismissed, setDismissed] = useState(false)
  // A different identity makes an earlier announcement stale; the cleared
  // state is written after the render that already reads it as invisible.
  useEffect(() => {
    setAnnouncement(null)
    setDismissed(false)
  }, [principal])
  const announce = useCallback(
    (message: string) => {
      setAnnouncement({ principal, message })
      setDismissed(false)
    },
    [principal],
  )
  const dismiss = useCallback(() => {
    setDismissed(true)
  }, [])
  const visible =
    announcement !== null && announcement.principal === principal && !dismissed
  const message = visible && announcement !== null ? announcement.message : ''
  const value = useMemo<AppNotice>(
    () => ({ visible, message, announce, dismiss }),
    [visible, message, announce, dismiss],
  )
  return (
    <AppNoticeContext.Provider value={value}>
      {children}
      {/* One standing, clipped status region for the whole provider life:
          empty while no announcement has landed, filled when one does. The
          visible confirmation on the home page is a separate, non-live
          block (src/pages/home-page.tsx) -- this region is the announcement.
          The message slot's `overflow: auto` is overridden so the squeezed
          box is not a keyboard-scrollable focus stop. */}
      <Box
        sx={{
          ...visuallyHiddenSx,
          '& .MuiAlert-message': { overflow: 'hidden' },
        }}
      >
        <Alert
          severity="success"
          role="status"
          aria-label={t('home.notice.region')}
        >
          {message}
        </Alert>
      </Box>
    </AppNoticeContext.Provider>
  )
}

/**
 * Mounts the provider above the shell switch and reports the acting principal
 * to it. The anonymous state has no principal, so it carries no notice either.
 */
export function AppNoticeHost({
  children,
}: {
  readonly children: ReactNode
}): ReactElement {
  const snapshot = useAuthState()
  const principal =
    snapshot.state === 'authenticated'
      ? (snapshot.principal?.account_id ?? '')
      : ''
  return <AppNoticeProvider principal={principal}>{children}</AppNoticeProvider>
}

/** The notice of the app view this component renders inside. */
export function useAppNotice(): AppNotice {
  return useContext(AppNoticeContext)
}
