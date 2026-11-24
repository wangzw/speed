/**
 * The tenancy-ui translation bundle and its bare namespace.
 */

import type { ResourceBundle } from '@speed/i18n'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }

/** The bare namespace tenancy-ui translations register under. */
export const TENANCY_UI_NAMESPACE = 'tenancy-ui' as const

/** The tenancy-ui resource bundle: one bundle per supported language. */
export const tenancyUiResources: Readonly<Record<string, ResourceBundle>> = {
  'zh-CN': zhCN as ResourceBundle,
  'en-US': enUS as ResourceBundle,
}
