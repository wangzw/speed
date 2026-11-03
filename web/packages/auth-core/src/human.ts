/**
 * The client half of the human-verification gate: what a surface does when
 * the deployment requires a proof before it sends a verification message.
 *
 * A deployment answers per surface -- the phone surface guards the four
 * SMS sends and the other surface guards every send that does not address
 * a phone number -- and may run a different mechanism on each. Three
 * mechanisms are possible, and the discovery document names which one a
 * surface runs:
 *
 *  - `pow` -- the self-hosted proof of work. The client asks
 *    `POST /api/v1/auth/human/challenge` for a challenge, solves it off the
 *    main thread (./pow.ts behind ./pow-worker-client.ts) and presents the
 *    `challenge.nonce` proof. A proof is single-use, so the surface
 *    consumes it after every send and the control obtains a fresh one.
 *  - `turnstile` -- a third-party widget. Its token is obtained by the
 *    widget the ui-kit block renders; this module only stores it.
 *  - `captcha` -- the self-hosted captcha. The default request carries no
 *    form: the server chooses between the image and slider forms and the
 *    client renders whichever it answers. Only the audio accessibility
 *    alternative is selectable, and that request names `form=audio`. The
 *    block obtains the answer from the viewer, and the proof is the
 *    challenge id and that answer joined by a dot. The challenge is
 *    single-use and good for one attempt, so a consumed proof is replaced
 *    with a fresh challenge in the same selection.
 *
 * `useHumanVerification` owns the discovery read, the proof state and the
 * captcha challenge, so every surface obtains its gate the same way. A
 * read that fails is read as the off mechanism: the server still judges
 * the send, and a client that cannot read the document must not block a
 * deployment that may require nothing. The proof itself is never
 * persisted -- it is a short-lived one-shot value held in memory for the
 * send that presents it.
 */

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

/** The surface a human-verification check belongs to: `phone` for a send
 * that delivers a message to a phone number, `other` for every send that
 * does not. */
export type HumanScope = 'phone' | 'other'

/** The proof state a surface renders. `working` covers both the initial
 * solve and a retry; `ready` carries a proof in `useHumanVerification`. */
export type HumanVerificationStatus = 'idle' | 'working' | 'ready' | 'failed'

/** The form a self-hosted captcha challenge takes: a picture of digits, a
 * slider puzzle, or the audio alternative for screen-reader users. */
export type HumanCaptchaForm = 'image' | 'slider' | 'audio'

/** The form the client may ask the server for. The image and slider forms
 * are the server's choice; only the audio accessibility alternative is
 * selectable. */
export type HumanCaptchaRequestForm = 'audio'

/** One issued self-hosted captcha challenge, as the challenge route
 * answered it: the id a proof names, the form, the instruction key the
 * block words, and the media paths the form carries. */
export type HumanCaptchaChallenge = CaptchaChallengeResponse

