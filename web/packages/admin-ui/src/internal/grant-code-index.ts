/**
 * The (resource, action) -> permission-code lookup for authorization rows.
 *
 * A grant carries the (resource, action) pair the policy engine matches on,
 * while a grant route names a permission by its code and the tenant's
 * permission set holds codes. Two surfaces need the same translation: the
 * permissions page labels a grant row with the code the catalog names it,
 * and the member panel compares a role's grants against the caller's own
 * permission set before offering to bind it.
 *
 * pkg/authz/internal/translation is the authority for the pairing, but the
 * catalog view carries only codes and the grant view only pairs, so the
 * lookup infers one from the other:
 *
 *  - a code's trailing segment is its action;
 *  - the object name is the last run of segments before the action, with
 *    the resource's underscore-separated tokens matched as a multiset. That
 *    covers both `authn.account.create` (account/create, the object is the
 *    suffix) and the qualified names the translation declares, such as
 *    `authz.tenant.role.read` (tenant_role/read) and `settings.tenant.read`
 *    (tenant_settings/read);
 *  - the same object word can be shared by a qualified and an unqualified
 *    code (`authz.role.read` beside `authz.tenant.role.read`). Both match
 *    the shorter resource, so the code with the fewest segments -- the
 *    unqualified one -- names it; an equally short tie stays unresolved;
 *  - where the object name is not a suffix at all (`authn.mfa.reset` is
 *    factor/reset), the pair is matched by its action alone, and only when
 *    the catalog declares that action once.
 *
 * A pair the lookup cannot name with one code resolves to null, so a caller
 * is never offered a wrong code: the panel refuses a bind it cannot prove,
 * and the permissions page withholds the revoke it cannot name.
 */

import type { AdminPermission } from '../operations.js'

/** One catalog code, split once for both the action and the object match. */
interface Entry {
  readonly code: string
  readonly segments: readonly string[]
}

export function buildGrantCodeIndex(
  permissions: readonly AdminPermission[],
): (resource: string, action: string) => string | null {
  // Codes carrying each action, for the action-only fallback.
  const byAction = new Map<string, Entry[]>()
  for (const entry of permissions) {
    const segments = entry.permission.split('.')
    const action = segments[segments.length - 1] ?? ''
    if (action === '' || segments.length < 2) {
      continue
    }
    const named: Entry = { code: entry.permission, segments }
    const bucket = byAction.get(action)
    if (bucket === undefined) {
      byAction.set(action, [named])
    } else {
      bucket.push(named)
    }
  }
  return (resource, action) => {
    const tokens = resource.split('_').filter((token) => token !== '')
    const bucket = byAction.get(action)
    if (bucket === undefined) {
      return null
    }
    const matches = tokens.length === 0
      ? []
      : bucket.filter((entry) => objectNameMatches(entry.segments, tokens))
    if (matches.length === 1) {
      return matches[0]?.code ?? null
    }
    if (matches.length > 1) {
      // The least qualified code names the object; two equally qualified
      // codes leave the pair ambiguous and resolve to nothing.
      const shortest = Math.min(
        ...matches.map((entry) => entry.segments.length),
      )
      const winners = matches.filter(
        (entry) => entry.segments.length === shortest,
      )
      return winners.length === 1 ? (winners[0]?.code ?? null) : null
    }
    // No object-name match: an action the catalog declares once still names
    // its only pair, whatever the translation called the object.
    return bucket.length === 1 ? (bucket[0]?.code ?? null) : null
  }
}

/**
 * Whether the code's segments immediately before the action end in the
 * resource's tokens, compared as a multiset so a qualified name whose word
 * order differs from the code's (`tenant_settings` in `settings.tenant.read`)
 * still matches.
 */
function objectNameMatches(
  segments: readonly string[],
  tokens: readonly string[],
): boolean {
  if (segments.length < tokens.length + 1) {
    return false
  }
  const suffix = segments.slice(segments.length - 1 - tokens.length, -1)
  const remaining = [...suffix]
  for (const token of tokens) {
    const at = remaining.indexOf(token)
    if (at === -1) {
      return false
    }
    remaining.splice(at, 1)
  }
  return remaining.length === 0
}
