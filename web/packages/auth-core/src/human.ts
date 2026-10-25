/**
 * The client half of the human-verification gate: what a surface does when
 * the deployment requires a proof before it sends a verification message.
 *
 * Two mechanisms are possible, and the discovery document names which one
 * this deployment runs:
 *
 *  - `pow` -- the self-hosted proof of work. The client asks
 *    `POST /api/v1/auth/human/challenge` for a challenge, solves it off the
 *    main thread (./pow.ts behind ./pow-worker-client.ts) and presents the
 *    `challenge.nonce` proof. A proof is single-use, so the surface
 *    consumes it after every send and the control obtains a fresh one.
 *  - `turnstile` -- a third-party widget. Its token is obtained by the
 *    widget the ui-kit block renders; this module only stores it.
 *
 * `useHumanVerification` owns both the discovery read and the proof state,
 * so every surface obtains the gate the same way. A read that fails is
 * read as the off mechanism: the server still judges the send, and a
 * client that cannot read the document must not block a deployment that
 * may require nothing. The proof itself is never persisted -- it is a
 * short-lived one-shot value held in memory for the send that presents it.
 */

import { useCallback, useEffect, useRef, useState } from 'react'
import { authnHumanverifyChallenge } from '@speed/api-sdk'
import type { ChallengeResponse } from '@speed/api-sdk'
import type { AuthSession, AuthnHumanGate } from './session.js'
import { POW_HASH, solvePowChallenge } from './pow.js'

/** The proof state a surface renders. `working` covers both the initial
 * solve and a retry; `ready` carries a proof in `useHumanVerification`. */
export type HumanVerificationStatus = 'idle' | 'working' | 'ready' | 'failed'

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
  /** The proof-of-work state; the token mechanism reports `ready` once the
   * widget yields a token. */
  readonly status: HumanVerificationStatus
  /** Adopt a token the Turnstile widget yielded (null when it expired or
   * failed). */
  adoptToken(token: string | null): void
  /** Solve once more after a failure. */
  retry(): void
  /** Give up the current proof after presenting it, so the control obtains
   * a fresh one for the next send. */
  consume(): void
}

/** What a send must do about the gate: a proof is required only for the
 * mechanisms this client can supply one for. An unknown provider is left
 * to the server rather than blocking a surface with no way to satisfy
 * it. */
export function humanGateNeedsProof(
  gate: AuthnHumanGate | null | undefined,
): boolean {
  if (gate === null || gate === undefined || !gate.required) {
    return false
  }
  return gate.provider === 'pow' || gate.provider === 'turnstile'
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

/** The options `useHumanVerification` takes. */
export interface UseHumanVerificationOptions {
  /** A gate the caller already read (the sign-in screen's discovery
   * document), so the hook does not read it again. */
  readonly gate?: AuthnHumanGate
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
 * presents.
 *
 * A `pow` gate solves one challenge as soon as the gate is known, and
 * again after every consumed proof, so the surface always shows either a
 * ready proof or the work in progress. A `turnstile` gate waits for the
 * widget to yield a token through `adoptToken`. The off gate does nothing.
 */
export function useHumanVerification(
  session: AuthSession,
  options: UseHumanVerificationOptions = {},
): HumanVerification {
  const [gate, setGate] = useState<AuthnHumanGate | null>(
    options.gate ?? null,
  )
  const [pending, setPending] = useState(options.gate === undefined)
  const [proof, setProof] = useState<string | null>(null)
  const [status, setStatus] = useState<HumanVerificationStatus>('idle')
  // Bumped when a solve is superseded, so the effect below starts a solve
  // for the gate that replaced it instead of leaving the block waiting.
  const [solveTick, setSolveTick] = useState(0)
  // The solve in flight and the generation it belongs to: a proof that
  // settles after the gate changed (or after another solve) is dropped.
  const solvingRef = useRef(false)
  const generationRef = useRef(0)
  const injectedRef = useRef(options.gate)
  injectedRef.current = options.gate
  const injectedRequired = options.gate?.required
  const injectedProvider = options.gate?.provider
  const injectedSiteKey = options.gate?.site_key

  useEffect(() => {
    const injected = injectedRef.current
    generationRef.current += 1
    setProof(null)
    setStatus('idle')
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
          setGate({ required: false, provider: 'off' })
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
  }, [session, injectedRequired, injectedProvider, injectedSiteKey])

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

  const active = humanGateNeedsProof(gate)
  const needsSolve =
    active && gate?.provider === 'pow' && proof === null && status === 'idle'

  useEffect(() => {
    if (needsSolve) {
      void solve()
    }
  }, [needsSolve, solve, solveTick])

  const adoptToken = useCallback((token: string | null): void => {
    setProof(token)
    setStatus(token === null ? 'idle' : 'ready')
  }, [])

  const retry = useCallback((): void => {
    void solve()
  }, [solve])

  const consume = useCallback((): void => {
    setProof(null)
    setStatus('idle')
  }, [])

  return { gate, pending, proof, status, adoptToken, retry, consume }
}
