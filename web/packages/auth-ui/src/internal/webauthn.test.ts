import { afterEach, describe, expect, it, vi } from 'vitest'
import { WebauthnError, runWebauthn, webauthnStepOf } from './webauthn.js'

const original = Object.getOwnPropertyDescriptor(window.navigator, 'credentials')

function buf(...bytes: number[]): ArrayBuffer {
  return Uint8Array.from(bytes).buffer
}

function stubCredentials(value: unknown): void {
  Object.defineProperty(window.navigator, 'credentials', { value, configurable: true })
}

afterEach(() => {
  if (original === undefined) {
    delete (window.navigator as { credentials?: unknown }).credentials
  } else {
    Object.defineProperty(window.navigator, 'credentials', original)
  }
  vi.restoreAllMocks()
})

describe('webauthnStepOf', () => {
  it('reads an otpauth_uri as a TOTP step', () => {
    expect(webauthnStepOf({ otpauth_uri: 'otpauth://totp/x' })).toBe('totp')
  })

  it('reads creation options (a user) as a registration', () => {
    expect(webauthnStepOf({ challenge: 'x', user: { id: 'a' } })).toBe('register')
  })

  it('reads request options (no user) as an assertion', () => {
    expect(webauthnStepOf({ challenge: 'x', allowCredentials: [] })).toBe('assert')
  })
})

describe('runWebauthn', () => {
  it('registers a credential and serializes it for the server', async () => {
    const created = {
      id: 'AQID',
      rawId: buf(1, 2, 3),
      type: 'public-key',
      response: {
        clientDataJSON: buf(4),
        attestationObject: buf(5),
        getTransports: () => ['internal'],
      },
      getClientExtensionResults: () => ({}),
      authenticatorAttachment: 'platform',
    }
    const create = vi.fn().mockResolvedValue(created)
    stubCredentials({ create, get: vi.fn() })

    const json = await runWebauthn({ challenge: 'Y2hhbGxlbmdl', user: { id: 'AQID' }, rp: {} })
    const parsed = JSON.parse(json) as {
      rawId: string
      type: string
      response: { attestationObject: string; clientDataJSON: string; transports: string[] }
    }
    expect(parsed.rawId).toBe('AQID')
    expect(parsed.type).toBe('public-key')
    expect(parsed.response.attestationObject).toBe('BQ')
    expect(parsed.response.transports).toEqual(['internal'])

    // The challenge reached create() as bytes, not as its base64url text.
    const call = create.mock.calls[0]?.[0] as {
      publicKey: { challenge: ArrayBuffer; user: { id: ArrayBuffer } }
    }
    expect(new Uint8Array(call.publicKey.challenge)).toEqual(
      new Uint8Array([...'challenge'].map((c) => c.charCodeAt(0))),
    )
    expect(new Uint8Array(call.publicKey.user.id)).toEqual(new Uint8Array([1, 2, 3]))
  })

  it('asserts a credential and serializes it for the server', async () => {
    const credential = {
      id: 'AQID',
      rawId: buf(1, 2, 3),
      type: 'public-key',
      response: {
        clientDataJSON: buf(4),
        authenticatorData: buf(5),
        signature: buf(6),
        userHandle: buf(7),
      },
      getClientExtensionResults: () => ({}),
      authenticatorAttachment: 'platform',
    }
    const get = vi.fn().mockResolvedValue(credential)
    stubCredentials({ get, create: vi.fn() })

    const json = await runWebauthn({ challenge: 'Y2hhbGxlbmdl', allowCredentials: [] })
    const parsed = JSON.parse(json) as { response: { signature: string; userHandle: string } }
    expect(parsed.response.signature).toBe('Bg')
    expect(parsed.response.userHandle).toBe('Bw')
    expect(get).toHaveBeenCalledTimes(1)
  })

  it('reports a browser without the credential API as unsupported', async () => {
    delete (window.navigator as { credentials?: unknown }).credentials
    await expect(runWebauthn({ challenge: 'x' })).rejects.toMatchObject({
      reason: 'unsupported',
    })
  })

  it('reports a dismissed prompt as cancelled', async () => {
    const create = vi
      .fn()
      .mockRejectedValue(new DOMException('the user cancelled', 'NotAllowedError'))
    stubCredentials({ create, get: vi.fn() })
    await expect(
      runWebauthn({ challenge: 'Y2hhbGxlbmdl', user: { id: 'AQID' } }),
    ).rejects.toMatchObject({ reason: 'cancelled' })
  })

  it('reports a failing ceremony as failed', async () => {
    const create = vi.fn().mockRejectedValue(new Error('boom'))
    stubCredentials({ create, get: vi.fn() })
    const failure = await runWebauthn({
      challenge: 'Y2hhbGxlbmdl',
      user: { id: 'AQID' },
    }).catch((error: unknown) => error)
    expect(failure).toBeInstanceOf(WebauthnError)
    expect((failure as WebauthnError).reason).toBe('failed')
  })

  it('refuses a TOTP step, which it does not run', async () => {
    await expect(runWebauthn({ otpauth_uri: 'otpauth://totp/x' })).rejects.toMatchObject({
      reason: 'failed',
    })
  })
})
