/**
 * session-harness.test.ts -- the fixture defaults the host's suites build
 * on. The principal's kind is what the assembled program reads to choose
 * between account and platform surfaces, so the fixture's default is
 * pinned here rather than inferred from a consumer.
 */

import { describe, expect, it } from 'vitest'
import { principal } from './session-harness.js'

describe('the harness fixture defaults', () => {
  it('builds a user-kind principal', () => {
    expect(principal().kind).toBe('user')
  })
})
