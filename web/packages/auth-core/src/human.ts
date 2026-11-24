import { useCallback, useEffect, useRef, useState } from 'react'
import {
  authnHumanverifyCaptchaChallenge,
  authnHumanverifyChallenge,
} from '@speed/api-sdk'
import type { CaptchaChallengeResponse, ChallengeResponse } from '@speed/api-sdk'
import type {
  AuthSession,
  AuthnHumanGate,
  AuthnHumanSurfaceGate,
} from './session.js'
import { POW_HASH, solvePowChallenge } from './pow.js'

/** The surface a human-verification check belongs to: `phone` for a send that delivers a message to a phone number, `other` for every send that does not. */
export type HumanScope = 'phone' | 'other'

/** The proof state a surface renders; `ready` carries a proof in `useHumanVerification`. */
export type HumanVerificationStatus = 'idle' | 'working' | 'ready' | 'failed'

/** The form a self-hosted captcha challenge takes: an image, a slider puzzle, or the audio alternative. */
export type HumanCaptchaForm = 'image' | 'slider' | 'audio'

/** One issued self-hosted captcha challenge, as the challenge route answered it. */
export type HumanCaptchaChallenge = CaptchaChallengeResponse

/** The control `useHumanVerification` hands to a surface. */
export interface HumanVerification {
  /** The deployment's gate, or null while the discovery read is in flight; a failed read settles on the off gate. */
  readonly gate: AuthnHumanGate | null
  /** The discovery read is in flight. */
  readonly pending: boolean
  /** The proof to present on the next send, or null while none is ready. */
  readonly proof: string | null
  /** The proof-of-work or captcha state. */
  readonly status: HumanVerificationStatus
  /** The issued self-hosted captcha challenge, or null while none is loaded. */
  readonly captcha: HumanCaptchaChallenge | null
  /** The wait a refused accessibility request stated, in whole seconds; undefined once it elapses. */
  readonly audioRetryAfterSeconds: number | undefined
  /** Adopt a token the Turnstile widget or the captcha block yielded (null when it expired or failed). */
  adoptToken(token: string | null): void
  /** Ask for another proof after a failure. */
  retry(): void
  /** Give up the current proof after presenting it, so the control obtains a fresh one for the next send. */
  consume(): void
  /** Ask for a fresh captcha challenge in the selection in force. */
  refreshCaptcha(): void
  /** The accessibility entry: ask for the audio alternative explicitly. */
  requestAudio(): void
}

/** The gate one surface runs, or null when the document is not readable. */
export function humanSurfaceGate(
  gate: AuthnHumanGate | null | undefined,
  scope: HumanScope,
): AuthnHumanSurfaceGate | null {
  if (gate === null || gate === undefined) {
    return null
  }
  const surface = gate[scope]
  return surface === undefined ? null : surface
}

/** Whether a send must present a proof for the gate: only the mechanisms this client can satisfy. */
export function humanGateNeedsProof(
  gate: AuthnHumanGate | null | undefined,
  scope: HumanScope,
): boolean {
  const surface = humanSurfaceGate(gate, scope)
  if (surface === null || !surface.required) {
    return false
  }
  return (
    surface.provider === 'pow' ||
    surface.provider === 'turnstile' ||
    surface.provider === 'captcha'
  )
}

/** Turns a challenge answer into a proof, or null when it is not a solvable proof-of-work challenge. */
export function powProofFromChallenge(answer: ChallengeResponse): string | null {
  if (
    answer.provider !== 'pow' ||
    answer.algorithm !== POW_HASH ||
    typeof answer.challenge !== 'string' ||
    answer.challenge === '' ||
    !Number.isInteger(answer.difficulty)
  ) {
    return null
  }
  return solvePowChallenge(answer.challenge, answer.difficulty)
}

/**
 * Asks for one challenge for a surface and solves it into the proof a send
 * presents; the server binds the proof to the scope. `requestChallenge` is
 * injectable so a test can drive the answer without a network.
 */
export async function requestPowProof(
  scope: HumanScope,
  requestChallenge: () => Promise<ChallengeResponse> = () =>
    authnHumanverifyChallenge({ scope }),
): Promise<string> {
  const answer = await requestChallenge()
  if (
    answer.provider !== 'pow' ||
    answer.algorithm !== POW_HASH ||
    typeof answer.challenge !== 'string' ||
    answer.challenge === '' ||
    !Number.isInteger(answer.difficulty)
  ) {
    throw new Error(
      'auth-core: the human challenge answer is not a solvable proof-of-work challenge',
    )
  }
  const proof = await solveChallenge(answer.challenge, answer.difficulty)
  if (proof === null) {
    throw new Error('auth-core: the human challenge could not be solved')
  }
  return proof
}

const CAPTCHA_FORMS = new Set<string>(['image', 'slider', 'audio'])

const CAPTCHA_MEDIA: Record<
  HumanCaptchaForm,
  readonly (keyof CaptchaChallengeResponse)[]
> = {
  image: ['image_url'],
  slider: ['background_url', 'piece_url'],
  audio: ['audio_url'],
}

