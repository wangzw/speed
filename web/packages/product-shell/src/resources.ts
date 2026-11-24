/**
 * The product-shell translation bundle and its bare namespace.
 */

import type { ResourceBundle } from '@speed/i18n'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }

/** The bare namespace product-shell translations register under. */
export const PRODUCT_SHELL_NAMESPACE = 'product-shell' as const

/** The product-shell resource bundle: one bundle per supported language. */
export const productShellResources: Readonly<Record<string, ResourceBundle>> = {
  'zh-CN': zhCN as ResourceBundle,
  'en-US': enUS as ResourceBundle,
}
