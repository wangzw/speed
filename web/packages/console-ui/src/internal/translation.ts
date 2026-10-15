/**
 * The console-ui namespace translation hook, bound once so the shell and
 * the administration pages do not repeat the namespace string.
 *
 * Components take their text from this namespace's bundles and never
 * hardcode user-facing strings; hosts override wording through the
 * namespace registration, not through component props. The surfaces
 * compose ui-kit components whose built-in strings speak ui-kit-namespace
 * keys, which ui-kit resolves at render time -- a host rendering
 * console-ui registers both namespaces.
 */

import { useTranslation } from '@speed/i18n'
import { CONSOLE_UI_NAMESPACE } from '../resources.js'

/** The translation response (t plus re-render binding) for the console-ui namespace. */
export function useConsoleUiTranslation() {
  return useTranslation(CONSOLE_UI_NAMESPACE)
}