/** Reads a captcha challenge answer, refusing a body the block could not render. */
export function parseCaptchaChallenge(
  body: unknown,
): HumanCaptchaChallenge {
  if (typeof body !== 'object' || body === null) {
    throw new Error('auth-core: the captcha challenge answer is not a document')
  }
  const answer = body as Record<string, unknown>
  if (answer.provider !== 'captcha') {
    throw new Error('auth-core: the captcha challenge names another provider')
  }
  const form = answer.form
  if (typeof form !== 'string' || !CAPTCHA_FORMS.has(form)) {
    throw new Error('auth-core: the captcha challenge names an unknown form')
  }
  if (typeof answer.id !== 'string' || answer.id === '') {
    throw new Error('auth-core: the captcha challenge carries no id')
  }
  if (typeof answer.instruction !== 'string' || answer.instruction === '') {
    throw new Error('auth-core: the captcha challenge carries no instruction')
  }
  if (!Number.isInteger(answer.expires_in)) {
    throw new Error('auth-core: the captcha challenge carries no lifetime')
  }
  for (const media of CAPTCHA_MEDIA[form as HumanCaptchaForm]) {
    if (typeof answer[media] !== 'string' || answer[media] === '') {
      throw new Error(
        `auth-core: the captcha challenge carries no ${media} for its form`,
      )
    }
  }
  return answer as unknown as HumanCaptchaChallenge
}

/** The options `useHumanVerification` takes. */
export interface UseHumanVerificationOptions {
  /** The surface the hook sends on. */
  readonly scope: HumanScope
  /** A gate the caller already read, so the hook does not read it again. */
  readonly gate?: AuthnHumanGate
  /** Whether the hook should read the gate and solve; defaults to true. */
  readonly enabled?: boolean
}

/** A stable key of a whole gate, so an inline object does not restart the read. */
function gateKey(gate: AuthnHumanGate | undefined): string | undefined {
  if (gate === undefined) {
    return undefined
  }
  const surface = (s: AuthnHumanSurfaceGate): string =>
    `${s.required}:${s.provider}:${s.site_key ?? ''}:${s.action ?? ''}`
  return `${surface(gate.phone)}|${surface(gate.other)}`
}

function statedWaitSeconds(error: unknown): number | undefined {
  if (typeof error !== 'object' || error === null) {
    return undefined
  }
  let status: unknown
  let wait: unknown
  try {
    status = (error as { status?: unknown }).status
    wait = (error as { retryAfterMs?: unknown }).retryAfterMs
  } catch {
    return undefined
  }
  if (status !== 429 || typeof wait !== 'number' || !Number.isFinite(wait)) {
    return undefined
  }
  return Math.max(1, Math.ceil(wait / 1000))
}

async function solveChallenge(
  challenge: string,
  difficulty: number,
): Promise<string | null> {
  if (typeof Worker === 'undefined') {
    return solvePowChallenge(challenge, difficulty)
  }
  try {
    const { solveInWorker } = await import('./pow-worker-client.js')
    return await solveInWorker(challenge, difficulty)
  } catch {
    return solvePowChallenge(challenge, difficulty)
  }
}

/**
 * Reads the deployment's human-verification gate and holds the proof a send
 * presents on one surface. A scope change drops the proof and challenge of the
 * old surface and the new surface obtains its own.
 */
