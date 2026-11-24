/** The admin-ui namespace translation hooks. */

import { useTranslation } from '@speed/i18n'
import { UI_KIT_NAMESPACE } from '@speed/ui-kit'
import { ADMIN_UI_NAMESPACE } from '../resources.js'

/** The translation response (t plus re-render binding) for the admin-ui namespace. */
export function useAdminUiTranslation() {
  return useTranslation(ADMIN_UI_NAMESPACE)
}

/**
 * The ui-kit namespace's translation response, for a page that renders a
 * ui-kit-owned control's wording itself, such as the account list's paging
 * footer outside the async section.
 */
export function useUiKitTranslation() {
  return useTranslation(UI_KIT_NAMESPACE)
}
