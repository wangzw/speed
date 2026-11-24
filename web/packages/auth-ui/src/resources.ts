/** The auth-ui translation bundle and its bare namespace. */

import type { ResourceBundle } from '@speed/i18n'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }

/** The bare namespace auth-ui translations register under: "auth-ui". */
export const AUTH_UI_NAMESPACE = 'auth-ui' as const

/** The auth-ui resource bundle: one bundle per supported language. */
export const authUiResources: Readonly<Record<string, ResourceBundle>> = {
  'zh-CN': zhCN as ResourceBundle,
  'en-US': enUS as ResourceBundle,
}
