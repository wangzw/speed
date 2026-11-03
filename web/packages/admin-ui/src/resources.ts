/**
 * The admin-ui translation bundle and its bare namespace.
 *
 * The host registers this namespace exactly once on its i18n instance,
 * right after createI18n (double registration throws, so it must never
 * happen inside a component or provider):
 *
 *   registerNamespace(i18n, ADMIN_UI_NAMESPACE, adminUiResources)
 *
 * Every user-facing string the console shell and the administration pages
 * render by themselves lives here, in zh-CN and en-US with
 * identical leaf key sets (registration enforces that): the shell's
 * landmark labels and skip link; the tenant roster surface (its list,
 * its create and rename controls, its status labels); the settings
 * surface (its namespace filter, its value editor and its remove
 * control); the audit surface; and the errors section the code-level
 * text resolves through. The two language files are imported with the
 * JSON import attribute so the NodeNext build keeps working; tsc copies
 * them into dist/ and the published package ships them through the "."
 * entry, never as separate locale subpaths.
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
