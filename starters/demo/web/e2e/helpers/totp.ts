import { createHmac } from 'node:crypto'

const BASE32 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'

function decodeBase32(secret: string): Buffer {
  const normalized = secret.toUpperCase().replace(/=+$/u, '')
  const bytes: number[] = []
  let buffer = 0
  let bits = 0
  for (const character of normalized) {
    const value = BASE32.indexOf(character)
    if (value === -1) {
      throw new Error(`the TOTP seed carries the non-base32 character ${character}`)
    }
    buffer = (buffer << 5) | value
    bits += 5
    if (bits >= 8) {
      bits -= 8
      bytes.push((buffer >>> bits) & 0xff)
    }
  }
  return Buffer.from(bytes)
}

// The instant is truncated to its thirty-second step, which the server verifies against.
export function totpCode(
  secret: string,
  at: Date = new Date(),
  stepSeconds = 30,
  digits = 6,
): string {
  const counter = BigInt(Math.floor(at.getTime() / 1000 / stepSeconds))
  const message = Buffer.alloc(8)
  message.writeBigUInt64BE(counter)
  const digest = createHmac('sha1', decodeBase32(secret)).update(message).digest()
  const last = digest[digest.length - 1]
  if (last === undefined) {
    throw new Error('the TOTP digest is empty')
  }
  const offset = last & 0x0f
  const part = (index: number): number => digest[offset + index] ?? 0
  const binary =
    ((part(0) & 0x7f) << 24) | (part(1) << 16) | (part(2) << 8) | part(3)
  return String(binary % 10 ** digits).padStart(digits, '0')
}
