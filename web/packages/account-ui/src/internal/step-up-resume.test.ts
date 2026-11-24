/**
 * The step-up re-login resume record checks that only its own account's
 * record is taken, and that taking consumes it.
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
    // Another account's record is discarded so it never replays.
    expect(pendingStepUpResume('user-2')).toBeNull()
    expect(takeStepUpResume('user-2', 'surface')).toBeNull()
    expect(pendingStepUpResume('user-1')).toBeNull()
  })

  it('does not replay for an anonymous session, and keeps the record for its account', () => {
    rememberStepUpResume('user-1', 'surface', '/account/security', {
      kind: 'unbind',
      id: '1',
    })
    // With no account signed in, the record stays for the one that wrote it.
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
    // The account is what binds a record to a sign-in.
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
