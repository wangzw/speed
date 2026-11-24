/**
 * Instance creation and the language switch. createI18n negotiates the start
 * language and pins the i18next options that make the missing-key discipline
 * hold.
 */

import { createInstance, type i18n as I18nInstance } from 'i18next'
import { initReactI18next } from 'react-i18next'
import {
  DEFAULT_LANGUAGE,
  DEFAULT_SUPPORTED_LANGUAGES,
  detectLanguage,
  matchSupportedLanguage,
  normalizeLanguageTag,
  readSupportedLanguages,
} from './languages.js'
import {
  SPEED_LOCALE_STORAGE_KEY,
  bindInstanceStorage,
  boundInstanceStorage,
  type StorageLike,
} from './storage.js'
import { missingKeyHandlerFactory, type MissingKeyDetails } from './missing-key.js'

/** Customize the language the instance starts with, or how it is decided. */
export interface CreateI18nOptions {
  /**
   * Canonical tags the instance may speak; defaults to zh-CN and en-US. Each entry
   * must already be canonical, and a stored preference outside this set is refused
   * with 400 authn.invalid_locale.
   */
  readonly supportedLanguages?: readonly string[]
  /** Negotiation fallback when every source misses; must be a member of supportedLanguages. */
  readonly defaultLanguage?: string
  /** URL parameter carrying a language override; defaults to "lang". Pass null or an empty string to disable it. */
  readonly urlParameterName?: string | null
  /** Injected URL parameters (tests, non-browser hosts). */
  readonly searchParams?: URLSearchParams | null
  /** Storage the chosen language persists to; defaults to the guarded browser localStorage, and null runs without persistence. */
  readonly storage?: StorageLike | null
  /** Override the key the choice is persisted under. Defaults to SPEED_LOCALE_STORAGE_KEY. */
  readonly storageKey?: string
  /**
   * Signed-in user's profile locale, resolved by the host before creation.
   * Apply a profile that resolves later through `switchLanguage(instance,
   * locale, null)`: the persisting form would shadow the profile tier.
   */
  readonly profileLanguage?: string | null
  /** Browser language preferences; defaults to the guarded navigator languages. */
  readonly navigatorLanguages?: readonly string[]
  /** Called with details whenever a translation key is missing; defaults to the console warning. */
  readonly onMissingKey?: (details: MissingKeyDetails) => void
  /**
   * Namespaces i18next consults when a lookup's namespace lacks the key. This
   * is namespace fallback, never language fallback; undefined leaves the
   * option unset.
   */
  readonly fallbackNamespaces?: readonly string[]
}

function readUrlLanguage(
  parameterName: string,
  injected: URLSearchParams | null | undefined,
): string | null {
  let params = injected
  if (params === undefined) {
    const globalWithLocation = globalThis as { location?: { search?: string } }
    if (typeof globalWithLocation.location?.search === 'string') {
      try {
        params = new URLSearchParams(globalWithLocation.location.search)
      } catch {
        params = null
      }
    } else {
      params = null
    }
  }
  if (params === null) {
    return null
  }
  const value = params.get(parameterName)
  return value === null || value === '' ? null : value
}

function readGlobalLocalStorage(): StorageLike | null {
  const globalWithStorage = globalThis as { localStorage?: unknown }
  let candidate: unknown
  try {
    candidate = globalWithStorage.localStorage
  } catch {
    return null
  }
  if (typeof candidate !== 'object' || candidate === null) {
    return null
  }
  const storage = candidate as Partial<StorageLike>
  if (typeof storage.getItem === 'function' && typeof storage.setItem === 'function') {
    return storage as StorageLike
  }
  return null
}

function readStoredLanguage(storage: StorageLike, key: string): string | null {
  try {
    return storage.getItem(key)
  } catch {
    return null
  }
}

function readGlobalNavigatorLanguages(): readonly string[] {
  const globalWithNavigator = globalThis as { navigator?: { languages?: unknown } }
  const raw = globalWithNavigator.navigator?.languages
  if (!Array.isArray(raw)) {
    return []
  }
  return raw.filter((entry): entry is string => typeof entry === 'string')
}

