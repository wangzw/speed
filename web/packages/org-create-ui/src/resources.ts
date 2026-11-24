/**
 * The org-create-ui translation bundle and its bare namespace.
 */

import type { ResourceBundle } from '@speed/i18n'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }

/** The bare namespace org-create-ui translations register under. */
export const ORG_CREATE_UI_NAMESPACE = 'org-create-ui' as const

/** The org-create-ui resource bundle: one bundle per supported language. */
export const orgCreateUiResources: Readonly<Record<string, ResourceBundle>> = {
  'zh-CN': zhCN as ResourceBundle,
  'en-US': enUS as ResourceBundle,
}
