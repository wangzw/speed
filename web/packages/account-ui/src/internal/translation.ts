/**
 * The account-ui namespace translation hook, bound once so components do not
 * repeat the namespace string.
 */

import { useTranslation } from '@speed/i18n'
import { ACCOUNT_UI_NAMESPACE } from '../resources.js'

export function useAccountUiTranslation() {
  return useTranslation(ACCOUNT_UI_NAMESPACE)
}
