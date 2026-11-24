/** MUI locale linkage: a canonical language tag to the MUI localization object; isolated so the main entry never imports @mui/material. */

import { zhCN, enUS, type Localization } from '@mui/material/locale'

/** The MUI localization object type (re-exported for host convenience). */
export type MuiLocale = Localization

/** The MUI localization for a canonical language tag; throws on an unknown tag. */
export function muiLocaleFor(language: string): MuiLocale {
  switch (language) {
    case 'zh-CN':
      return zhCN
    case 'en-US':
      return enUS
    default:
      throw new Error(
        `[speed-i18n] no MUI localization for language "${language}"; ` +
          'supported: zh-CN, en-US. Add the mapping here when the platform ' +
          'starts shipping another language.',
      )
  }
}
