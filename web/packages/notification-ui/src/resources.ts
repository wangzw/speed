/**
 * The notification-ui translation bundle and its bare namespace.
 *
 * The host registers this namespace exactly once on its i18n instance,
 * right after createI18n (double registration throws, so it must never
 * happen inside a component or provider):
 *
 *   registerNamespace(i18n, NOTIFICATION_UI_NAMESPACE, notificationUiResources)
 *
 * Every user-facing string any notification-ui component renders by
 * itself lives here, in zh-CN and en-US with identical leaf key sets
 * (registration enforces that): the errors section -- the code-level
 * text the error resolver and InlineError render -- the bell badge
 * labels, the inbox surface keys, and the preference matrix keys. The
 * two language files are imported with the JSON import attribute so the
 * NodeNext build keeps working; tsc copies them into dist/ and the
 * published package ships them through the "." entry, never as separate
 * locale subpaths.
 */

import type { ResourceBundle } from '@speed/i18n'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }

/** The bare namespace notification-ui translations register under:
 * "notification-ui". */
export const NOTIFICATION_UI_NAMESPACE = 'notification-ui' as const

/** The notification-ui resource bundle: one bundle per supported
 * language. */
export const notificationUiResources: Readonly<
  Record<string, ResourceBundle>
> = {
  'zh-CN': zhCN as ResourceBundle,
  'en-US': enUS as ResourceBundle,
}
