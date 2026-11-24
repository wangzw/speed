/**
 * @speed/i18n -- the platform i18n layer for web packages: one
 * react-i18next instance per host, per-package namespaces with parity
 * validation, and a missing-key discipline with no cross-language fallback.
 */

export { createI18n, switchLanguage } from './create.js'
export { registerNamespace } from './register.js'
export { overlayResources, type ResourceOverlay } from './overlay.js'
export {
  CLIENT_TRANSPORT_ERROR_CODES,
  SESSION_LIFECYCLE_ERROR_CODES,
  WAIT_LONG_FROM_SECONDS,
  createErrorTextResolver,
  waitCountdownText,
  type ErrorTextResolverOptions,
  type ErrorTextTranslate,
  type ErrorTextTranslateOptions,
  type HttpFamilyOptions,
} from './error-text.js'
export {
  formatInstant,
  isSupportedTimeZone,
  resolveTimeZone,
  type FormatInstantOptions,
} from './time.js'
export {
  instantToWallClock,
  wallClockToInstant,
  type WallClock,
  type WallClockToInstantOptions,
} from './wall-clock.js'
export { languageDisplayName } from './language-name.js'
export {
  DEFAULT_LANGUAGE,
  DEFAULT_SUPPORTED_LANGUAGES,
  normalizeLanguageTag,
  matchSupportedLanguage,
  readSupportedLanguages,
} from './languages.js'
export { SPEED_LOCALE_STORAGE_KEY, type StorageLike } from './storage.js'
export {
  defaultMissingKeyHandler,
  type MissingKeyDetails,
} from './missing-key.js'
export type { CreateI18nOptions } from './create.js'
export type { ResourceBundle } from './register.js'
export type { i18n as I18nInstance } from 'i18next'
export { I18nextProvider, useTranslation } from 'react-i18next'
export type { UseTranslationResponse } from 'react-i18next'
