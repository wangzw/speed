/**
 * The org-create-ui translation bundle and its bare namespace.
 *
 * The host registers this namespace exactly once on its i18n instance,
 * right after createI18n (double registration throws, so it must never
 * happen inside a component or provider):
 *
 *   registerNamespace(i18n, ORG_CREATE_UI_NAMESPACE, orgCreateUiResources)
 *
 * Every built-in string the shared creation form and the self-service
 * creation page render lives here, in zh-CN and en-US with identical leaf
 * key sets (registration enforces that): the fields and their helpers,
 * the client-side validation messages, the page copy, the switch-to-the-
 * new-organization state, and the errors section the code-level text
 * resolves through.
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
