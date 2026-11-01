/**
 * The step-up re-login resume record: it carries the page, the owning surface
 * and the operation a new sign-in must re-issue across the session's end. Only
 * a site-internal path and a JSON-safe descriptor are kept, a surface takes
 * only its own record, and taking consumes it so the operation is re-issued
 * once.
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
  it('round-trips the page, the owner and the operation', () => {
    rememberStepUpResume('surface', '/account/security', {
      kind: 'authorize',
      method: 'wechat',
    })
    expect(pendingStepUpResume()).toEqual({
      scope: 'surface',
      path: '/account/security',
      operation: { kind: 'authorize', method: 'wechat' },
    })
  })

  it('returns a record only to its own surface', () => {
    rememberStepUpResume('surface', '/account/security', { kind: 'bind' })
    expect(pendingStepUpResume('another-surface')).toBeNull()
    // Another surface's read leaves the record in place.
    expect(takeStepUpResume('another-surface')).toBeNull()
    expect(pendingStepUpResume('surface')).not.toBeNull()
  })

  it('consumes the record when its surface takes it', () => {
    rememberStepUpResume('surface', '/account/security', {
      kind: 'unbind',
      id: '1',
    })
    expect(takeStepUpResume('surface')).toEqual({
      scope: 'surface',
      path: '/account/security',
      operation: { kind: 'unbind', id: '1' },
    })
    expect(pendingStepUpResume()).toBeNull()
  })

  it('drops an empty scope or a path that is not a site-internal one', () => {
    rememberStepUpResume('', '/account/security', { kind: 'bind' })
    rememberStepUpResume(
      'surface',
      'https://elsewhere.example/account',
      { kind: 'bind' },
    )
    rememberStepUpResume('surface', '//elsewhere.example/account', {
      kind: 'bind',
    })
    expect(pendingStepUpResume()).toBeNull()
  })

  it('reads a malformed record as none', () => {
    window.sessionStorage.setItem('speed.step-up-resume', 'not json')
    expect(pendingStepUpResume()).toBeNull()
    window.sessionStorage.setItem(
      'speed.step-up-resume',
      JSON.stringify({ scope: 'surface', path: '//elsewhere.example' }),
    )
    expect(pendingStepUpResume()).toBeNull()
    window.sessionStorage.setItem(
      'speed.step-up-resume',
      JSON.stringify({ path: '/account/security', operation: {} }),
    )
    expect(pendingStepUpResume()).toBeNull()
  })

  it('forgets the record on demand', () => {
    rememberStepUpResume('surface', '/account/security', { kind: 'bind' })
    forgetStepUpResume()
    expect(pendingStepUpResume()).toBeNull()
  })
})
