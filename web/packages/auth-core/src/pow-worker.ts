/**
 * The proof-of-work worker: solves one challenge off the main thread and
 * posts the proof back.
 *
 * The request and reply shapes are this module's own; the solver in
 * ./pow.ts is the only logic here, so the worker and the main-thread
 * fallback cannot drift.
 */

import { solvePowChallenge } from './pow.js'

/** One solve request: the challenge text and the work it takes. */
export interface PowWorkerRequest {
  readonly challenge: string
  readonly difficulty: number
}

/** The worker's answer: the proof, or null when the challenge cannot be
 * solved. */
export interface PowWorkerReply {
  readonly proof: string | null
}

/** The part of the dedicated-worker global this module uses. DOM's `self`
 * is a Window, so the worker-only members are named as the worker sees
 * them. */
const scope = self as unknown as {
  addEventListener(
    type: 'message',
    listener: (event: MessageEvent<PowWorkerRequest>) => void,
  ): void
  postMessage(message: PowWorkerReply): void
}

scope.addEventListener('message', (event) => {
  const { challenge, difficulty } = event.data
  scope.postMessage({ proof: solvePowChallenge(challenge, difficulty) })
})
