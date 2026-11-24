import { useTranslation } from '@speed/i18n'
import { NOTIFICATION_UI_NAMESPACE } from '../resources.js'

export function useNotificationUiTranslation() {
  return useTranslation(NOTIFICATION_UI_NAMESPACE)
}
