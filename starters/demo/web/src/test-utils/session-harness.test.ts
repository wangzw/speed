import { describe, expect, it } from 'vitest'
import { principal } from './session-harness.js'

describe('the harness fixture defaults', () => {
  it('builds a user-kind principal', () => {
    expect(principal().kind).toBe('user')
  })
})
