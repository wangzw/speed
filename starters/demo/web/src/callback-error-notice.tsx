import { useState } from 'react'
import type { ReactElement } from 'react'
import Alert from '@mui/material/Alert'
import { useTranslation } from '@speed/i18n'
import { useCallbackLanding } from './use-callback-landing.js'
import { DEMO_NAMESPACE } from './resources.js'

function useCallbackErrorText(namespace: string, code: string): string {
  const { t, i18n } = useTranslation(namespace)
  const key = `errors.${code}`
  return i18n.exists(key, { ns: namespace })
    ? String(t(key))
    : String(t('errors.unknown'))
}

export interface CallbackErrorAlertProps {
  readonly code: string
  readonly namespace: string
  readonly onClose: () => void
}

export function CallbackErrorAlert({
  code,
  namespace,
  onClose,
}: CallbackErrorAlertProps): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const text = useCallbackErrorText(namespace, code)
  return (
    <Alert
      severity="error"
      onClose={onClose}
      closeText={t('callbackNotice.dismiss')}
      sx={{ width: '100%' }}
    >
      {text}
    </Alert>
  )
}

export interface CallbackLandingNoticeProps {
  readonly namespace: string
}

export function CallbackLandingNotice({
  namespace,
}: CallbackLandingNoticeProps): ReactElement | null {
  const { landing } = useCallbackLanding()
  const [dismissed, setDismissed] = useState(false)
  if (landing === undefined || dismissed) {
    return null
  }
  return (
    <CallbackErrorAlert
      code={landing.code}
      namespace={namespace}
      onClose={() => setDismissed(true)}
    />
  )
}
