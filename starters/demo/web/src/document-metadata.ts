/**
 * Keeps the entry document's title in step with the active language.
 *
 * The served index.html carries a static title -- the build-time brand -- but
 * the interface renders in the language the instance negotiates at startup (a
 * URL parameter, a stored choice, the browser's own languages), and a title
 * that stayed the placeholder would name nothing in either. The bootstrap
 * calls this once with the resolved instance, and it follows every later
 * switch; the value comes from the demo namespace, so it stays bilingual with
 * the rest of the app.
 *
 * `<html lang>` is not this module's: the app-entry assembly binds it at boot
 * and keeps it in step with the instance (SpeedAppDefinition's
 * `documentLanguage`), for every host built on it. Keeping a second binding
 * here would just duplicate the assembly's.
 */

import type { I18nInstance } from '@speed/i18n'
import { DEMO_NAMESPACE } from './resources.js'

/**
 * Applies the instance's current language to the document title, and keeps
 * following it: the title resolves from the demo namespace's `app.title`, so
 * a language switch relabels the tab the same way it relabels the header.
 */
export function applyDocumentMetadata(i18n: I18nInstance): void {
  const apply = (): void => {
    document.title = i18n.t('app.title', { ns: DEMO_NAMESPACE })
  }
  apply()
  i18n.on('languageChanged', apply)
}
