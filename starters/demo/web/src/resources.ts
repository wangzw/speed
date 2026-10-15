/**
 * The demo application's own translation bundle.
 *
 * Every string the app renders itself -- the product title, the business
 * slot page, the fallback page -- resolves from this namespace. The packages
 * the app composes ship their own bundles; the app-entry assembly registers
 * the four family namespaces automatically, and the app adds this one beside
 * the console-ui namespace its console area renders from
 * (src/main.tsx's namespaces list).
 *
 * Both language files carry the same leaf key set (registration enforces it),
 * and the JSON import attribute keeps the bundler's JSON handling explicit.
 */

import type { ResourceBundle } from '@speed/i18n'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }

/** The bare namespace the app's own translations register under: "demo". */
export const DEMO_NAMESPACE = 'demo' as const

/** The demo resource bundle: one bundle per supported language. */
export const demoResources: Readonly<Record<string, ResourceBundle>> = {
  'zh-CN': zhCN as ResourceBundle,
  'en-US': enUS as ResourceBundle,
}
