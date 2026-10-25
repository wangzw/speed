/**
 * The main-thread side of the proof-of-work worker: spawn a module worker,
 * hand it one challenge, and resolve the proof it posts back.
 *
 * This module is loaded dynamically from ./human.ts only where the
 * platform has a Worker, so a server-side render and the jsdom test
 * environment never evaluate the worker URL. A worker that cannot be
 * constructed, or that fails to load, falls back to solving on the
 * calling thread: correctness first, the responsive UI second.
 */

import { solvePowChallenge } from './pow.js'
import type { PowWorkerReply, PowWorkerRequest } from './pow-worker.js'

/**
 * Solve a challenge in a Web Worker. The worker is terminated once it
 * answers, fails, or its first answer is taken, so one solve never leaves
 * a worker behind.
 */
export function solveInWorker(
  challenge: string,
  difficulty: number,
): Promise<string | null> {
  let worker: Worker
  try {
    worker = new Worker(new URL('./pow-worker.js', import.meta.url), {
      type: 'module',
    })
  } catch {
    return Promise.resolve(solvePowChallenge(challenge, difficulty))
  }
  return new Promise<string | null>((resolve) => {
    let settled = false
    const finish = (proof: string | null): void => {
      if (settled) {
        return
      }
      settled = true
      worker.terminate()
      resolve(proof)
    }
    worker.addEventListener('message', (event: MessageEvent<PowWorkerReply>) => {
      finish(event.data.proof)
    })
    worker.addEventListener('error', () => {
      // A worker that could not load (a bundler that did not copy it) must
      // not make the gate unusable: solve here instead.
      finish(solvePowChallenge(challenge, difficulty))
    })
    const request: PowWorkerRequest = { challenge, difficulty }
    worker.postMessage(request)
  })
}
