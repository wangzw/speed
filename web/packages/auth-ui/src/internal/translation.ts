/** The auth-ui namespace translation hook, bound once so components do not
 * repeat the namespace string. */

import { useTranslation } from '@speed/i18n'
import { AUTH_UI_NAMESPACE } from '../resources.js'

export function useAuthUiTranslation() {
  return useTranslation(AUTH_UI_NAMESPACE)
}
