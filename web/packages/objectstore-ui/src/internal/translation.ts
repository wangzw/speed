/**
 * The objectstore-ui namespace translation hook, bound once so the
 * surfaces do not repeat the namespace string.
 *
 * Components take their text from this namespace's bundles and never
 * hardcode user-facing strings; hosts override wording through the
 * namespace registration, not through component props. The page composes
 * ui-kit components whose built-in strings speak ui-kit-namespace keys,
 * which ui-kit resolves at render time -- a host rendering objectstore-ui
 * registers both namespaces.
 */

import { useTranslation } from '@speed/i18n'
import { OBJECTSTORE_UI_NAMESPACE } from '../resources.js'

/** The translation response (t plus re-render binding) for the objectstore-ui namespace. */
export function useObjectstoreUiTranslation() {
  return useTranslation(OBJECTSTORE_UI_NAMESPACE)
}
