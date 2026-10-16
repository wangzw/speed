/**
 * The access-token seam.
 *
 * A session delivered as a cookie needs none of this: the browser holds
 * the session cookie and attaches it per the client's `credentials` mode,
 * so a cookie-session host (the platform's default, via @speed/auth-core)
 * leaves the store out and no Authorization header is ever sent. This
 * store is for a host that still authenticates with a bearer token: the
 * access token lives in memory only (a token in localStorage is a
 * credential an XSS walks away with). The store is synchronous on
 * purpose -- it is a memory cell, not an API, and the request path reads
 * it on every send with no async indirection. Nothing in this package
 * touches localStorage or any other persistence -- there is no storage
 * seam to bind, by design, so a host cannot accidentally configure token
 * persistence.
 */

/** Where the current bearer token lives between requests. */
export interface AccessTokenStore {
  /** The current token, or null when signed out / not yet signed in. */
  get(): string | null
  /** Store a fresh token; pass null to sign out (clears the memory cell). */
  set(token: string | null): void
}

/**
 * The in-memory AccessTokenStore. Process-lifetime only: reloading the
 * page signs the user out until the host's session layer re-establishes
 * a session -- which a cookie-delivered session does on its own, so a
 * cookie host does not use this store at all. Tests and hosts that do
 * use it inject it (or their own store) through ClientOptions; the
 * client never creates one.
 */
export function createMemoryAccessTokenStore(): AccessTokenStore {
  let token: string | null = null
  return {
    get(): string | null {
      return token
    },
    set(next: string | null): void {
      token = next
    },
  }
}
