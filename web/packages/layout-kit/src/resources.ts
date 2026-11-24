import type { ResourceBundle } from '@speed/i18n'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }

/** The bare namespace layout-kit translations register under: "layout-kit". */
export const LAYOUT_KIT_NAMESPACE = 'layout-kit' as const

/** The layout-kit resource bundle: one bundle per supported language. */
export const layoutKitResources: Readonly<Record<string, ResourceBundle>> = {
  'zh-CN': zhCN as ResourceBundle,
  'en-US': enUS as ResourceBundle,
}
