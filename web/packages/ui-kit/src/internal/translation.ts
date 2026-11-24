/** The ui-kit namespace translation hook, bound once so components do not repeat the namespace string. */

import { useTranslation } from '@speed/i18n'
import { UI_KIT_NAMESPACE } from '../resources.js'

/** The translation response (t plus re-render binding) for the ui-kit namespace. */
export function useUiKitTranslation() {
  return useTranslation(UI_KIT_NAMESPACE)
}
