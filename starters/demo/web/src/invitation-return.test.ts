import { beforeEach, describe, expect, it } from 'vitest'
import {
  forgetInvitationLanding,
  pendingInvitationLanding,
  rememberInvitationLanding,
} from './invitation-return.js'

const STORAGE_KEY = 'speed.invitation-return'

const LANDING = '/invitations/tok-123'

beforeEach(() => {
  window.sessionStorage.clear()
})

describe('the invitation landing a registration keeps', () => {
  it('returns the invitation landing a registration started from', () => {
    rememberInvitationLanding(LANDING)

    expect(pendingInvitationLanding()).toBe(LANDING)
  })

  it('clears an earlier landing when the registration started elsewhere', () => {
    rememberInvitationLanding(LANDING)

    rememberInvitationLanding('/')

    expect(pendingInvitationLanding()).toBeNull()
  })

  it('drops the landing once the visitor is on the way back', () => {
    rememberInvitationLanding(LANDING)

    forgetInvitationLanding()

    expect(pendingInvitationLanding()).toBeNull()
  })

  it('never returns a stored value that is not an invitation landing', () => {
    window.sessionStorage.setItem(STORAGE_KEY, 'https://example.test/phish')

    expect(pendingInvitationLanding()).toBeNull()
  })

  it('does not treat the bare prefix or a deeper path as a landing', () => {
    rememberInvitationLanding('/invitations')
    expect(pendingInvitationLanding()).toBeNull()

    rememberInvitationLanding('/invitations/tok-123/settings')
    expect(pendingInvitationLanding()).toBeNull()
  })
})
