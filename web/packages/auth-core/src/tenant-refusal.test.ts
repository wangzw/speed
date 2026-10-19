/**
 * isTenantAdmissionRefusal: the admission layer's bare-404 tenant refusal.
 *
 * The distinction under test: the refusal carries no module code (the
 * client synthesizes `client.http.404` for it), while a module's own typed
 * 404 keeps its module code at the same status. The predicate reads the
 * code, so status alone never classifies.
 */

import { describe, expect, it } from 'vitest'
import { ApiError } from '@speed/api-client'
import { isTenantAdmissionRefusal } from './tenant-refusal.js'

function apiError(status: number, code: string): ApiError {
  return new ApiError({ status, code, attempts: 1 })
}

describe('isTenantAdmissionRefusal', () => {
  it('recognises the code the client synthesizes for a bare 404', () => {
    expect(isTenantAdmissionRefusal(apiError(404, 'client.http.404'))).toBe(true)
  })

  it('recognises a structural 404 with no module code', () => {
    expect(isTenantAdmissionRefusal({ status: 404 })).toBe(true)
    expect(isTenantAdmissionRefusal({ status: 404, code: 'client.http.404' })).toBe(true)
  })

  it('does not classify a module 404, however shaped', () => {
    expect(isTenantAdmissionRefusal(apiError(404, 'objectstore.not_found'))).toBe(false)
    expect(isTenantAdmissionRefusal(apiError(404, 'notification.not_found'))).toBe(false)
    expect(isTenantAdmissionRefusal({ status: 404, code: 'authn.not_found' })).toBe(false)
  })

  it('does not classify another status, even a bare one', () => {
    expect(isTenantAdmissionRefusal(apiError(403, 'client.http.403'))).toBe(false)
    expect(isTenantAdmissionRefusal(apiError(401, 'client.http.401'))).toBe(false)
    expect(isTenantAdmissionRefusal({ status: 403 })).toBe(false)
  })

  it('does not classify a value that is not an error shape', () => {
    expect(isTenantAdmissionRefusal(null)).toBe(false)
    expect(isTenantAdmissionRefusal(undefined)).toBe(false)
    expect(isTenantAdmissionRefusal('404')).toBe(false)
    expect(isTenantAdmissionRefusal(new Error('boom'))).toBe(false)
  })

  it('classifies on the synthesized code alone', () => {
    expect(isTenantAdmissionRefusal({ code: 'client.http.404' })).toBe(true)
  })
})
