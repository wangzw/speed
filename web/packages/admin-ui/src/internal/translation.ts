/**
 * The admin-ui namespace translation hook, bound once so the shell and
 * the administration pages do not repeat the namespace string.
 *
 * Components take their text from this namespace's bundles and never
 * hardcode user-facing strings; hosts override wording through the
 * namespace registration, not through component props. The surfaces
 * compose ui-kit components whose built-in strings speak ui-kit-namespace
 * keys, which ui-kit resolves at render time -- a host rendering
 * admin-ui registers both namespaces.
 */

import { useTranslation } from '@speed/i18n'
import { UI_KIT_NAMESPACE } from '@speed/ui-kit'
import { ADMIN_UI_NAMESPACE } from '../resources.js'

/** The translation response (t plus re-render binding) for the admin-ui namespace. */
export function useAdminUiTranslation() {
  return useTranslation(ADMIN_UI_NAMESPACE)
}

/**
 * The ui-kit namespace's translation response, for a console page that
 * renders a ui-kit-owned control's wording itself. It exists for one
 * shape: a page placing a ui-kit control outside the ui-kit component
 * that owns it -- the account list's paging footer, which must outlive
 * the async section's branches -- still speaks the ui-kit namespace, so
 * the host overrides that wording where it registers ui-kit's resources
 * rather than through a second copy of the keys.
 */
export function useUiKitTranslation() {
  return useTranslation(UI_KIT_NAMESPACE)
}
