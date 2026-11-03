/**
 * The callback-failure notice: a refused redirect round trip lands back in
 * the product with its problem code in the URL fragment. The surface that
 * started the flow reads the code and renders it here, so the refusal is
 * never silently dropped.
 *
 * The code resolves through the namespace of the surface that renders the
 * notice -- the account surface's vocabulary for the tenant area, the
 * admin's for the admin area -- and a code outside that namespace falls
 * back to its generic unknown text rather than to a raw key. The notice is
 * dismissible and one-shot: it stands until closed, and the fragment it came
 * from is cleared by the reader, so a reload does not repeat it.
 */

import { useState } from 'react'
import type { ReactElement } from 'react'
import Alert from '@mui/material/Alert'
import { useTranslation } from '@speed/i18n'
import { useCallbackLanding } from './use-callback-landing.js'
import { DEMO_NAMESPACE } from './resources.js'

/** The text a namespace resolves for the callback's problem code. */
function useCallbackErrorText(namespace: string, code: string): string {
  const { t, i18n } = useTranslation(namespace)
  const key = `errors.${code}`
  return i18n.exists(key, { ns: namespace })
    ? String(t(key))
    : String(t('errors.unknown'))
}

export interface CallbackErrorAlertProps {
  /** The problem code the fragment carried (`authn.login_failed`). */
  readonly code: string
  /** The namespace whose error vocabulary words the code. */
  readonly namespace: string
  /** Closes the notice; a one-shot notice is gone until another refusal. */
  readonly onClose: () => void
}

/** One refused round trip's problem code, rendered as its namespace's text. */
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
  /** The namespace whose error vocabulary words the code. */
  readonly namespace: string
}

/**
 * The landing notice of a surface that consumes the fragment itself: the
 * surface read the code on its first render (and clears it after mount), so
 * this notice needs no second read. Renders nothing when the surface
 * landed without a refusal, and nothing after the viewer closes it.
 */
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
