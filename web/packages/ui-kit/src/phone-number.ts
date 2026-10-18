/**
 * Phone-number composition for PhoneNumberField.
 *
 * A phone number is edited as two parts -- a country/region and a local
 * number -- and submitted as one E.164 string. Everything country-aware
 * here is libphonenumber-js's, not a table of our own: its "min" metadata
 * build supplies the country set, the calling codes, the as-you-type
 * grouping and the per-country length/prefix validation. Country names
 * are not data of ours either: they come from `Intl.DisplayNames` at
 * render time, so they follow the active language without a translation
 * key per region.
 *
 * The two validation verdicts are deliberately distinct:
 * `incomplete` (a number that is still shorter than any number of that
 * country can be), and `invalid` (the length is plausible but the
 * number's own pattern is not). The field renders a different message
 * for each, naming the calling code, so a viewer is told what is wrong
 * rather than that something is.
 */

import {
  formatIncompletePhoneNumber,
  getCountries,
  getCountryCallingCode,
  parsePhoneNumberFromString,
} from 'libphonenumber-js/min'
import type { CountryCode } from 'libphonenumber-js/min'

/** The two ways a typed number can be refused before it is sent. */
export type PhoneNumberValidation = 'incomplete' | 'invalid'

/** The two parts the control edits. */
export interface PhoneNumberParts {
  /** ISO 3166-1 alpha-2 country code, uppercase. */
  readonly country: string
  /** The local number: digits only, no calling code. */
  readonly nationalNumber: string
}

const PHONE_COUNTRIES = getCountries()

const COUNTRY_SET: ReadonlySet<string> = new Set(PHONE_COUNTRIES)

/** Whether `value` is a country the phone metadata covers. */
export function isPhoneCountry(value: string): boolean {
  return COUNTRY_SET.has(value)
}

/** The countries the phone metadata covers, as ISO alpha-2 codes. */
export function phoneCountries(): readonly string[] {
  return PHONE_COUNTRIES
}

/** The calling code of a covered country, without the leading `+`. */
export function phoneCountryCallingCode(country: string): string | null {
  if (!COUNTRY_SET.has(country)) {
    return null
  }
  return getCountryCallingCode(country as CountryCode)
}

/**
 * The country's display name in `locale`, from `Intl.DisplayNames`. A
 * locale or region the runtime cannot name falls back to the ISO code,
 * so the selector never shows an empty entry.
 */
export function phoneCountryName(country: string, locale: string): string {
  try {
    return new Intl.DisplayNames([locale], { type: 'region' }).of(country) ?? country
  } catch {
    return country
  }
}

/**
 * The country a browser locale starts on: the locale's own region when
 * the phone metadata covers it (zh-CN -> CN, en-US -> US), the region
 * the language maximizes to when the tag names none (en -> US, zh -> CN),
 * and US otherwise. A malformed tag never throws on this path.
 */
export function defaultPhoneCountry(locale?: string): string {
  const tag =
    locale ?? (typeof navigator === 'undefined' ? 'en-US' : navigator.language)
  let region: string | undefined
  try {
    region = new Intl.Locale(tag).maximize().region
  } catch {
    region = undefined
  }
  if (region !== undefined && COUNTRY_SET.has(region)) {
    return region
  }
  return COUNTRY_SET.has('US') ? 'US' : (PHONE_COUNTRIES[0] ?? 'US')
}

/** The digits of an input value, dropping any formatting and a leading `+`. */
export function nationalNumberDigits(value: string): string {
  return value.replace(/\D+/g, '')
}

/**
 * Parse a value that starts with `+` into its country and local part.
 * `null` while the input is still too short to identify (a lone `+`, or
 * a partial calling code), which lets the field keep showing what was
 * typed until enough of it exists to switch countries.
 */
export function parseInternationalPhoneNumber(
  value: string,
): PhoneNumberParts | null {
  const trimmed = value.trim()
  if (!trimmed.startsWith('+')) {
    return null
  }
  const parsed = parsePhoneNumberFromString(trimmed)
  if (parsed === undefined || parsed.country === undefined) {
    return null
  }
  return { country: parsed.country, nationalNumber: parsed.nationalNumber }
}

/**
 * The local number as the viewer sees it while typing: the library's
 * as-you-type grouping with every separator folded to a space, so the
 * field reads the same way in every country ("415 555 2671", not
 * "(415) 555-2671").
 */
export function formatNationalPhoneNumber(
  country: string,
  nationalNumber: string,
): string {
  if (!COUNTRY_SET.has(country) || nationalNumber === '') {
    return nationalNumber
  }
  return formatIncompletePhoneNumber(nationalNumber, country as CountryCode)
    .replace(/\D+/g, ' ')
    .trim()
}

/**
 * Whether the local number can be submitted under `country`: `null` when
 * it can, otherwise which way it fails. The country must be one the
 * metadata covers; an unknown one is answered as `invalid`, never thrown.
 */
export function validatePhoneNumber(
  country: string,
  nationalNumber: string,
): PhoneNumberValidation | null {
  if (!COUNTRY_SET.has(country)) {
    return 'invalid'
  }
  if (nationalNumber === '') {
    return 'incomplete'
  }
  const parsed = parsePhoneNumberFromString(
    nationalNumber,
    country as CountryCode,
  )
  if (parsed === undefined) {
    return 'invalid'
  }
  if (parsed.isValid()) {
    return null
  }
  return parsed.isPossible() ? 'invalid' : 'incomplete'
}

/**
 * The E.164 string the backend takes, or `null` while the parts do not
 * make a valid number. This is the only place the two parts are joined.
 */
export function toE164(
  country: string,
  nationalNumber: string,
): string | null {
  if (validatePhoneNumber(country, nationalNumber) !== null) {
    return null
  }
  const parsed = parsePhoneNumberFromString(
    nationalNumber,
    country as CountryCode,
  )
  return parsed?.number ?? null
}
