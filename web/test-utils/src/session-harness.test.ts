import { describe, expect, it } from 'vitest'
import type { HttpMethod } from '@speed/api-client'
import { speedRequest } from '@speed/api-sdk/runtime'
import { makeHarness, principal } from './session-harness'

describe('the harness fixture defaults', () => {
  it('builds a user-kind principal', () => {
    expect(principal().kind).toBe('user')
  })
})

describe('session-harness script dispatch', () => {
  it('refuses a request whose method names a prototype-chain function', async () => {
    const inheritedRuns: string[] = []
    makeHarness(
      Object.create({
        'constructor /api/v1/auth/flows': () => {
          inheritedRuns.push('dispatched')
          return undefined
        },
      }),
    )
    let error: unknown
    try {
      await speedRequest({
        url: '/api/v1/auth/flows',
        method: 'constructor' as HttpMethod,
      })
    } catch (caught) {
      error = caught
    }
    expect(inheritedRuns).toEqual([])
    expect(error).toBeInstanceOf(Error)
    expect((error as Error).message).toBe(
      'no scripted handler for constructor /api/v1/auth/flows',
    )
  })
})