/** The control `useHumanVerification` hands to a surface. */
export interface HumanVerification {
  /** The deployment's gate, or null while the discovery read is in
   * flight. A failed read settles on the off gate. */
  readonly gate: AuthnHumanGate | null
  /** The discovery read is in flight. A surface that renders the block
   * before the gate is known shows nothing until it settles. */
  readonly pending: boolean
  /** The proof to present on the next send, or null while none is ready. */
  readonly proof: string | null
  /** The proof-of-work or captcha state; the token mechanism reports
   * `ready` once the widget yields a token. */
  readonly status: HumanVerificationStatus
  /** The issued self-hosted captcha challenge, or null while none is
   * loaded (or the surface does not run the captcha). */
  readonly captcha: HumanCaptchaChallenge | null
  /** Adopt a token the Turnstile widget or the captcha block yielded
   * (null when it expired or failed). */
  adoptToken(token: string | null): void
  /** Ask for another proof after a failure: a fresh solve for `pow`, a
   * fresh challenge for `captcha`. */
  retry(): void
  /** Give up the current proof after presenting it, so the control obtains
   * a fresh one for the next send. */
  consume(): void
  /** Ask for a fresh captcha challenge: the server's choice of the image
   * or slider form when no form is named, or the audio accessibility
   * alternative. The selection persists across a consumed challenge. */
  refreshCaptcha(form?: HumanCaptchaRequestForm): void
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

/** What a send must do about the gate: a proof is required only for the
 * mechanisms this client can supply one for. An unknown provider is left
 * to the server rather than blocking a surface with no way to satisfy
 * it. */
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

/**
 * Turn a challenge answer into a proof, or null when the answer is not a
 * proof-of-work challenge this solver understands. The answer's `provider`
 * and `algorithm` are checked before any work: a deployment that changed
 * mechanisms mid-flight must not be solved as work.
 */
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
 * Ask for one challenge and solve it into the proof a send presents. The
 * challenge request is the generated operation, so the host's bound
 * transport (base URL, credentials, retry) applies as it does to every
 * other call. The solve runs off the main thread where the platform has
 * a Worker. `requestChallenge` is injectable so a test can drive the
 * answer without a network.
 */
export async function requestPowProof(
  requestChallenge: () => Promise<ChallengeResponse> = () =>
    authnHumanverifyChallenge(),
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

/** The captcha forms the challenge route draws. */
const CAPTCHA_FORMS = new Set<string>(['image', 'slider', 'audio'])

/** The media paths a form must carry: an image its picture, a slider both
 * the background and the piece that travels over it, audio its recording. */
const CAPTCHA_MEDIA: Record<
  HumanCaptchaForm,
  readonly (keyof CaptchaChallengeResponse)[]
> = {
  image: ['image_url'],
  slider: ['background_url', 'piece_url'],
  audio: ['audio_url'],
}

/**
 * Read a captcha challenge answer, refusing anything the block could not
 * render as a contract violation before the first property access. A form
 * whose media path is missing is refused too: the block would otherwise
 * render a control with nothing in it.
 */
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
  /** The surface this surface sends on: the phone surface for a message
   * delivered to a phone number, the other surface for every send that
   * does not address one. */
  readonly scope: HumanScope
  /** A gate the caller already read (the sign-in screen's discovery
   * document), so the hook does not read it again. */
  readonly gate?: AuthnHumanGate
  /** Whether the hook should read the gate and solve. Defaults to true; a
   * caller that holds an inactive surface (a closed dialog, a challenge
   * that has not opened) passes false so nothing is read or solved until
   * it is needed. */
  readonly enabled?: boolean
}

/** A stable key of a whole gate, so an inline object a caller passes does
 * not restart the read on every render. */
function gateKey(gate: AuthnHumanGate | undefined): string | undefined {
  if (gate === undefined) {
    return undefined
  }
  const surface = (s: AuthnHumanSurfaceGate): string =>
    `${s.required}:${s.provider}:${s.site_key ?? ''}`
  return `${surface(gate.phone)}|${surface(gate.other)}`
}

/**
 * Solve a challenge off the main thread. The worker module is imported
 * only where the platform has a Worker, so a server render and jsdom never
 * evaluate the worker URL; a platform without one solves here.
 */
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
 * Read the deployment's human-verification gate and hold the proof a send
 * presents on one surface.
 *
 * A `pow` gate solves one challenge as soon as the gate is known, and
 * again after every consumed proof, so the surface always shows either a
 * ready proof or the work in progress. A `captcha` gate obtains one
 * challenge -- the server's choice of form unless the screen selected the
 * audio alternative -- and replaces it after every consumed proof in the
 * same selection, so the block always has a challenge to render or a
 * reason it could not. A `turnstile` gate waits for the widget to yield a
 * token through `adoptToken`. The off gate does nothing.
 *
 * A change of scope selects the other surface's gate: the proof and any
 * challenge belong to the surface that issued them, so both are dropped
 * and the new surface obtains its own.
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
  // Bumped when a solve is superseded, so the effect below starts a solve
  // for the gate that replaced it instead of leaving the block waiting.
  const [solveTick, setSolveTick] = useState(0)
  // The solve in flight and the generation it belongs to: a proof that
  // settles after the gate changed (or after another solve) is dropped.
  const solvingRef = useRef(false)
  const generationRef = useRef(0)
  const injectedRef = useRef(options.gate)
  injectedRef.current = options.gate
  const injectedKey = gateKey(options.gate)
  // The form the screen asked for, or undefined for the server's choice
  // between the image and slider forms. Only audio is selectable.
  const lastCaptchaForm = useRef<HumanCaptchaRequestForm | undefined>(undefined)

  // A change of scope selects the other member of the same document. The
  // proof and the challenge belong to the surface that issued them, so
  // they are dropped and the new surface obtains its own; the document is
  // already read and is not read again.
  useEffect(() => {
    generationRef.current += 1
    setProof(null)
    setStatus('idle')
    setCaptcha(null)
  }, [scope])

  useEffect(() => {
    generationRef.current += 1
    setProof(null)
    setStatus('idle')
    setCaptcha(null)
    if (!enabled) {
      // An inactive surface reads nothing and solves nothing; it settles on
      // the off gate until it opens.
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
        // The server still judges the send; a document this client cannot
        // read must not block one that may require nothing.
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
      const next = await requestPowProof()
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
        // The gate changed while this solve ran. Ask for one for the gate
        // that replaced it rather than leaving the block waiting forever.
        setSolveTick((tick) => tick + 1)
      }
    }
  }, [])

  const requestCaptcha = useCallback(
    async (form?: HumanCaptchaRequestForm): Promise<void> => {
      lastCaptchaForm.current = form
      const generation = generationRef.current
      setProof(null)
      setCaptcha(null)
      setStatus('working')
      try {
        // A default request carries no form: the server chooses between the
        // image and slider forms.
        const answer = await authnHumanverifyCaptchaChallenge(
          form === undefined ? undefined : { form },
        )
        if (generationRef.current !== generation) {
          return
        }
        setCaptcha(parseCaptchaChallenge(answer))
        setStatus('ready')
      } catch {
        if (generationRef.current !== generation) {
          return
        }
        setCaptcha(null)
        setStatus('failed')
      }
    },
    [],
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
    // A captcha challenge is spent with the send that presented it, so the
    // next send needs a new one; dropping it lets the effect obtain it.
    setCaptcha(null)
  }, [])

  const refreshCaptcha = useCallback(
    (form?: HumanCaptchaRequestForm): void => {
      void requestCaptcha(form ?? lastCaptchaForm.current)
    },
    [requestCaptcha],
  )

  return {
    gate,
    pending,
    proof,
    status,
    captcha,
    adoptToken,
    retry,
    consume,
    refreshCaptcha,
  }
}