export function useHumanVerification(
  session: AuthSession,
  options: UseHumanVerificationOptions,
): HumanVerification {
  const scope = options.scope
  const enabled = options.enabled ?? true
  const [gate, setGate] = useState<AuthnHumanGate | null>(
    options.gate ?? null,
  )
  const [pending, setPending] = useState(enabled && options.gate === undefined)
  const [proof, setProof] = useState<string | null>(null)
  const [status, setStatus] = useState<HumanVerificationStatus>('idle')
  const [captcha, setCaptcha] = useState<HumanCaptchaChallenge | null>(null)
  const [audioRetryAfterSeconds, setAudioRetryAfterSeconds] = useState<
    number | undefined
  >(undefined)
  // The wait's deadline in epoch ms, kept in a ref so a late timer reads the true remainder.
  const audioRetryDeadlineRef = useRef<number | null>(null)
  const captchaRef = useRef<HumanCaptchaChallenge | null>(null)
  captchaRef.current = captcha
  const [solveTick, setSolveTick] = useState(0)
  // The solve in flight and its generation: a proof that settles after the gate changed is dropped.
  const solvingRef = useRef(false)
  const generationRef = useRef(0)
  const injectedRef = useRef(options.gate)
  injectedRef.current = options.gate
  const injectedKey = gateKey(options.gate)
  const lastCaptchaForm = useRef<'audio' | undefined>(undefined)

  useEffect(() => {
    generationRef.current += 1
    setProof(null)
    setStatus('idle')
    setCaptcha(null)
    audioRetryDeadlineRef.current = null
    setAudioRetryAfterSeconds(undefined)
  }, [scope])

  useEffect(() => {
    generationRef.current += 1
    setProof(null)
    setStatus('idle')
    setCaptcha(null)
    audioRetryDeadlineRef.current = null
    setAudioRetryAfterSeconds(undefined)
    if (!enabled) {
      setGate(null)
      setPending(false)
      return
    }
    const injected = injectedRef.current
    if (injected !== undefined) {
      setGate(injected)
      setPending(false)
      return
    }
    let active = true
    setGate(null)
    setPending(true)
    void (async () => {
      try {
        const methods = await session.listMethods()
        if (active) {
          setGate(methods.human)
        }
      } catch {
        if (active) {
          setGate({
            phone: { required: false, provider: 'off' },
            other: { required: false, provider: 'off' },
          })
        }
      } finally {
        if (active) {
          setPending(false)
        }
      }
    })()
    return () => {
      active = false
    }
  }, [session, injectedKey, enabled])

  // Counts the stated wait down once a second; the deadline is a ref so a late tick reports the true remainder.
  const audioWaitActive = audioRetryAfterSeconds !== undefined
  useEffect(() => {
    if (!audioWaitActive) {
      return undefined
    }
    const timer = setInterval(() => {
      const deadline = audioRetryDeadlineRef.current
      if (deadline === null) {
        return
      }
      const remaining = Math.ceil((deadline - Date.now()) / 1000)
      if (remaining <= 0) {
        audioRetryDeadlineRef.current = null
        setAudioRetryAfterSeconds(undefined)
        return
      }
      setAudioRetryAfterSeconds(remaining)
    }, 1000)
    return () => {
      clearInterval(timer)
    }
  }, [audioWaitActive])

  const surface = humanSurfaceGate(gate, scope)
  const provider = surface?.provider ?? 'off'
  const required = surface?.required === true

  const solve = useCallback(async (): Promise<void> => {
    if (solvingRef.current) {
      return
    }
    solvingRef.current = true
    const generation = generationRef.current
    setStatus('working')
    try {
      const next = await requestPowProof(scope)
      if (generationRef.current !== generation) {
        return
      }
      setProof(next)
      setStatus('ready')
    } catch {
      if (generationRef.current !== generation) {
        return
      }
      setProof(null)
      setStatus('failed')
    } finally {
      solvingRef.current = false
      if (generationRef.current !== generation) {
        // The gate changed while this solve ran: solve for the gate that replaced it.
        setSolveTick((tick) => tick + 1)
      }
    }
  }, [scope])

  const requestCaptcha = useCallback(
    async (form?: 'audio'): Promise<void> => {
      lastCaptchaForm.current = form
      const generation = generationRef.current
      // A refused accessibility entry must not spend the challenge on screen.
      const kept = form === 'audio' ? captchaRef.current : null
      setProof(null)
      setCaptcha(null)
      setStatus('working')
      audioRetryDeadlineRef.current = null
      setAudioRetryAfterSeconds(undefined)
      try {
        const answer = await authnHumanverifyCaptchaChallenge(
          form === undefined ? { scope } : { form, scope },
        )
        if (generationRef.current !== generation) {
          return
        }
        setCaptcha(parseCaptchaChallenge(answer))
        setStatus('ready')
      } catch (error) {
        if (generationRef.current !== generation) {
          return
        }
        const wait = kept === null ? undefined : statedWaitSeconds(error)
        if (kept !== null && wait !== undefined) {
          setCaptcha(kept)
          setStatus('ready')
          audioRetryDeadlineRef.current = Date.now() + wait * 1000
          setAudioRetryAfterSeconds(wait)
          return
        }
        setCaptcha(null)
        setStatus('failed')
      }
    },
    [scope],
  )

  const needsSolve =
    enabled &&
    required &&
    provider === 'pow' &&
    proof === null &&
    status === 'idle'

  const needsCaptcha =
    enabled &&
    required &&
    provider === 'captcha' &&
    proof === null &&
    captcha === null &&
    status === 'idle'

  useEffect(() => {
    if (needsSolve) {
      void solve()
    }
  }, [needsSolve, solve, solveTick])

  useEffect(() => {
    if (needsCaptcha) {
      void requestCaptcha(lastCaptchaForm.current)
    }
  }, [needsCaptcha, requestCaptcha])

  const adoptToken = useCallback((token: string | null): void => {
    setProof(token)
    setStatus(token === null ? 'idle' : 'ready')
  }, [])

  const retry = useCallback((): void => {
    if (provider === 'captcha') {
      void requestCaptcha(lastCaptchaForm.current)
      return
    }
    void solve()
  }, [provider, requestCaptcha, solve])

  const consume = useCallback((): void => {
    setProof(null)
    setStatus('idle')
    // A captcha challenge is spent with the send that presented it.
    setCaptcha(null)
    audioRetryDeadlineRef.current = null
    setAudioRetryAfterSeconds(undefined)
  }, [])

  const refreshCaptcha = useCallback((): void => {
    void requestCaptcha(lastCaptchaForm.current)
  }, [requestCaptcha])

  const requestAudio = useCallback((): void => {
    void requestCaptcha('audio')
  }, [requestCaptcha])

  return {
    gate,
    pending,
    proof,
    status,
    captcha,
    audioRetryAfterSeconds,
    adoptToken,
    retry,
    consume,
    refreshCaptcha,
    requestAudio,
  }
}
