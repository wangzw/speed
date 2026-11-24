/** The ui-kit translation bundle and its bare namespace, registered once at host bootstrap. */

import type { ResourceBundle } from '@speed/i18n'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }

/** The bare namespace ui-kit translations register under: "ui-kit". */
export const UI_KIT_NAMESPACE = 'ui-kit' as const

/** The ui-kit resource bundle: one bundle per supported language. */
export const uiKitResources: Readonly<Record<string, ResourceBundle>> = {
  'zh-CN': zhCN as ResourceBundle,
  'en-US': enUS as ResourceBundle,
}
