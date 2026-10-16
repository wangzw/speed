/**
 * The objectstore-ui translation bundle and its bare namespace.
 *
 * The host registers this namespace exactly once on its i18n instance,
 * right after createI18n (double registration throws, so it must never
 * happen inside a component or provider):
 *
 *   registerNamespace(i18n, OBJECTSTORE_UI_NAMESPACE, objectstoreUiResources)
 *
 * Every user-facing string the object page renders by itself lives here,
 * in zh-CN and en-US with identical leaf key sets (registration
 * enforces that): the page and list surface (its columns, its cursor
 * paging controls, its empty and error and no-permission copy); the
 * upload surface; the delete confirmation; the details dialog; the
 * object-state labels; and the errors section the code-level text
 * resolves through. The two language files are imported with the JSON
 * import attribute so the NodeNext build keeps working; tsc copies them
 * into dist/ and the published package ships them through the "."
 * entry, never as separate locale subpaths.
 */

import type { ResourceBundle } from '@speed/i18n'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }

/** The bare namespace objectstore-ui translations register under: "objectstore-ui". */
export const OBJECTSTORE_UI_NAMESPACE = 'objectstore-ui' as const

/** The objectstore-ui resource bundle: one bundle per supported language. */
export const objectstoreUiResources: Readonly<Record<string, ResourceBundle>> = {
  'zh-CN': zhCN as ResourceBundle,
  'en-US': enUS as ResourceBundle,
}
