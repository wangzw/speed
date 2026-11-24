/**
 * The account-ui translation bundle and its bare namespace.
 */

import type { ResourceBundle } from '@speed/i18n'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }

/** The bare namespace account-ui translations register under: "account-ui". */
export const ACCOUNT_UI_NAMESPACE = 'account-ui' as const

/** The account-ui resource bundle: one bundle per supported language. */
export const accountUiResources: Readonly<Record<string, ResourceBundle>> = {
  'zh-CN': zhCN as ResourceBundle,
  'en-US': enUS as ResourceBundle,
}