/** Creates the host i18n instance: negotiates the start language, pins the discipline options, binds persistence. */
export function createI18n(options: CreateI18nOptions = {}): I18nInstance {
  const supportedLanguages = options.supportedLanguages ?? DEFAULT_SUPPORTED_LANGUAGES
  if (supportedLanguages.length === 0) {
    throw new Error(
      '[speed-i18n] createI18n requires at least one supported language; ' +
        'supportedLanguages must not be empty.',
    )
  }
  for (const language of supportedLanguages) {
    const canonical = normalizeLanguageTag(language)
    if (canonical === null) {
      throw new Error(
        `[speed-i18n] createI18n: supported language "${language}" is not a valid ` +
          'language tag; the supported set must list canonical tags such as ' +
          '"zh-CN", one per language the instance may speak.',
      )
    }
    if (canonical !== language) {
      throw new Error(
        `[speed-i18n] createI18n: supported language "${language}" is not in ` +
          `canonical form; use "${canonical}". Every negotiation source is ` +
          'canonicalized before it is matched, so a non-canonical entry (an ' +
          'underscore, stray whitespace or a malformed subtag) could never be ' +
          'reached and would be dead weight in the set.',
      )
    }
  }
  const defaultLanguage = options.defaultLanguage ?? DEFAULT_LANGUAGE
  const defaultMatched = matchSupportedLanguage(defaultLanguage, supportedLanguages)
  if (defaultMatched === null) {
    throw new Error(
      `[speed-i18n] defaultLanguage "${defaultLanguage}" is not among the supported ` +
        `languages [${supportedLanguages.join(', ')}]; the negotiation fallback must be a ` +
        'member of the supported set.',
    )
  }
  const storage =
    options.storage === undefined ? readGlobalLocalStorage() : options.storage
  const storageKey = options.storageKey ?? SPEED_LOCALE_STORAGE_KEY
  const storedLanguage =
    storage === null ? null : readStoredLanguage(storage, storageKey)

  // null/empty opts the URL source out; coalescing onto "lang" would re-enable it, and a raw null would honor a literal ?null=.
  const urlLanguage =
    options.urlParameterName === null || options.urlParameterName === ''
      ? null
      : readUrlLanguage(options.urlParameterName ?? 'lang', options.searchParams)

  const detected = detectLanguage({
    supportedLanguages,
    defaultLanguage: defaultMatched,
    urlLanguage,
    storedLanguage,
    profileLanguage: options.profileLanguage ?? null,
    navigatorLanguages: options.navigatorLanguages ?? readGlobalNavigatorLanguages(),
  })

  const instance = createInstance()
  instance.use(initReactI18next)
  // saveMissing gates i18next v26's missingKeyHandler dispatch; this package registers in-memory resources only.
  void instance.init({
    lng: detected,
    supportedLngs: [...supportedLanguages],
    fallbackLng: false,
    load: 'currentOnly',
    saveMissing: true,
    missingKeyHandler: missingKeyHandlerFactory(options.onMissingKey),
    initAsync: false,
    // React escapes a translated text node itself; escaping again would show the entities to the user.
    interpolation: { escapeValue: false },
    ...(options.fallbackNamespaces === undefined
      ? {}
      : { fallbackNS: [...options.fallbackNamespaces] }),
  })
  bindInstanceStorage(instance, storage, storageKey)
  return instance
}

/**
 * Persists the choice from the manual override UI; the target must be supported
 * and the write is best-effort (a failure only warns). `storage`: undefined uses
 * the creation binding, null persists nothing, a StorageLike persists there.
 */
export async function switchLanguage(
  instance: I18nInstance,
  language: string,
  storage?: StorageLike | null,
): Promise<void> {
  const supported = readSupportedLanguages(instance)
  if (supported.length === 0) {
    throw new Error(
      '[speed-i18n] switchLanguage needs the instance to pin a supported-language set; ' +
        'create the instance with createI18n (bare i18next instances are refused).',
    )
  }
  const target = matchSupportedLanguage(language, supported)
  if (target === null) {
    throw new Error(
      `[speed-i18n] cannot switch to "${language}": it is not among the supported ` +
        `languages [${supported.join(', ')}].`,
    )
  }
  const binding = boundInstanceStorage(instance)
  const effectiveStorage =
    storage === undefined ? (binding?.storage ?? null) : storage
  if (effectiveStorage !== null) {
    persistChoice(effectiveStorage, binding?.key ?? SPEED_LOCALE_STORAGE_KEY, target)
  }
  await instance.changeLanguage(target)
  // Reported after the switch settles: the warning is only true if the switch happened.
  if (effectiveStorage === null && storage === undefined && binding === null) {
    warnUnboundStorage(target)
  }
}

function persistChoice(storage: StorageLike, key: string, language: string): void {
  try {
    storage.setItem(key, language)
  } catch {
    console.warn(
      `[speed-i18n] could not persist the language choice "${language}" under key ` +
        `"${key}"; the instance still switched, but the choice will not survive a ` +
        'reload. Storage failures never fail a language switch.',
    )
  }
}

function warnUnboundStorage(language: string): void {
  console.warn(
    `[speed-i18n] could not persist the language choice "${language}": the instance ` +
      'carries no storage binding. Instances created by createI18n bind one (pass ' +
      'storage: null at creation to opt out of persistence); the instance still ' +
      'switched, but the choice will not survive a reload.',
  )
}
