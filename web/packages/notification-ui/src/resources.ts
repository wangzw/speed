/**
 * The notification-ui translation bundle and its bare namespace.
 */

import type { ResourceBundle } from '@speed/i18n'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }

/** The bare namespace notification-ui translations register under. */
export const NOTIFICATION_UI_NAMESPACE = 'notification-ui' as const

/** The notification-ui resource bundle: one bundle per supported
 * language. */
export const notificationUiResources: Readonly<
  Record<string, ResourceBundle>
> = {
  'zh-CN': zhCN as ResourceBundle,
  'en-US': enUS as ResourceBundle,
}
