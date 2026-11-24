import { isTenantAdmissionRefusal } from '@speed/auth-core'
import {
  CLIENT_TRANSPORT_ERROR_CODES,
  SESSION_LIFECYCLE_ERROR_CODES,
  createErrorTextResolver,
} from '@speed/i18n'
import { useNotificationUiTranslation } from './translation.js'

export const ERROR_TEXT_CODES = [
  'notification.bad_request',
  'notification.message_not_found',
  'notification.not_unsubscribable',
  ...SESSION_LIFECYCLE_ERROR_CODES,
  ...CLIENT_TRANSPORT_ERROR_CODES,
] as const

const KNOWN_CODES = new Set<string>(ERROR_TEXT_CODES)

function isTenantRefusalCode(code: string): boolean {
  return isTenantAdmissionRefusal({ code })
}

export function useNotificationUiErrorText(): (code: string) => string {
  const { t } = useNotificationUiTranslation()
  const resolveKnown = createErrorTextResolver(t, KNOWN_CODES)
  return (code) =>
    isTenantRefusalCode(code)
      ? t('errors.tenantUnavailable')
      : resolveKnown(code)
}
