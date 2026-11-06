/**
 * The shared creation form's values and client-side validation.
 *
 * The server is the authority on every rule here: this module repeats the
 * alias shape, the built-in reserved-name set and the profile bounds so
 * the operator meets a typo in place instead of a refusal after a round
 * trip. A deployment may append reserved names through
 * `authn.account.reserved-usernames`; the client cannot know those, so a
 * name the server alone refuses still reaches the error region.
 *
 * The values are one flat shape for both surfaces. The platform form
 * renders the identifier, contact address and website; the self-service
 * form renders only the alias, display name and description, and its
 * validator ignores the other three even when a host seeds them.
 */

/** Which surface is rendering the form. */
export type OrganizationCreateMode = 'platform' | 'self-service'

/** One creation form's controlled values. */
export interface OrganizationCreateValues {
  /** The platform surface's internal identifier; ignored by self-service. */
  readonly id: string
  /** The public slug, unique across the roster and immutable after creation. */
  readonly alias: string
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
  id: '',
  alias: '',
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
  | 'aliasShape'
  | 'aliasReserved'
  | 'descriptionTooLong'
  | 'emailShape'
  | 'websiteShape'

/** The violations a form render can show, one per field at most. */
export type OrganizationCreateViolations = Partial<
  Record<OrganizationCreateField, OrganizationCreateViolation>
>

/**
 * The alias shape the server enforces: a DNS-label-shaped slug of three to
 * sixty-three lower-case letters, digits and dashes, beginning with a
 * letter or a digit. It is the username shape too, because a personal
 * tenant's alias is its owner's username.
 */
const ALIAS_SHAPE = /^[a-z0-9][a-z0-9-]{2,62}$/

/**
 * The names the server keeps for itself: the API paths, the console and
 * its routes, the static asset prefix, and the health and readiness
 * probes. A deployment may append names; those are server-only.
 */
export const RESERVED_ALIASES: readonly string[] = [
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

/** The reserved prefix no alias may begin with (personal tenant ids). */
export const RESERVED_ALIAS_PREFIX = 'personal-'

/** The maximum description length, in runes, the server enforces. */
export const DESCRIPTION_MAX_RUNES = 500

/** Whether a value is a slug the alias domain admits. */
export function isAliasShape(value: string): boolean {
  return ALIAS_SHAPE.test(value)
}

/** Whether a value is in the built-in reserved set or its reserved prefix. */
export function isReservedAlias(value: string): boolean {
  return (
    value.startsWith(RESERVED_ALIAS_PREFIX) ||
    RESERVED_ALIASES.includes(value)
  )
}

/** The violation an alias value carries, or undefined when it is admissible. */
export function aliasViolation(
  value: string,
): OrganizationCreateViolation | undefined {
  const alias = value.trim()
  if (alias === '') {
    return 'required'
  }
  if (!isAliasShape(alias)) {
    return 'aliasShape'
  }
  if (isReservedAlias(alias)) {
    return 'aliasReserved'
  }
  return undefined
}

/** Trim every field, the normalization the request body uses. */
export function normalizeOrganizationCreateValues(
  values: OrganizationCreateValues,
): OrganizationCreateValues {
  return {
    id: values.id.trim(),
    alias: values.alias.trim(),
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
 * form checks its full field set; the self-service form checks the alias,
 * display name and description only.
 */
export function validateOrganizationCreateValues(
  values: OrganizationCreateValues,
  mode: OrganizationCreateMode,
): OrganizationCreateViolations {
  const normalized = normalizeOrganizationCreateValues(values)
  const violations: Record<string, OrganizationCreateViolation> = {}

  const alias = aliasViolation(normalized.alias)
  if (alias !== undefined) {
    violations.alias = alias
  }
  if (normalized.displayName === '') {
    violations.displayName = 'required'
  }
  if (runeLength(normalized.description) > DESCRIPTION_MAX_RUNES) {
    violations.description = 'descriptionTooLong'
  }

  if (mode === 'platform') {
    if (normalized.id === '') {
      violations.id = 'required'
    }
    if (
      normalized.contactEmail !== '' &&
      !isBareEmail(normalized.contactEmail)
    ) {
      violations.contactEmail = 'emailShape'
    }
    if (normalized.website !== '' && !isAbsoluteWebAddress(normalized.website)) {
      violations.website = 'websiteShape'
    }
  }

  return violations as OrganizationCreateViolations
}

/** What the self-service page hands its create operation. */
export interface OrganizationCreateSubmission {
  readonly alias: string
  readonly displayName: string
  readonly description: string
}

/** The organization a creation operation answers with. */
export interface CreatedOrganization {
  readonly id: string
  readonly alias: string
  readonly displayName: string
  readonly description: string
}
