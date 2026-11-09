/**
 * The shared creation form's values and client-side validation.
 *
 * The server is the authority on every rule here: this module repeats the
 * name shape, the built-in reserved-name set and the profile bounds -- the
 * display-name and website limits included -- so the operator meets a typo
 * in place instead of a refusal after a round trip. A deployment may append
 * reserved names through `authn.account.reserved-usernames`; the client
 * cannot know those, so a name the server alone refuses still reaches the
 * error region.
 *
 * The values are one flat shape for both surfaces. The platform form adds
 * the contact address and website to the name, display name and
 * description; the self-service form renders only the latter three, and its
 * validator ignores the platform-only fields even when a host seeds them.
 */

/** Which surface is rendering the form. */
export type OrganizationCreateMode = 'platform' | 'self-service'

/** One creation form's controlled values. */
export interface OrganizationCreateValues {
  /** The public slug, unique across the roster and immutable after creation. */
  readonly name: string
  /** The organization's display name; required after trimming. */
  readonly displayName: string
  /** The organization's description; optional, at most 500 runes. */
  readonly description: string
  /** The organization's contact address; platform surface only. */
  readonly contactEmail: string
  /** The organization's website; platform surface only. */
  readonly website: string
}

/** The form's starting values. */
export const EMPTY_ORGANIZATION_CREATE_VALUES: OrganizationCreateValues = {
  name: '',
  displayName: '',
  description: '',
  contactEmail: '',
  website: '',
}

/** A field of the shared form. */
export type OrganizationCreateField = keyof OrganizationCreateValues

/** A client-side violation, keyed to a validation message leaf. */
export type OrganizationCreateViolation =
  | 'required'
  | 'nameShape'
  | 'nameReserved'
  | 'displayNameTooLong'
  | 'descriptionTooLong'
  | 'emailShape'
  | 'websiteShape'
  | 'websiteTooLong'

/** The violations a form render can show, one per field at most. */
export type OrganizationCreateViolations = Partial<
  Record<OrganizationCreateField, OrganizationCreateViolation>
>

/**
 * The name shape the server enforces: a DNS-label-shaped slug of three to
 * sixty-three lower-case letters, digits and dashes, beginning with a
 * letter or a digit. It is the username shape too, because a personal
 * tenant's name is its owner's username.
 */
const NAME_SHAPE = /^[a-z0-9][a-z0-9-]{2,62}$/

/**
 * The names the server keeps for itself: the API paths, the console and
 * its routes, the static asset prefix, and the health and readiness
 * probes. A deployment may append names; those are server-only.
 */
export const RESERVED_NAMES: readonly string[] = [
  'api',
  'console',
  'account',
  'admin',
  'system',
  'login',
  'logout',
  'signin',
  'signup',
  'static',
  'assets',
  'health',
  'ready',
  'metrics',
  'oauth',
  'callback',
  'well-known',
  'www',
  'root',
]

/** The reserved prefix no name may begin with (personal tenant names). */
export const RESERVED_NAME_PREFIX = 'personal-'

/** The maximum display-name length, in runes, the server enforces. */
export const DISPLAY_NAME_MAX_RUNES = 100

/** The maximum description length, in runes, the server enforces. */
export const DESCRIPTION_MAX_RUNES = 500

/** The maximum website length, in bytes, the server enforces. */
export const WEBSITE_MAX_BYTES = 2048

/** Whether a value is a slug the name domain admits. */
export function isNameShape(value: string): boolean {
  return NAME_SHAPE.test(value)
}

/** Whether a value is in the built-in reserved set or its reserved prefix. */
export function isReservedName(value: string): boolean {
  return (
    value.startsWith(RESERVED_NAME_PREFIX) ||
    RESERVED_NAMES.includes(value)
  )
}

/** The violation a name value carries, or undefined when it is admissible. */
export function nameViolation(
  value: string,
): OrganizationCreateViolation | undefined {
  const name = value.trim()
  if (name === '') {
    return 'required'
  }
  if (!isNameShape(name)) {
    return 'nameShape'
  }
  if (isReservedName(name)) {
    return 'nameReserved'
  }
  return undefined
}

/** Trim every field, the normalization the request body uses. */
export function normalizeOrganizationCreateValues(
  values: OrganizationCreateValues,
): OrganizationCreateValues {
  return {
    name: values.name.trim(),
    displayName: values.displayName.trim(),
    description: values.description.trim(),
    contactEmail: values.contactEmail.trim(),
    website: values.website.trim(),
  }
}

/** The number of runes in a string, the unit the profile bounds use. */
function runeLength(value: string): number {
  return Array.from(value).length
}

/** The number of UTF-8 bytes in a string, the unit the website bound uses. */
function byteLength(value: string): number {
  return new TextEncoder().encode(value).length
}

/** Whether a contact address is a single bare email address. */
function isBareEmail(value: string): boolean {
  if (value.length > 254 || /\s/.test(value)) {
    return false
  }
  return /^[^@]+@[^@]+$/.test(value)
}

/** Whether a website is an absolute http/https URL with a host and no
 * userinfo, the shape the server's `net/url` check admits. */
function isAbsoluteWebAddress(value: string): boolean {
  let parsed: URL
  try {
    parsed = new URL(value)
  } catch {
    return false
  }
  return (
    (parsed.protocol === 'http:' || parsed.protocol === 'https:') &&
    parsed.hostname !== '' &&
    parsed.username === '' &&
    parsed.password === ''
  )
}

/**
 * The violations the values carry for the given surface. The platform
 * form checks its full field set; the self-service form checks the name,
 * display name and description only.
 */
export function validateOrganizationCreateValues(
  values: OrganizationCreateValues,
  mode: OrganizationCreateMode,
): OrganizationCreateViolations {
  const normalized = normalizeOrganizationCreateValues(values)
  const violations: Record<string, OrganizationCreateViolation> = {}

  const name = nameViolation(normalized.name)
  if (name !== undefined) {
    violations.name = name
  }
  if (normalized.displayName === '') {
    violations.displayName = 'required'
  } else if (runeLength(normalized.displayName) > DISPLAY_NAME_MAX_RUNES) {
    violations.displayName = 'displayNameTooLong'
  }
  if (runeLength(normalized.description) > DESCRIPTION_MAX_RUNES) {
    violations.description = 'descriptionTooLong'
  }

  if (mode === 'platform') {
    if (
      normalized.contactEmail !== '' &&
      !isBareEmail(normalized.contactEmail)
    ) {
      violations.contactEmail = 'emailShape'
    }
    if (normalized.website !== '') {
      if (byteLength(normalized.website) > WEBSITE_MAX_BYTES) {
        violations.website = 'websiteTooLong'
      } else if (!isAbsoluteWebAddress(normalized.website)) {
        violations.website = 'websiteShape'
      }
    }
  }

  return violations as OrganizationCreateViolations
}

/** What the self-service page hands its create operation. */
export interface OrganizationCreateSubmission {
  readonly name: string
  readonly displayName: string
  readonly description: string
}

/** The organization a creation operation answers with. */
export interface CreatedOrganization {
  readonly name: string
  readonly displayName: string
  readonly description: string
}
