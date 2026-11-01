/**
 * The step-up re-login resume record: it carries the page and the
 * operation a new sign-in must re-issue across the session's end. Only a
 * site-internal path and a JSON-safe descriptor are kept, and taking the
 * record consumes it so the operation is re-issued once.
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
  it('round-trips the page and the operation', () => {
    rememberStepUpResume('/account/security', {
      kind: 'authorize',
      method: 'wechat',
    })
    expect(pendingStepUpResume()).toEqual({
      path: '/account/security',
      operation: { kind: 'authorize', method: 'wechat' },
    })
  })

  it('consumes the record when it is taken', () => {
    rememberStepUpResume('/account/security', { kind: 'unbind', id: '1' })
    expect(takeStepUpResume()).toEqual({
      path: '/account/security',
      operation: { kind: 'unbind', id: '1' },
    })
    expect(pendingStepUpResume()).toBeNull()
  })

  it('drops a path that is not a site-internal one', () => {
    rememberStepUpResume('https://elsewhere.example/account', { kind: 'bind' })
    rememberStepUpResume('//elsewhere.example/account', { kind: 'bind' })
    expect(pendingStepUpResume()).toBeNull()
  })

  it('reads a malformed record as none', () => {
    window.sessionStorage.setItem('speed.step-up-resume', 'not json')
    expect(pendingStepUpResume()).toBeNull()
    window.sessionStorage.setItem(
      'speed.step-up-resume',
      JSON.stringify({ path: '//elsewhere.example' }),
    )
    expect(pendingStepUpResume()).toBeNull()
    window.sessionStorage.setItem(
      'speed.step-up-resume',
      JSON.stringify({ path: '/account/security' }),
    )
    expect(pendingStepUpResume()).toBeNull()
  })

  it('forgets the record on demand', () => {
    rememberStepUpResume('/account/security', { kind: 'bind' })
    forgetStepUpResume()
    expect(pendingStepUpResume()).toBeNull()
  })
})
