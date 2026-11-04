// @vitest-environment jsdom
/**
 * The human-verification client: the per-surface gate, the
 * challenge-to-proof path and the proof state a surface holds.
 *
 * The proof is checked with the provider's own rule -- SHA-256 over the
 * decoded challenge and the big-endian nonce against the difficulty -- so
 * a passing case proves the backend would accept the string. The hook
 * cases drive the real session over the shared harness: the discovery
 * read, the off fallback, the per-surface selection, the Turnstile token,
 * the self-hosted captcha challenge, and the fresh proof a consumed one is
 * replaced with.
 */

import { createHash } from 'node:crypto'
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  CAPTCHA_CHALLENGE,
  apiError,
  captchaChallenge,
  challengeResponse,
  HUMAN_CHALLENGE,
  LOGIN_METHODS,
  makeHarness,
  methodList,
} from '@speed/test-utils/session-harness'
import {
  humanGateNeedsProof,
  parseCaptchaChallenge,
  powProofFromChallenge,
  requestPowProof,
  useHumanVerification,
} from './human.js'
import { decodeBase64Url } from './pow.js'

afterEach(() => {
  cleanup()
  vi.useRealTimers()
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
    const proof = await requestPowProof('other', async () => answer)
    expect(proof.slice(0, proof.lastIndexOf('.'))).toBe(challenge)
    expect(proofZeroBits(challenge, proof)).toBeGreaterThanOrEqual(8)
  })

  it('refuses an answer that names another mechanism or hash', async () => {
    await expect(
      requestPowProof('phone', async () => ({
        ...challengeResponse(),
        provider: 'turnstile',
      })),
    ).rejects.toThrow()
    await expect(
      requestPowProof('phone', async () => ({
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

/** A whole gate, both surfaces off except the members a case names. */
function gateOf(
  phone: { required: boolean; provider: string; site_key?: string },
  other: { required: boolean; provider: string; site_key?: string } = {
    required: false,
    provider: 'off',
  },
) {
  return { phone, other }
}

describe('humanGateNeedsProof', () => {
  it('reads the member of the scope it is asked about', () => {
    const gate = gateOf(
      { required: true, provider: 'pow' },
      { required: true, provider: 'turnstile', site_key: 'site-1' },
    )
    expect(humanGateNeedsProof(gate, 'phone')).toBe(true)
    expect(humanGateNeedsProof(gate, 'other')).toBe(true)

    const phoneOnly = gateOf({ required: true, provider: 'captcha' })
    expect(humanGateNeedsProof(phoneOnly, 'phone')).toBe(true)
    expect(humanGateNeedsProof(phoneOnly, 'other')).toBe(false)

    const otherOnly = gateOf(
      { required: false, provider: 'off' },
      { required: true, provider: 'pow' },
    )
    expect(humanGateNeedsProof(otherOnly, 'phone')).toBe(false)
    expect(humanGateNeedsProof(otherOnly, 'other')).toBe(true)
  })

  it('needs a proof only for a required mechanism this client can satisfy', () => {
    expect(humanGateNeedsProof(null, 'phone')).toBe(false)
    expect(
      humanGateNeedsProof(
        gateOf({ required: false, provider: 'pow' }),
        'phone',
      ),
    ).toBe(false)
    expect(
      humanGateNeedsProof(
        gateOf({ required: true, provider: 'something-new' }),
        'phone',
      ),
    ).toBe(false)
  })
})

describe('parseCaptchaChallenge', () => {
  it('reads a challenge whose form carries its media', () => {
    const answer = captchaChallenge({ form: 'slider' })
    expect(parseCaptchaChallenge(answer)).toEqual(answer)
    expect(parseCaptchaChallenge(captchaChallenge({ form: 'audio' }))).toEqual(
      captchaChallenge({ form: 'audio' }),
    )
  })

  it('refuses a body that names another provider, form or no media', () => {
    expect(() =>
      parseCaptchaChallenge({ ...captchaChallenge(), provider: 'pow' }),
    ).toThrow()
    expect(() =>
      parseCaptchaChallenge({ ...captchaChallenge(), form: 'puzzle' }),
    ).toThrow()
    expect(() =>
      parseCaptchaChallenge({ ...captchaChallenge(), id: '' }),
    ).toThrow()
    expect(() =>
      parseCaptchaChallenge({
        ...captchaChallenge(),
        image_url: undefined,
      }),
    ).toThrow()
    // A slider needs both its background and the piece that travels over
    // it; either one missing leaves the block nothing to draw.
    expect(() =>
      parseCaptchaChallenge({
        ...captchaChallenge({ form: 'slider' }),
        piece_url: undefined,
      }),
    ).toThrow()
    expect(() => parseCaptchaChallenge(null)).toThrow()
  })
})

describe('useHumanVerification', () => {
  it('adopts an injected off gate without reading the methods document', async () => {
    const harness = makeHarness({})
    const gate = gateOf({ required: false, provider: 'off' })
    const { result } = renderHook(() =>
      useHumanVerification(harness.session, { scope: 'phone', gate }),
    )
    await waitFor(() => expect(result.current.pending).toBe(false))
    expect(result.current.gate).toEqual(gate)
    expect(result.current.proof).toBeNull()
    expect(result.current.status).toBe('idle')
    expect(harness.calls).toHaveLength(0)
  })

  it('reads a proof-of-work gate for the phone surface and solves it', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList([], {
          humanPhoneProvider: 'pow',
          humanPhoneRequired: true,
          humanOtherProvider: 'turnstile',
          humanOtherRequired: true,
          humanOtherSiteKey: 'site-other',
        }),
      [HUMAN_CHALLENGE]: () => challengeResponse({ difficulty: 4 }),
    })
    const { result } = renderHook(() =>
      useHumanVerification(harness.session, { scope: 'phone' }),
    )
    await waitFor(() => expect(result.current.status).toBe('ready'))
    expect(result.current.gate?.phone).toEqual({
      required: true,
      provider: 'pow',
    })
    expect(result.current.gate?.other).toEqual({
      required: true,
      provider: 'turnstile',
      site_key: 'site-other',
    })
    expect(result.current.proof).not.toBeNull()
    const challenges = harness.calls.filter(
      (call) => call.path === '/api/v1/auth/human/challenge',
    )
    expect(challenges).toHaveLength(1)
    // The challenge names the surface it is issued for, so the proof the
    // server hands back is bound to the phone surface.
    expect(challenges[0]?.options?.query).toEqual({ scope: 'phone' })
  })

  it('solves a proof-of-work gate for the other surface with its own scope', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList([], {
          humanOtherProvider: 'pow',
          humanOtherRequired: true,
        }),
      [HUMAN_CHALLENGE]: () => challengeResponse({ difficulty: 4 }),
    })
    const { result } = renderHook(() =>
      useHumanVerification(harness.session, { scope: 'other' }),
    )
    await waitFor(() => expect(result.current.status).toBe('ready'))
    const challenges = harness.calls.filter(
      (call) => call.path === '/api/v1/auth/human/challenge',
    )
    expect(challenges).toHaveLength(1)
    expect(challenges[0]?.options?.query).toEqual({ scope: 'other' })
  })

  it('reads the other surface without solving the phone surface work', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList([], {
          humanPhoneProvider: 'pow',
          humanPhoneRequired: true,
          humanOtherProvider: 'turnstile',
          humanOtherRequired: true,
          humanOtherSiteKey: 'site-other',
        }),
    })
    const { result } = renderHook(() =>
      useHumanVerification(harness.session, { scope: 'other' }),
    )
    await waitFor(() => expect(result.current.gate).not.toBeNull())
    expect(result.current.gate?.other.provider).toBe('turnstile')
    expect(result.current.status).toBe('idle')
    expect(
      harness.calls.filter(
        (call) => call.path === '/api/v1/auth/human/challenge',
      ),
    ).toHaveLength(0)
  })

  it('renders the challenge the server chose when the request names no form', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList([], {
          humanPhoneProvider: 'captcha',
          humanPhoneRequired: true,
        }),
      [CAPTCHA_CHALLENGE]: () => captchaChallenge({ form: 'slider' }),
    })
    const { result } = renderHook(() =>
      useHumanVerification(harness.session, { scope: 'phone' }),
    )
    await waitFor(() => expect(result.current.captcha).not.toBeNull())
    // The default request carries no form: the server picks between the
    // image and slider forms, and the client renders whichever it answered.
    expect(result.current.captcha?.form).toBe('slider')
    expect(result.current.status).toBe('ready')
    expect(result.current.proof).toBeNull()
    const requests = harness.calls.filter(
      (call) => call.path === '/api/v1/auth/human/captcha/challenge',
    )
    expect(requests).toHaveLength(1)
    // No form is named, but the surface always is.
    expect(requests[0]?.options?.query).toEqual({ scope: 'phone' })
  })

  it('asks for audio through the accessibility entry and keeps it across refresh and a consumed challenge', async () => {
    const forms: (string | undefined)[] = []
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList([], {
          humanPhoneProvider: 'captcha',
          humanPhoneRequired: true,
        }),
      [CAPTCHA_CHALLENGE]: ({ options }) => {
        const form = options?.query?.form as 'audio' | undefined
        forms.push(form)
        return captchaChallenge({ form: form ?? 'image' })
      },
    })
    const { result } = renderHook(() =>
      useHumanVerification(harness.session, { scope: 'phone' }),
    )
    await waitFor(() => expect(result.current.captcha?.form).toBe('image'))
    expect(forms).toEqual([undefined])

    act(() => {
      result.current.requestAudio()
    })
    await waitFor(() => expect(result.current.captcha?.form).toBe('audio'))
    expect(forms).toEqual([undefined, 'audio'])

    // The refresh control names no form of its own: it re-requests the
    // selection in force, which is the audio alternative once chosen.
    act(() => {
      result.current.refreshCaptcha()
    })
    await waitFor(() => expect(forms).toEqual([undefined, 'audio', 'audio']))
    // The refresh put the challenge back in flight; the request only lands
    // with the form, so wait for the challenge itself before reading its id.
    await waitFor(() => expect(result.current.captcha).not.toBeNull())

    act(() => {
      result.current.adoptToken(`${result.current.captcha!.id}.12345`)
    })
    expect(result.current.proof).not.toBeNull()
    act(() => {
      result.current.consume()
    })
    await waitFor(() => expect(result.current.captcha).not.toBeNull())
    expect(forms).toEqual([undefined, 'audio', 'audio', 'audio'])
  })

  it('keeps the challenge on screen and states the wait when the audio entry is refused', async () => {
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList([], {
          humanPhoneProvider: 'captcha',
          humanPhoneRequired: true,
        }),
      [CAPTCHA_CHALLENGE]: ({ options }) => {
        if (options?.query?.form === 'audio') {
          // The accessibility entry is limited more strictly than the
          // rotation, and the refusal names the wait.
          throw apiError(429, 'authn.throttled', 42_000)
        }
        return captchaChallenge({ form: 'image' })
      },
    })
    const { result } = renderHook(() =>
      useHumanVerification(harness.session, { scope: 'phone' }),
    )
    await waitFor(() => expect(result.current.captcha?.form).toBe('image'))
    expect(result.current.audioRetryAfterSeconds).toBeUndefined()

    act(() => {
      result.current.requestAudio()
    })
    await waitFor(() => expect(result.current.audioRetryAfterSeconds).toBe(42))
    // The refusal did not spend the challenge on screen: the viewer can
    // still answer the visual one, and the wait belongs to the entry.
    expect(result.current.captcha?.form).toBe('image')
    expect(result.current.status).toBe('ready')
    expect(result.current.proof).toBeNull()
  })

  it('clears the stated wait once the audio entry obtains a challenge', async () => {
    let refuse = true
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList([], {
          humanPhoneProvider: 'captcha',
          humanPhoneRequired: true,
        }),
      [CAPTCHA_CHALLENGE]: ({ options }) => {
        const form = options?.query?.form as 'audio' | undefined
        if (form === 'audio' && refuse) {
          throw apiError(429, 'authn.throttled', 1_000)
        }
        return captchaChallenge({ form: form ?? 'image' })
      },
    })
    const { result } = renderHook(() =>
      useHumanVerification(harness.session, { scope: 'phone' }),
    )
    await waitFor(() => expect(result.current.captcha?.form).toBe('image'))
    act(() => {
      result.current.requestAudio()
    })
    await waitFor(() => expect(result.current.audioRetryAfterSeconds).toBe(1))
    refuse = false
    act(() => {
      result.current.requestAudio()
    })
    await waitFor(() => expect(result.current.captcha?.form).toBe('audio'))
    expect(result.current.audioRetryAfterSeconds).toBeUndefined()
  })

  /** A harness whose accessibility entry is always refused with the given
   * wait, while the rotation keeps issuing the visual form. */
  function refusedAudioHarness(retryAfterMs: number) {
    return makeHarness({
      [LOGIN_METHODS]: () =>
        methodList([], {
          humanPhoneProvider: 'captcha',
          humanPhoneRequired: true,
        }),
      [CAPTCHA_CHALLENGE]: ({ options }) => {
        if (options?.query?.form === 'audio') {
          throw apiError(429, 'authn.throttled', retryAfterMs)
        }
        return captchaChallenge({ form: 'image' })
      },
    })
  }

  it('counts the stated wait down after the audio entry is refused', async () => {
    // The wait is a live countdown: the message shows the time left, not
    // the number the refusal named, so consumers re-render each second.
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const harness = refusedAudioHarness(42_000)
    const { result } = renderHook(() =>
      useHumanVerification(harness.session, { scope: 'phone' }),
    )
    await waitFor(() => expect(result.current.captcha?.form).toBe('image'))
    act(() => {
      result.current.requestAudio()
    })
    await waitFor(() => expect(result.current.audioRetryAfterSeconds).toBe(42))

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000)
    })
    expect(result.current.audioRetryAfterSeconds).toBe(41)
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000)
    })
    expect(result.current.audioRetryAfterSeconds).toBe(40)
  })

  it('clears the stated wait at its deadline so the audio entry re-enables', async () => {
    // Storing the wait without expiring it left the only screen-reader
    // entry disabled forever; at the deadline it must be undefined again.
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const harness = refusedAudioHarness(42_000)
    const { result } = renderHook(() =>
      useHumanVerification(harness.session, { scope: 'phone' }),
    )
    await waitFor(() => expect(result.current.captcha?.form).toBe('image'))
    act(() => {
      result.current.requestAudio()
    })
    await waitFor(() => expect(result.current.audioRetryAfterSeconds).toBe(42))

    await act(async () => {
      await vi.advanceTimersByTimeAsync(42_000)
    })
    expect(result.current.audioRetryAfterSeconds).toBeUndefined()
    // The refusal kept the visual challenge on screen; it stays answerable.
    expect(result.current.captcha?.form).toBe('image')
    expect(result.current.status).toBe('ready')
  })

  it('offers a retry when the captcha challenge cannot be issued', async () => {
    let attempts = 0
    const harness = makeHarness({
      [LOGIN_METHODS]: () =>
        methodList([], {
          humanPhoneProvider: 'captcha',
          humanPhoneRequired: true,
        }),
      [CAPTCHA_CHALLENGE]: () => {
        attempts += 1
        if (attempts === 1) {
          throw new Error('the store is unavailable')
        }
        return captchaChallenge()
      },
    })
    const { result } = renderHook(() =>
      useHumanVerification(harness.session, { scope: 'phone' }),
    )
    await waitFor(() => expect(result.current.status).toBe('failed'))
    expect(result.current.captcha).toBeNull()
    act(() => {
      result.current.retry()
    })
    await waitFor(() => expect(result.current.status).toBe('ready'))
    expect(attempts).toBe(2)
  })

  it('holds the token a Turnstile widget yields and clears it when consumed', async () => {
    const harness = makeHarness({})
    const gate = gateOf(
      { required: false, provider: 'off' },
      { required: true, provider: 'turnstile', site_key: 'site-1' },
    )
    const { result } = renderHook(() =>
      useHumanVerification(harness.session, { scope: 'other', gate }),
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
        methodList([], {
          humanPhoneProvider: 'pow',
          humanPhoneRequired: true,
        }),
      [HUMAN_CHALLENGE]: () => {
        const challenge = challenges[Math.min(issued, challenges.length - 1)]!
        issued += 1
        return challengeResponse({ challenge, difficulty: 2 })
      },
    })
    const { result } = renderHook(() =>
      useHumanVerification(harness.session, { scope: 'phone' }),
    )
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
