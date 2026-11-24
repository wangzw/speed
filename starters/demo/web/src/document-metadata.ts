import type { I18nInstance } from '@speed/i18n'
import { DEMO_NAMESPACE } from './resources.js'

export function applyDocumentMetadata(i18n: I18nInstance): void {
  const apply = (): void => {
    document.title = i18n.t('app.title', { ns: DEMO_NAMESPACE })
  }
  apply()
  i18n.on('languageChanged', apply)
}
