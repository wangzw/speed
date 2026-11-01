/**
 * The step-up re-login resume record.
 *
 * A step-up this deployment cannot raise offers a re-login exit: the session
 * ends and the host's gate renders its sign-in surface at the same URL. The
 * refused write cannot survive that unmount in React state, so the surface
 * that opened the challenge writes a resume record here before the session
 * ends: the page the operation belongs to, the surface that owns it, and a
 * JSON-safe descriptor of the operation itself. After the new sign-in the
 * surface mounts at the same page, takes the record back and re-issues
 * exactly the operation it names.
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

/** The page, owning surface and operation a re-login must resume. */
export interface StepUpResume {
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
 * Records the operation a re-login must resume. An empty scope, a path that
 * is not a site-internal relative one, or a tab that refuses storage records
 * nothing: the sign-in still returns the viewer to the page the host keeps
 * in the URL, but the operation is not re-issued.
 */
export function rememberStepUpResume(
  scope: string,
  path: string,
  operation: unknown,
): void {
  if (scope === '' || !isSitePath(path)) {
    return
  }
  try {
    storage()?.setItem(
      STORAGE_KEY,
      JSON.stringify({ scope, path, operation }),
    )
  } catch {
    // The tab refuses storage; the new sign-in still returns to the URL.
  }
}

/**
 * The pending record, without consuming it. `scope` narrows the read to one
 * surface's record; another surface's is not returned.
 */
export function pendingStepUpResume(scope?: string): StepUpResume | null {
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
      readonly scope?: unknown
      readonly path?: unknown
      readonly operation?: unknown
    }
    if (
      typeof record.scope !== 'string' ||
      record.scope === '' ||
      typeof record.path !== 'string' ||
      !isSitePath(record.path) ||
      record.operation === undefined
    ) {
      return null
    }
    if (scope !== undefined && record.scope !== scope) {
      return null
    }
    return { scope: record.scope, path: record.path, operation: record.operation }
  } catch {
    return null
  }
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
 * Takes the record belonging to `scope`, consuming it so the operation is
 * re-issued once. Another surface's record is left in place.
 */
export function takeStepUpResume(scope: string): StepUpResume | null {
  const record = pendingStepUpResume(scope)
  if (record !== null) {
    forgetStepUpResume()
  }
  return record
}
