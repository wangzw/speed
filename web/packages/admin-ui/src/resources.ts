/**
 * The admin-ui translation bundle and its namespace. The host registers it once
 * after createI18n (double registration throws); the two language files carry
 * identical leaf key sets, which registration enforces.
 */

import type { ResourceBundle } from '@speed/i18n'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }

/** The bare namespace admin-ui translations register under: "admin-ui". */
export const ADMIN_UI_NAMESPACE = 'admin-ui' as const

/** The admin-ui resource bundle: one bundle per supported language. */
export const adminUiResources: Readonly<Record<string, ResourceBundle>> = {
  'zh-CN': zhCN as ResourceBundle,
  'en-US': enUS as ResourceBundle,
}
