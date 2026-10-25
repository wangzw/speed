// @vitest-environment jsdom
/**
 * The human-verification client: the challenge-to-proof path and the
 * proof state a surface holds.
 *
 * The proof is checked with the provider's own rule -- SHA-256 over the
 * decoded challenge and the big-endian nonce against the difficulty -- so
 * a passing case proves the backend would accept the string. The hook
 * cases drive the real session over the shared harness: the discovery
 * read, the off fallback, the Turnstile token, and the fresh proof a
 * consumed one is replaced with.
 */

import { createHash } from 'node:crypto'
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import {
  challengeResponse,
  HUMAN_CHALLENGE,
  LOGIN_METHODS,
  makeHarness,
  methodList,
} from '@speed/test-utils/session-harness'
import {
  humanGateNeedsProof,
  powProofFromChallenge,
  requestPowProof,
  useHumanVerification,
} from './human.js'
import { decodeBase64Url } from './pow.js'

afterEach(() => {
  cleanup()
})

/** The leading zero bits of a proof's hash, recomputed as the provider
 * does it. */
function proofZeroBits(challenge: string, proof: string): number {
  const dot = proof.lastIndexOf('.')
  const nonceBytes = Buffer.alloc(8)
  nonceBytes.writeBigUInt64BE(BigInt(proof.slice(dot + 1)))
  const digest = createHash('sha256')
    .update(Buffer.from(decodeBase64Url(challenge)!))
    .update(nonceBytes)
    .digest()
  let count = 0
  for (const byte of digest) {
    if (byte !== 0) {
      return count + Math.clz32(byte) - 24
    }
    count += 8
  }
  return 256
}

describe('requestPowProof', () => {
  it('turns a challenge answer into a proof the provider accepts', async () => {
    const challenge = 'AQIDBAUGBwgJCgsMDQ4PEA'
    const answer = challengeResponse({ challenge, difficulty: 8 })
    const proof = await requestPowProof(async () => answer)
    expect(proof.slice(0, proof.lastIndexOf('.'))).toBe(challenge)
    expect(proofZeroBits(challenge, proof)).toBeGreaterThanOrEqual(8)
  })

  it('refuses an answer that names another mechanism or hash', async () => {
    await expect(
      requestPowProof(async () => ({
        ...challengeResponse(),
        provider: 'turnstile',
      })),
    ).rejects.toThrow()
    await expect(
      requestPowProof(async () => ({
        ...challengeResponse(),
        algorithm: 'sha512',
      })),
    ).rejects.toThrow()
  })
})

describe('powProofFromChallenge', () => {
  it('refuses a challenge whose difficulty is not a whole number', () => {
    expect(
      powProofFromChallenge({
        ...challengeResponse(),
        difficulty: 1.5,
      }),
    ).toBeNull()
  })
})

describe('humanGateNeedsProof', () => {
  it('needs a proof only for a required mechanism this client can satisfy', () => {
    expect(humanGateNeedsProof(null)).toBe(false)
    expect(humanGateNeedsProof({ required: false, provider: 'pow' })).toBe(false)
    expect(humanGateNeedsProof({ required: true, provider: 'pow' })).toBe(true)
    expect(
      humanGateNeedsProof({ required: true, provider: 'turnstile' }),
    ).toBe(true)
    expect(
      humanGateNeedsProof({ required: true, provider: 'something-new' }),
    ).toBe(false)
  })
})

describe('useHumanVerification', () => {
  it('adopts an injected off gate without reading the methods document', async () => {
    const harness = makeHarness({})
    const { result } = renderHook(() =>
      useHumanVerification(harness.session, {
        gate: { required: false, provider: 'off' },
      }),
    )
    await waitFor(() => expect(result.current.pending).toBe(false))
    expect(result.current.gate).toEqual({ required: false, provider: 'off' })
    expect(result.current.proof).toBeNull()
    expect(result.current.status).toBe('idle')
    expect(harness.calls).toHaveLength(0)
  })

  it('reads a proof-of-work gate from the methods document and solves it', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList([], { humanRequired: true, humanProvider: 'pow' }),
      [HUMAN_CHALLENGE]: () => challengeResponse({ difficulty: 4 }),
    })
    const { result } = renderHook(() => useHumanVerification(harness.session))
    await waitFor(() => expect(result.current.status).toBe('ready'))
    expect(result.current.gate).toEqual({ required: true, provider: 'pow' })
    expect(result.current.proof).not.toBeNull()
    expect(
      harness.calls.filter((call) => call.path === '/api/v1/auth/human/challenge'),
    ).toHaveLength(1)
  })

  it('holds the token a Turnstile widget yields and clears it when consumed', async () => {
    const harness = makeHarness({})
    const { result } = renderHook(() =>
      useHumanVerification(harness.session, {
        gate: { required: true, provider: 'turnstile', site_key: 'site-1' },
      }),
    )
    await waitFor(() => expect(result.current.pending).toBe(false))
    act(() => {
      result.current.adoptToken('token-1')
    })
    expect(result.current.proof).toBe('token-1')
    expect(result.current.status).toBe('ready')
    act(() => {
      result.current.consume()
    })
    expect(result.current.proof).toBeNull()
    expect(result.current.status).toBe('idle')
  })

  it('obtains a fresh proof after a consumed one', async () => {
    const challenges = ['AAAAAAAAAAAAAAAAAAAAAA', 'AQIDBAUGBwgJCgsMDQ4PEA']
    let issued = 0
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList([], { humanRequired: true, humanProvider: 'pow' }),
      [HUMAN_CHALLENGE]: () => {
        const challenge = challenges[Math.min(issued, challenges.length - 1)]!
        issued += 1
        return challengeResponse({ challenge, difficulty: 2 })
      },
    })
    const { result } = renderHook(() => useHumanVerification(harness.session))
    await waitFor(() => expect(result.current.status).toBe('ready'))
    const first = result.current.proof
    act(() => {
      result.current.consume()
    })
    await waitFor(() => expect(result.current.status).toBe('ready'))
    expect(issued).toBe(2)
    expect(result.current.proof).not.toBe(first)
  })
})
