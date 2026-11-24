/**
 * The proof-of-work solver behind the human-verification gate's `pow`
 * mechanism: find a nonce whose SHA-256 over the decoded challenge followed by
 * the nonce in big-endian carries at least `difficulty` leading zero bits; the
 * proof is `challenge.nonce`. SHA-256 is implemented here because
 * `crypto.subtle` is asynchronous and one proof can take hundreds of
 * thousands of attempts; the solver reuses one 64-byte block when the
 * challenge fits.
 */

export const POW_HASH = 'sha256'

const BASE64URL_ALPHABET =
  'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_'

const K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1,
  0x923f82a4, 0xab1c5ed5, 0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
  0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174, 0xe49b69c1, 0xefbe4786,
  0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147,
  0x06ca6351, 0x14292967, 0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
  0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85, 0xa2bfe8a1, 0xa81a664b,
  0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a,
  0x5b9cca4f, 0x682e6ff3, 0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
  0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
])

const H0 = new Uint32Array([
  0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c,
  0x1f83d9ab, 0x5be0cd19,
])

function rotr(value: number, bits: number): number {
  return ((value >>> bits) | (value << (32 - bits))) >>> 0
}

function compress(
  state: Uint32Array,
  block: Uint8Array,
  schedule: Uint32Array,
  offset: number,
): void {
  for (let i = 0; i < 16; i += 1) {
    const at = offset + i * 4
    schedule[i] =
      ((block[at]! << 24) |
        (block[at + 1]! << 16) |
        (block[at + 2]! << 8) |
        block[at + 3]!) >>>
      0
  }
  for (let i = 16; i < 64; i += 1) {
    const w15 = schedule[i - 15]!
    const w2 = schedule[i - 2]!
    const s0 = rotr(w15, 7) ^ rotr(w15, 18) ^ (w15 >>> 3)
    const s1 = rotr(w2, 17) ^ rotr(w2, 19) ^ (w2 >>> 10)
    schedule[i] = (schedule[i - 16]! + s0 + schedule[i - 7]! + s1) >>> 0
  }
  let a = state[0]!
  let b = state[1]!
  let c = state[2]!
  let d = state[3]!
  let e = state[4]!
  let f = state[5]!
  let g = state[6]!
  let h = state[7]!
  for (let i = 0; i < 64; i += 1) {
    const s1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25)
    const ch = (e & f) ^ (~e & g)
    const temp1 = (h + s1 + ch + K[i]! + schedule[i]!) >>> 0
    const s0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22)
    const maj = (a & b) ^ (a & c) ^ (b & c)
    const temp2 = (s0 + maj) >>> 0
    h = g
    g = f
    f = e
    e = (d + temp1) >>> 0
    d = c
    c = b
    b = a
    a = (temp1 + temp2) >>> 0
  }
  state[0] = (state[0]! + a) >>> 0
  state[1] = (state[1]! + b) >>> 0
  state[2] = (state[2]! + c) >>> 0
  state[3] = (state[3]! + d) >>> 0
  state[4] = (state[4]! + e) >>> 0
  state[5] = (state[5]! + f) >>> 0
  state[6] = (state[6]! + g) >>> 0
  state[7] = (state[7]! + h) >>> 0
}

function leadingZeroBits(state: Uint32Array): number {
  let count = 0
  for (let i = 0; i < 8; i += 1) {
    const word = state[i]!
    if (word !== 0) {
      return count + Math.clz32(word)
    }
    count += 32
  }
  return 256
}

/**
 * Decodes a base64url value without padding into bytes, or returns null when
 * the text is not base64url.
 */
export function decodeBase64Url(value: string): Uint8Array | null {
  if (value.length === 0 || value.length % 4 === 1) {
    return null
  }
  const bytes: number[] = []
  let buffer = 0
  let bits = 0
  for (let i = 0; i < value.length; i += 1) {
    const index = BASE64URL_ALPHABET.indexOf(value[i]!)
    if (index < 0) {
      return null
    }
    buffer = (buffer << 6) | index
    bits += 6
    if (bits >= 8) {
      bits -= 8
      bytes.push((buffer >>> bits) & 0xff)
    }
  }
  return Uint8Array.from(bytes)
}

