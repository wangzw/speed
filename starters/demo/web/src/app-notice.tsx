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

export interface AppNotice {
  readonly visible: boolean
  readonly message: string
  announce(message: string): void
  dismiss(): void
}

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

export function AppNoticeProvider({
  principal,
  children,
}: {
  readonly principal: string
  readonly children: ReactNode
}): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const [announcement, setAnnouncement] = useState<Announcement | null>(null)
  const [dismissed, setDismissed] = useState(false)
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

export function useAppNotice(): AppNotice {
  return useContext(AppNoticeContext)
}
