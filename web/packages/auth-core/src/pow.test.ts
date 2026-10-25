/**
 * The proof-of-work solver: the SHA-256 it computes, and the proof it
 * returns.
 *
 * The proof is checked the way the provider checks it -- the hash over the
 * decoded challenge and the nonce in big-endian, against the difficulty --
 * so the shape this client produces is the shape the server accepts.
 */

import { createHash } from 'node:crypto'
import { describe, expect, it } from 'vitest'
import {
  decodeBase64Url,
  sha256,
  solvePowChallenge,
} from './pow.js'

function hex(bytes: Uint8Array): string {
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
}

/** The number of leading zero bits a digest begins with. */
function leadingZeroBits(digest: Uint8Array): number {
  let count = 0
  for (const byte of digest) {
    if (byte !== 0) {
      return count + Math.clz32(byte) - 24
    }
    count += 8
  }
  return 256
}

/** Recompute a proof's hash the way the provider does. */
function hashOfProof(challenge: string, proof: string): Buffer {
  const dot = proof.lastIndexOf('.')
  const nonce = BigInt(proof.slice(dot + 1))
  const nonceBytes = Buffer.alloc(8)
  nonceBytes.writeBigUInt64BE(nonce)
  return createHash('sha256')
    .update(Buffer.from(decodeBase64Url(challenge)!))
    .update(nonceBytes)
    .digest()
}

describe('SHA-256', () => {
  it('matches the canonical empty and short-message vectors', () => {
    expect(hex(sha256(new Uint8Array()))).toBe(
      'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855',
    )
    expect(hex(sha256(new TextEncoder().encode('abc')))).toBe(
      'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad',
    )
  })

  it('matches the reference hash for a multi-block message', () => {
    const message = new TextEncoder().encode(
      'abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq',
    )
    const expected = createHash('sha256').update(Buffer.from(message)).digest('hex')
    expect(hex(sha256(message))).toBe(expected)
  })
})

describe('base64url decoding', () => {
  it('decodes the unpadded url alphabet', () => {
    expect(Array.from(decodeBase64Url('AQID')!)).toEqual([1, 2, 3])
    expect(decodeBase64Url('-_8')!.length).toBe(2)
  })

  it('refuses text that is not base64url', () => {
    expect(decodeBase64Url('')).toBeNull()
    expect(decodeBase64Url('a')).toBeNull()
    expect(decodeBase64Url('ab+c')).toBeNull()
  })
})

describe('solvePowChallenge', () => {
  it('returns a challenge.nonce proof the provider accepts', () => {
    const challenge = 'AAAAAAAAAAAAAAAAAAAAAA'
    const difficulty = 8
    const proof = solvePowChallenge(challenge, difficulty)
    expect(proof).not.toBeNull()
    const dot = proof!.lastIndexOf('.')
    // The challenge is carried verbatim on the left of the dot and the
    // nonce is a decimal unsigned integer on the right.
    expect(proof!.slice(0, dot)).toBe(challenge)
    expect(proof!.slice(dot + 1)).toMatch(/^\d+$/)
    expect(leadingZeroBits(hashOfProof(challenge, proof!))).toBeGreaterThanOrEqual(
      difficulty,
    )
  })

  it('reaches the difficulty from any starting challenge, including a multi-block one', () => {
    // A challenge long enough to push the padded message past one block.
    const challenge = 'A'.repeat(80)
    const proof = solvePowChallenge(challenge, 4)
    expect(proof).not.toBeNull()
    expect(leadingZeroBits(hashOfProof(challenge, proof!))).toBeGreaterThanOrEqual(
      4,
    )
  })

  it('refuses a malformed challenge or a difficulty outside the hash', () => {
    expect(solvePowChallenge('not base64url!', 4)).toBeNull()
    expect(solvePowChallenge('AAAAAAAAAAAAAAAAAAAAAA', -1)).toBeNull()
    expect(solvePowChallenge('AAAAAAAAAAAAAAAAAAAAAA', 1.5)).toBeNull()
    expect(solvePowChallenge('AAAAAAAAAAAAAAAAAAAAAA', 257)).toBeNull()
  })
})
