/**
 * The tenancy-ui translation bundle and its bare namespace.
 *
 * The host registers this namespace exactly once on its i18n instance,
 * right after createI18n (double registration throws, so it must never
 * happen inside a component or provider):
 *
 *   registerNamespace(i18n, TENANCY_UI_NAMESPACE, tenancyUiResources)
 *
 * Every user-facing string TenantSwitcher renders by itself lives here, in
 * zh-CN and en-US with identical leaf key sets (registration enforces
 * that). The two language files are imported with the JSON import
 * attribute so the NodeNext build keeps working; tsc copies them into
 * dist/ and the published package ships them through the "." entry, never
 * as separate locale subpaths.
 *
 * The bundle covers only the control's own states -- the trigger's
 * noCurrentTenant label and the switchedTo announcement -- because the
 * switch itself is the host's: the host's route change carries every
 * failure (no membership, a failed token check, an unreachable route) and
 * renders it in the host's own data layer, so this package carries no
 * error table. The trigger label and the list rows are host text
 * (TenantOption.name), rendered verbatim and never translated.
 */

import type { ResourceBundle } from '@speed/i18n'
import enUS from './locales/en-US.json' with { type: 'json' }
import zhCN from './locales/zh-CN.json' with { type: 'json' }

/** The bare namespace tenancy-ui translations register under: "tenancy-ui". */
export const TENANCY_UI_NAMESPACE = 'tenancy-ui' as const

/** The tenancy-ui resource bundle: one bundle per supported language. */
export const tenancyUiResources: Readonly<Record<string, ResourceBundle>> = {
  'zh-CN': zhCN as ResourceBundle,
  'en-US': enUS as ResourceBundle,
}
