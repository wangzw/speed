/**
 * The step-up re-login resume record.
 *
 * A step-up this deployment cannot raise offers a re-login exit: the session
 * ends and the host's gate renders its sign-in surface at the same URL. The
 * refused write cannot survive that unmount in React state, so the surface
 * that opened the challenge writes a resume record here before the session
 * ends: the account that recorded it, the page the operation belongs to, the
 * surface that owns it, and a JSON-safe descriptor of the operation itself.
 * After the new sign-in the surface mounts at the same page, takes the record
 * back and re-issues exactly the operation it names.
 *
 * The account is the session snapshot's principal account id
 * (`AuthSnapshot.principal.account_id`), the stable identity the session
 * carries for the signed-in account. It does not change for as long as that
 * account stays signed in, so a record written before the session ends still
 * names the account a later sign-in must match. A record belongs to the
 * account that wrote it: reading compares the record's account with the
 * current one and fails closed on a mismatch, and taking a record another
 * account wrote discards it. A sign-in that lands on a different account in
 * the same tab therefore never replays the previous account's operation.
 *
 * The owning surface is part of the record because one page can mount more
 * than one resumable surface. A surface takes only a record whose scope is
 * its own; another surface's record is left in place for it.
 *
 * Only a site-internal relative path is stored, and only a descriptor the
 * surface itself understands. The record holds no credential: a surface that
 * would have to replay a password or a one-time code records none, because
 * those values must never be persisted. Such a surface keeps its re-login
 * exit but leaves the operation for the person to redo.
 */

import { isSitePath } from './binding-return.js'

const STORAGE_KEY = 'speed.step-up-resume'

/** The account, page, owning surface and operation a re-login must resume. */
export interface StepUpResume {
  /** The account that recorded the operation, so only it takes it back. */
  readonly account: string
  /** The surface that recorded the operation, so only it takes it back. */
  readonly scope: string
  /** The page the operation belongs to, a site-internal relative path. */
  readonly path: string
  /** The surface's own JSON-safe operation descriptor. */
  readonly operation: unknown
}

/** Whether storage is reachable at all (a server render has none). */
function storage(): Storage | null {
  if (typeof window === 'undefined') {
    return null
  }
  try {
    return window.sessionStorage
  } catch {
    return null
  }
}

/**
 * The stored record, read and shape-checked without comparing it to any
 * account. A malformed record, or one missing a member, reads as none.
 */
function storedRecord(): StepUpResume | null {
  try {
    const raw = storage()?.getItem(STORAGE_KEY)
    if (raw === null || raw === undefined) {
      return null
    }
    const parsed: unknown = JSON.parse(raw)
    if (typeof parsed !== 'object' || parsed === null) {
      return null
    }
    const record = parsed as {
      readonly account?: unknown
      readonly scope?: unknown
      readonly path?: unknown
      readonly operation?: unknown
    }
    if (
      typeof record.account !== 'string' ||
      record.account === '' ||
      typeof record.scope !== 'string' ||
      record.scope === '' ||
      typeof record.path !== 'string' ||
      !isSitePath(record.path) ||
      record.operation === undefined
    ) {
      return null
    }
    return {
      account: record.account,
      scope: record.scope,
      path: record.path,
      operation: record.operation,
    }
  } catch {
    return null
  }
}

/**
 * Records the operation a re-login must resume. An empty account, an empty
 * scope, a path that is not a site-internal relative one, or a tab that
 * refuses storage records nothing: the sign-in still returns the viewer to
 * the page the host keeps in the URL, but the operation is not re-issued.
 */
export function rememberStepUpResume(
  account: string,
  scope: string,
  path: string,
  operation: unknown,
): void {
  if (account === '' || scope === '' || !isSitePath(path)) {
    return
  }
  try {
    storage()?.setItem(
      STORAGE_KEY,
      JSON.stringify({ account, scope, path, operation }),
    )
  } catch {
    // The tab refuses storage; the new sign-in still returns to the URL.
  }
}

/**
 * The pending record belonging to `account`, without consuming it. It fails
 * closed: an empty account, or a record another account wrote, reads as none.
 * `scope` narrows the read to one surface's record; another surface's is not
 * returned.
 */
export function pendingStepUpResume(
  account: string,
  scope?: string,
): StepUpResume | null {
  const record = storedRecord()
  if (record === null || account === '' || record.account !== account) {
    return null
  }
  if (scope !== undefined && record.scope !== scope) {
    return null
  }
  return record
}

/** Drops the pending record. */
export function forgetStepUpResume(): void {
  try {
    storage()?.removeItem(STORAGE_KEY)
  } catch {
    // Nothing to clear when the tab refuses storage.
  }
}

/**
 * Takes the record belonging to `account` and `scope`, consuming it so the
 * operation is re-issued once. Another surface's record on the same account
 * is left in place. While no account is signed in nothing can be its
 * operation, so the record is neither returned nor consumed. A record another
 * account wrote can never become this account's operation, so it is discarded
 * rather than returned.
 */
export function takeStepUpResume(
  account: string,
  scope: string,
): StepUpResume | null {
  const record = storedRecord()
  if (record === null) {
    return null
  }
  if (account === '') {
    return null
  }
  if (record.account !== account) {
    forgetStepUpResume()
    return null
  }
  if (record.scope !== scope) {
    return null
  }
  forgetStepUpResume()
  return record
}