/**
 * SHA-256 over a byte string, returned as 32 bytes; the reference for message
 * lengths the single-block path cannot take.
 */
export function sha256(message: Uint8Array): Uint8Array {
  const bitLength = message.length * 8
  const paddedLength = (((message.length + 8) >> 6) + 1) * 64
  const padded = new Uint8Array(paddedLength)
  padded.set(message)
  padded[message.length] = 0x80
  const view = new DataView(padded.buffer)
  view.setUint32(paddedLength - 8, Math.floor(bitLength / 0x100000000))
  view.setUint32(paddedLength - 4, bitLength >>> 0)
  const state = new Uint32Array(H0)
  const schedule = new Uint32Array(64)
  for (let offset = 0; offset < paddedLength; offset += 64) {
    compress(state, padded, schedule, offset)
  }
  const digest = new Uint8Array(32)
  const digestView = new DataView(digest.buffer)
  for (let i = 0; i < 8; i += 1) {
    digestView.setUint32(i * 4, state[i]!)
  }
  return digest
}

function writeNonce(target: Uint8Array, offset: number, nonce: number): void {
  const high = Math.floor(nonce / 0x100000000)
  const low = nonce >>> 0
  target[offset] = (high >>> 24) & 0xff
  target[offset + 1] = (high >>> 16) & 0xff
  target[offset + 2] = (high >>> 8) & 0xff
  target[offset + 3] = high & 0xff
  target[offset + 4] = (low >>> 24) & 0xff
  target[offset + 5] = (low >>> 16) & 0xff
  target[offset + 6] = (low >>> 8) & 0xff
  target[offset + 7] = low & 0xff
}

/**
 * Solves one challenge into its proof, or returns null when the challenge or
 * the difficulty cannot describe work this solver can do. The search starts at
 * nonce zero and returns the first nonce that reaches the difficulty.
 */
export function solvePowChallenge(
  challenge: string,
  difficulty: number,
): string | null {
  if (
    !Number.isInteger(difficulty) ||
    difficulty < 0 ||
    difficulty > 256
  ) {
    return null
  }
  const raw = decodeBase64Url(challenge)
  if (raw === null) {
    return null
  }
  const messageLength = raw.length + 8
  if (messageLength > 55) {
    // The padded message spans more than one block: hash the general way.
    const message = new Uint8Array(messageLength)
    message.set(raw, 0)
    for (let nonce = 0; nonce <= Number.MAX_SAFE_INTEGER; nonce += 1) {
      writeNonce(message, raw.length, nonce)
      if (leadingZeroBitsBytes(sha256(message)) >= difficulty) {
        return `${challenge}.${nonce}`
      }
    }
    return null
  }
  // One 64-byte block holds the challenge, the nonce, the 0x80 marker and
  // the message length, so only the nonce bytes change between attempts.
  const block = new Uint8Array(64)
  block.set(raw, 0)
  block[messageLength] = 0x80
  const view = new DataView(block.buffer)
  const bitLength = messageLength * 8
  view.setUint32(56, Math.floor(bitLength / 0x100000000))
  view.setUint32(60, bitLength >>> 0)
  const state = new Uint32Array(8)
  const schedule = new Uint32Array(64)
  for (let nonce = 0; nonce <= Number.MAX_SAFE_INTEGER; nonce += 1) {
    view.setUint32(raw.length, Math.floor(nonce / 0x100000000))
    view.setUint32(raw.length + 4, nonce >>> 0)
    state.set(H0)
    compress(state, block, schedule, 0)
    if (leadingZeroBits(state) >= difficulty) {
      return `${challenge}.${nonce}`
    }
  }
  return null
}

function leadingZeroBitsBytes(digest: Uint8Array): number {
  let count = 0
  for (const byte of digest) {
    if (byte !== 0) {
      return count + Math.clz32(byte) - 24
    }
    count += 8
  }
  return 256
}
