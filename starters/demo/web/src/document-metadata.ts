/**
 * Keeps the entry document's language and title in step with the active
 * language.
 *
 * The served index.html carries a static default -- a valid language tag and
 * the brand -- but the interface renders in the language the instance
 * negotiates at startup (a URL parameter, a stored choice, the browser's own
 * languages). A document that kept announcing a fixed language would
 * misdescribe its own content (WCAG 3.1.1), and a title that stayed the
 * build-time placeholder would name nothing. The bootstrap calls this once
 * with the resolved instance, and it follows every later switch; both values
 * come from the demo namespace, so they stay bilingual with the rest of the
 * app.
 */

import type { I18nInstance } from '@speed/i18n'
import { DEMO_NAMESPACE } from './resources.js'

/**
 * Applies the instance's current language to `<html lang>` and the document
 * title, and keeps following it: the title resolves from the demo namespace's
 * `app.title`, so a language switch relabels the tab the same way it relabels
 * the header.
 */
export function applyDocumentMetadata(i18n: I18nInstance): void {
  const apply = (): void => {
    document.documentElement.lang = i18n.language
    document.title = i18n.t('app.title', { ns: DEMO_NAMESPACE })
  }
  apply()
  i18n.on('languageChanged', apply)
}
