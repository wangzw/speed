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
 * A code's trailing two dot-separated segments spell its pair
 * (authn.account.create -> account/create; a bare module code like
 * audit.read -> audit/read), so those build the primary lookup. Where the
 * pair's resource is not the code's second-to-last segment (pkg/authz's
 * translation table is the authority and is not always a suffix of the
 * code, e.g. authn.mfa.reset -> factor/reset), the pair is matched by its
 * action alone, and only when the catalog declares that action once; an
 * action two pairs share (read, manage, ...) stays ambiguous and resolves
 * to null, so a caller is never offered a wrong code.
 *
 * Returns a function; a pair the catalog does not name resolves to null.
 */

import type { AdminPermission } from '../operations.js'

export function buildGrantCodeIndex(
  permissions: readonly AdminPermission[],
): (resource: string, action: string) => string | null {
  const byPair = new Map<string, string>()
  // One code per action, or null once a second code shares the action.
  const byAction = new Map<string, string | null>()
  for (const entry of permissions) {
    const segments = entry.permission.split('.')
    const action = segments[segments.length - 1] ?? ''
    if (action === '') {
      continue
    }
    if (segments.length >= 2) {
      const resource = segments[segments.length - 2] ?? ''
      if (resource !== '') {
        byPair.set(`${resource}:${action}`, entry.permission)
      }
    }
    byAction.set(action, byAction.has(action) ? null : entry.permission)
  }
  return (resource, action) => {
    const pair = byPair.get(`${resource}:${action}`)
    if (pair !== undefined) {
      return pair
    }
    return byAction.get(action) ?? null
  }
}
