/**
 * The step-up re-login resume record. A record belongs to the account and the
 * surface that wrote it, and it never holds a credential.
 */

import { isSitePath } from './binding-return.js'

const STORAGE_KEY = 'speed.step-up-resume'

export interface StepUpResume {
  readonly account: string
  readonly scope: string
  readonly path: string
  readonly operation: unknown
}

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

/** A malformed record, or one missing a member, reads as none. */
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
 * Records the operation a re-login must resume. An empty account or scope, a
 * non-site path, or a tab that refuses storage records nothing.
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
    // The tab refuses storage; the sign-in still returns to the URL.
  }
}

/**
 * The pending record belonging to `account`, without consuming it. An empty
 * account, or another account's record, reads as none; `scope` narrows the
 * read to one surface.
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

export function forgetStepUpResume(): void {
  try {
    storage()?.removeItem(STORAGE_KEY)
  } catch {
    // Nothing to clear when the tab refuses storage.
  }
}

/**
 * Takes and consumes the record belonging to `account` and `scope`, so the
 * operation is re-issued once. Another surface's record is left in place; a
 * record another account wrote is discarded.
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
