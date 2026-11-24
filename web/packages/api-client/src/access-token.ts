/** Where the current bearer token lives between requests. */
export interface AccessTokenStore {
  /** The current token, or null when signed out / not yet signed in. */
  get(): string | null
  /** Store a fresh token; pass null to sign out (clears the memory cell). */
  set(token: string | null): void
}

/** An in-memory AccessTokenStore that lives for the process only. */
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
