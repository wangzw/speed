/**
 * Canonical language tags and the negotiation chain (URL parameter, stored
 * choice, profile locale, navigator, default). Matching is exact, then
 * bidirectional subtag relaxation that never crosses languages.
 */

/** The canonical language tags the platform ships. */
export const DEFAULT_SUPPORTED_LANGUAGES = ['zh-CN', 'en-US'] as const

/** Negotiation default: an unknown language resolves to en-US. */
export const DEFAULT_LANGUAGE = 'en-US'

const TAG_PATTERN = /^[A-Za-z]{2,3}(?:[-_][A-Za-z0-9]{2,8})*$/

/** Canonicalizes a raw language string, or null when it is not a usable tag; underscores become hyphens. */
export function normalizeLanguageTag(input: string): string | null {
  const trimmed = input.trim()
  if (!TAG_PATTERN.test(trimmed)) {
    return null
  }
  return trimmed.replaceAll('_', '-')
}

/**
 * Matches a candidate against the supported set, or null; exact canonical
 * first, then two-directional language-subtag relaxation that never crosses
 * languages.
 */
export function matchSupportedLanguage(
  candidate: string,
  supported: readonly string[],
): string | null {
  const normalized = normalizeLanguageTag(candidate)
  if (normalized === null) {
    return null
  }
  const lower = normalized.toLowerCase()
  for (const language of supported) {
    if (language.toLowerCase() === lower) {
      return language
    }
  }
  const primary = lower.split('-')[0] ?? lower
  if (lower.includes('-')) {
    for (const language of supported) {
      if (language.toLowerCase() === primary) {
        return language
      }
    }
  }
  const matches = supported.filter((language) =>
    language.toLowerCase().startsWith(`${primary}-`),
  )
  return matches.length === 1 ? matches[0]! : null
}

export interface DetectLanguageOptions {
  readonly supportedLanguages: readonly string[]
  readonly defaultLanguage: string
  readonly urlLanguage?: string | null
  readonly storedLanguage?: string | null
  readonly profileLanguage?: string | null
  readonly navigatorLanguages?: readonly string[]
}

export function detectLanguage(options: DetectLanguageOptions): string {
  const { supportedLanguages, defaultLanguage } = options
  const candidates = [
    options.urlLanguage,
    options.storedLanguage,
    options.profileLanguage,
    ...(options.navigatorLanguages ?? []),
  ]
  for (const candidate of candidates) {
    if (candidate === null || candidate === undefined) {
      continue
    }
    const matched = matchSupportedLanguage(candidate, supportedLanguages)
    if (matched !== null) {
      return matched
    }
  }
  return defaultLanguage
}

export interface SupportedLanguagesCarrier {
  readonly options: {
    readonly supportedLngs?: unknown
  }
}

/**
 * Reads the supported-language set an instance was created with, filtering
 * i18next's internal `cimode`; returns [] when none is declared.
 */
export function readSupportedLanguages(
  instance: SupportedLanguagesCarrier,
): readonly string[] {
  const raw = instance.options.supportedLngs
  const entries =
    Array.isArray(raw) ? (raw as readonly string[]) : typeof raw === 'string' && raw.length > 0 ? [raw] : []
  return entries.filter((entry) => entry.toLowerCase() !== 'cimode')
}
