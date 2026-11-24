// @vitest-environment jsdom

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

  it('reads a non-object as an assertion', () => {
    expect(webauthnStepOf(null)).toBe('assert')
    expect(webauthnStepOf('x')).toBe('assert')
  })
})

describe('runWebauthn', () => {
  it('registers a credential and serializes it for the server', async () => {
    const created = {
      id: 'AQID',
      rawId: buf(1, 2, 3),
      type: 'public-key',
      response: {
        clientDataJSON: buf(4, 5, 6),
        attestationObject: buf(7, 8, 9),
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
    expect(parsed.response.clientDataJSON).toBe('BAUG')
    expect(parsed.response.attestationObject).toBe('BwgJ')
    expect(parsed.response.transports).toEqual(['internal'])

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
        clientDataJSON: buf(4, 5, 6),
        authenticatorData: buf(7, 8, 9),
        signature: buf(10, 11),
        userHandle: buf(10, 11),
      },
      getClientExtensionResults: () => ({}),
      authenticatorAttachment: 'platform',
    }
    const get = vi.fn().mockResolvedValue(credential)
    stubCredentials({ get, create: vi.fn() })

    const json = await runWebauthn({ challenge: 'Y2hhbGxlbmdl', allowCredentials: [] })
    const parsed = JSON.parse(json) as {
      response: { authenticatorData: string; signature: string; userHandle: string }
    }
    expect(parsed.response.authenticatorData).toBe('BwgJ')
    expect(parsed.response.signature).toBe('Cgs')
    expect(parsed.response.userHandle).toBe('Cgs')
    expect(get).toHaveBeenCalledTimes(1)
  })

  it('omits userHandle when the assertion carries none', async () => {
    const credential = {
      id: 'AQID',
      rawId: buf(1, 2, 3),
      type: 'public-key',
      response: {
        clientDataJSON: buf(4, 5, 6),
        authenticatorData: buf(7, 8, 9),
        signature: buf(10, 11),
        userHandle: null,
      },
      getClientExtensionResults: () => ({}),
      authenticatorAttachment: null,
    }
    stubCredentials({ get: vi.fn().mockResolvedValue(credential), create: vi.fn() })

    const json = await runWebauthn({ challenge: 'Y2hhbGxlbmdl' })
    const parsed = JSON.parse(json) as { response: Record<string, unknown> }
    expect(parsed.response).not.toHaveProperty('userHandle')
  })

  it('hands the caller an abort signal it can give up the ceremony with', () => {
    const get = vi.fn().mockReturnValue(new Promise(() => {}))
    stubCredentials({ get, create: vi.fn() })
    const controller = new AbortController()
    void runWebauthn({ challenge: 'Y2hhbGxlbmdl' }, controller.signal)
    const call = get.mock.calls[0]?.[0] as { signal?: AbortSignal }
    expect(call.signal).toBe(controller.signal)
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

  it('reports an aborted ceremony as cancelled', async () => {
    const get = vi.fn().mockRejectedValue(new DOMException('aborted', 'AbortError'))
    stubCredentials({ get, create: vi.fn() })
    await expect(runWebauthn({ challenge: 'Y2hhbGxlbmdl' })).rejects.toMatchObject({
      reason: 'cancelled',
    })
  })

  it('reports an empty credential as cancelled', async () => {
    const create = vi.fn().mockResolvedValue(null)
    stubCredentials({ create, get: vi.fn() })
    await expect(
      runWebauthn({ challenge: 'Y2hhbGxlbmdl', user: { id: 'AQID' } }),
    ).rejects.toMatchObject({ reason: 'cancelled' })
  })

  it('reports a browser without support as unsupported', async () => {
    const create = vi.fn().mockRejectedValue(new DOMException('no', 'NotSupportedError'))
    stubCredentials({ create, get: vi.fn() })
    await expect(
      runWebauthn({ challenge: 'Y2hhbGxlbmdl', user: { id: 'AQID' } }),
    ).rejects.toMatchObject({ reason: 'unsupported' })
  })

  it('reports a credential the authenticator already holds as already-registered', async () => {
    const create = vi
      .fn()
      .mockRejectedValue(
        new DOMException('the authenticator already holds this credential', 'InvalidStateError'),
      )
    stubCredentials({ create, get: vi.fn() })
    await expect(
      runWebauthn({
        challenge: 'Y2hhbGxlbmdl',
        user: { id: 'AQID' },
        excludeCredentials: [{ id: 'AQID' }],
      }),
    ).rejects.toMatchObject({ reason: 'already-registered' })
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

  it('refuses options whose binary fields are not base64url strings', async () => {
    stubCredentials({ create: vi.fn(), get: vi.fn() })
    await expect(
      runWebauthn({ challenge: 42, user: { id: 'AQID' } }),
    ).rejects.toMatchObject({ reason: 'failed' })
  })
})
