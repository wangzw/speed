/**
 * The organization profile's four descriptive fields shared by both surfaces;
 * the client bounds mirror the server, which stays the authority.
 */

/** The four descriptive fields in the spelling the views carry. */
export interface TenantProfileSource {
  readonly display_name: string
  readonly description: string
  readonly contact_email: string
  readonly website: string
}

/** The four descriptive fields as the form edits them. */
export interface TenantProfileFields {
  readonly displayName: string
  readonly description: string
  readonly contactEmail: string
  readonly website: string
}

/** The same fields in the write body's snake_case spelling. */
export interface TenantProfileWriteBody {
  readonly display_name: string
  readonly description: string
  readonly contact_email: string
  readonly website: string
}

/** The profile's client-side limits, mirroring the root module's rules. */
export const TENANT_PROFILE_LIMITS = {
  displayNameRunes: 100,
  descriptionRunes: 500,
  contactEmailBytes: 254,
  websiteBytes: 2048,
} as const

/** One field's refusal: the only local rule each field has. */
export type TenantProfileFieldError = 'tooLong' | 'invalidEmail' | 'invalidWebsite'

/** The fields the client refused, by name. */
export interface TenantProfileFieldErrors {
  readonly displayName?: TenantProfileFieldError
  readonly description?: TenantProfileFieldError
  readonly contactEmail?: TenantProfileFieldError
  readonly website?: TenantProfileFieldError
}

/** The fields in the form's spelling from a view that carries them. */
export function profileFieldsOf(source: TenantProfileSource): TenantProfileFields {
  return {
    displayName: source.display_name,
    description: source.description,
    contactEmail: source.contact_email,
    website: source.website,
  }
}

/** The write body for the profile route, from the form's fields. */
export function profileWriteOf(fields: TenantProfileFields): TenantProfileWriteBody {
  return {
    display_name: fields.displayName,
    description: fields.description,
    contact_email: fields.contactEmail,
    website: fields.website,
  }
}

/** The name a surface shows for an organization: its display name, or the name. */
export function displayNameOf(displayName: string, name: string): string {
  return displayName.trim() === '' ? name : displayName
}

/** A string's length in Unicode code points, the unit the server's rune limits count. */
function runeLength(value: string): number {
  return Array.from(value).length
}

/** A string's length in UTF-8 bytes, the unit the server's byte limits count. */
function byteLength(value: string): number {
  return new TextEncoder().encode(value).length
}

function looksLikeEmail(value: string): boolean {
  return /^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$/.test(value)
}

/**
 * Whether a string is an absolute http(s) address with a host and no userinfo.
 * Judged as a URL, so a bad scheme, a missing host and userinfo are all visible.
 */
function looksLikeWebsite(value: string): boolean {
  try {
    const url = new URL(value)
    return (
      (url.protocol === 'http:' || url.protocol === 'https:') &&
      url.hostname !== '' &&
      url.username === '' &&
      url.password === ''
    )
  } catch {
    return false
  }
}

/** The fields the form refuses locally; empty fields are always accepted. */
export function validateTenantProfileFields(
  fields: TenantProfileFields,
): TenantProfileFieldErrors {
  const errors: {
    displayName?: TenantProfileFieldError
    description?: TenantProfileFieldError
    contactEmail?: TenantProfileFieldError
    website?: TenantProfileFieldError
  } = {}
  if (runeLength(fields.displayName) > TENANT_PROFILE_LIMITS.displayNameRunes) {
    errors.displayName = 'tooLong'
  }
  if (runeLength(fields.description) > TENANT_PROFILE_LIMITS.descriptionRunes) {
    errors.description = 'tooLong'
  }
  if (
    fields.contactEmail.trim() !== '' &&
    (byteLength(fields.contactEmail) > TENANT_PROFILE_LIMITS.contactEmailBytes ||
      !looksLikeEmail(fields.contactEmail.trim()))
  ) {
    errors.contactEmail = 'invalidEmail'
  }
  if (
    fields.website.trim() !== '' &&
    (byteLength(fields.website) > TENANT_PROFILE_LIMITS.websiteBytes ||
      !looksLikeWebsite(fields.website.trim()))
  ) {
    errors.website = 'invalidWebsite'
  }
  return errors
}

/** Whether any field was refused locally. */
export function hasTenantProfileFieldErrors(
  errors: TenantProfileFieldErrors,
): boolean {
  return (
    errors.displayName !== undefined ||
    errors.description !== undefined ||
    errors.contactEmail !== undefined ||
    errors.website !== undefined
  )
}
