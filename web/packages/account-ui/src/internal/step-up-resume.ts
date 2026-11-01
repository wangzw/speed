/**
 * The step-up re-login resume record.
 *
 * A step-up this deployment cannot raise offers a re-login exit: the session
 * ends and the host's gate renders its sign-in surface at the same URL. The
 * refused write cannot survive that unmount in React state, so the surface
 * that opened the challenge writes a resume record here before the session
 * ends: the page the operation belongs to, and a JSON-safe descriptor of the
 * operation itself. After the new sign-in the surface mounts at the same
 * page, reads the record back and re-issues exactly the operation it names.
 *
 * Only a site-internal relative path is stored, and only a descriptor the
 * surface itself understands. The record holds no credential: a surface that
 * would have to replay a password or a one-time code records none, because
 * those values must never be persisted. Such a surface keeps its re-login
 * exit but leaves the operation for the person to redo.
 */

import { isSitePath } from './binding-return.js'

const STORAGE_KEY = 'speed.step-up-resume'

/** The page and operation a re-login must return to and re-issue. */
export interface StepUpResume {
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
 * Records the operation a re-login must resume. A path that is not a
 * site-internal relative one, or a tab that refuses storage, records
 * nothing: the sign-in still returns the viewer to the page the host keeps
 * in the URL, but the operation is not re-issued.
 */
export function rememberStepUpResume(
  path: string,
  operation: unknown,
): void {
  if (!isSitePath(path)) {
    return
  }
  try {
    storage()?.setItem(STORAGE_KEY, JSON.stringify({ path, operation }))
  } catch {
    // The tab refuses storage; the new sign-in still returns to the URL.
  }
}

/** The pending record, without consuming it. */
export function pendingStepUpResume(): StepUpResume | null {
  try {
    const raw = storage()?.getItem(STORAGE_KEY)
    if (raw === null || raw === undefined) {
      return null
    }
    const parsed: unknown = JSON.parse(raw)
    if (typeof parsed !== 'object' || parsed === null) {
      return null
    }
    const record = parsed as { readonly path?: unknown; readonly operation?: unknown }
    if (
      typeof record.path !== 'string' ||
      !isSitePath(record.path) ||
      record.operation === undefined
    ) {
      return null
    }
    return { path: record.path, operation: record.operation }
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

/** Takes the pending record, consuming it so it is re-issued once. */
export function takeStepUpResume(): StepUpResume | null {
  const record = pendingStepUpResume()
  if (record !== null) {
    forgetStepUpResume()
  }
  return record
}
