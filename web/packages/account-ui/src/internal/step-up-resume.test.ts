/**
 * The step-up re-login resume record: it carries the account that recorded it,
 * the page, the owning surface and the operation a new sign-in must re-issue
 * across the session's end. Only a site-internal path and a JSON-safe
 * descriptor are kept, a surface takes only its own account's record, a record
 * another account wrote is never replayed, and taking consumes it so the
 * operation is re-issued once.
 */

import { beforeEach, describe, expect, it } from 'vitest'
import {
  forgetStepUpResume,
  pendingStepUpResume,
  rememberStepUpResume,
  takeStepUpResume,
} from './step-up-resume.js'

beforeEach(() => {
  window.sessionStorage.clear()
})

describe('step-up resume', () => {
  it('round-trips the account, the page, the owner and the operation', () => {
    rememberStepUpResume('user-1', 'surface', '/account/security', {
      kind: 'authorize',
      method: 'wechat',
    })
    expect(pendingStepUpResume('user-1')).toEqual({
      account: 'user-1',
      scope: 'surface',
      path: '/account/security',
      operation: { kind: 'authorize', method: 'wechat' },
    })
  })

  it('returns a record only to its own surface', () => {
    rememberStepUpResume('user-1', 'surface', '/account/security', {
      kind: 'bind',
    })
    expect(pendingStepUpResume('user-1', 'another-surface')).toBeNull()
    // Another surface's read leaves the record in place.
    expect(takeStepUpResume('user-1', 'another-surface')).toBeNull()
    expect(pendingStepUpResume('user-1', 'surface')).not.toBeNull()
  })

  it('consumes the record when its surface takes it', () => {
    rememberStepUpResume('user-1', 'surface', '/account/security', {
      kind: 'unbind',
      id: '1',
    })
    expect(takeStepUpResume('user-1', 'surface')).toEqual({
      account: 'user-1',
      scope: 'surface',
      path: '/account/security',
      operation: { kind: 'unbind', id: '1' },
    })
    expect(pendingStepUpResume('user-1')).toBeNull()
  })

  it('does not replay a record for another account', () => {
    rememberStepUpResume('user-1', 'surface', '/account/security', {
      kind: 'unbind',
      id: '1',
    })
    // The tab's next sign-in landed on another account: the record is not
    // returned, and taking it from the other account discards it so it can
    // never replay for anyone.
    expect(pendingStepUpResume('user-2')).toBeNull()
    expect(takeStepUpResume('user-2', 'surface')).toBeNull()
    expect(pendingStepUpResume('user-1')).toBeNull()
  })

  it('does not replay for an anonymous session, and keeps the record for its account', () => {
    rememberStepUpResume('user-1', 'surface', '/account/security', {
      kind: 'unbind',
      id: '1',
    })
    // No account is signed in, so nothing can be its operation. The record is
    // neither returned nor consumed: it stays for the account that wrote it.
    expect(pendingStepUpResume('')).toBeNull()
    expect(takeStepUpResume('', 'surface')).toBeNull()
    expect(pendingStepUpResume('user-1')).not.toBeNull()
  })

  it('drops an empty account or scope, or a path that is not a site-internal one', () => {
    rememberStepUpResume('', 'surface', '/account/security', { kind: 'bind' })
    rememberStepUpResume('user-1', '', '/account/security', { kind: 'bind' })
    rememberStepUpResume(
      'user-1',
      'surface',
      'https://elsewhere.example/account',
      { kind: 'bind' },
    )
    rememberStepUpResume('user-1', 'surface', '//elsewhere.example/account', {
      kind: 'bind',
    })
    expect(pendingStepUpResume('user-1')).toBeNull()
  })

  it('reads a malformed record as none', () => {
    window.sessionStorage.setItem('speed.step-up-resume', 'not json')
    expect(pendingStepUpResume('user-1')).toBeNull()
    window.sessionStorage.setItem(
      'speed.step-up-resume',
      JSON.stringify({ scope: 'surface', path: '//elsewhere.example' }),
    )
    expect(pendingStepUpResume('user-1')).toBeNull()
    window.sessionStorage.setItem(
      'speed.step-up-resume',
      JSON.stringify({ path: '/account/security', operation: {} }),
    )
    expect(pendingStepUpResume('user-1')).toBeNull()
    // A record with no account is as unusable as one with no scope: the
    // account is what binds it to a sign-in.
    window.sessionStorage.setItem(
      'speed.step-up-resume',
      JSON.stringify({
        scope: 'surface',
        path: '/account/security',
        operation: {},
      }),
    )
    expect(pendingStepUpResume('user-1')).toBeNull()
  })

  it('forgets the record on demand', () => {
    rememberStepUpResume('user-1', 'surface', '/account/security', {
      kind: 'bind',
    })
    forgetStepUpResume()
    expect(pendingStepUpResume('user-1')).toBeNull()
  })
})
