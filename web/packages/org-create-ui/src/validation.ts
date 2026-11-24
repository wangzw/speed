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

/** Mirrors the server's slug rule; a divergence admits names the server rejects. */
const NAME_SHAPE = /^[a-z0-9][a-z0-9-]{2,62}$/

/** The names the server reserves for itself. A deployment may append
 * server-only names. */
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

function runeLength(value: string): number {
  return Array.from(value).length
}

function byteLength(value: string): number {
  return new TextEncoder().encode(value).length
}

function isBareEmail(value: string): boolean {
  if (value.length > 254 || /\s/.test(value)) {
    return false
  }
  return /^[^@]+@[^@]+$/.test(value)
}

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
  /** The public slug. */
  readonly name: string
  /** The display name. */
  readonly displayName: string
  /** The description. */
  readonly description: string
}

/** The organization a creation operation answers with. */
export interface CreatedOrganization {
  /** The public slug. */
  readonly name: string
  /** The display name. */
  readonly displayName: string
  /** The description. */
  readonly description: string
}
