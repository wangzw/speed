/** Phone-number composition for the mobile-number control, backed by libphonenumber-js's `mobile` metadata. */

import {
  formatIncompletePhoneNumber,
  getCountries,
  getCountryCallingCode,
  parsePhoneNumberFromString,
} from 'libphonenumber-js/mobile'
import type { CountryCode } from 'libphonenumber-js/mobile'

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
 * The country's display name in `locale`, from `Intl.DisplayNames`. Falls
 * back to the ISO code when the runtime cannot name it.
 */
export function phoneCountryName(country: string, locale: string): string {
  try {
    return new Intl.DisplayNames([locale], { type: 'region' }).of(country) ?? country
  } catch {
    return country
  }
}

/**
 * The country a browser locale starts on: the locale's own region when the
 * phone metadata covers it, else the maximized region, else US. A malformed
 * tag does not throw.
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
 * Parses a value that starts with `+` into its country and local part.
 * `null` while the input is too short to identify.
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
 * as-you-type grouping with every separator folded to a space.
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
