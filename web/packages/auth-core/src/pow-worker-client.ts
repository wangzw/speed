import { solvePowChallenge } from './pow.js'
import type { PowWorkerReply, PowWorkerRequest } from './pow-worker.js'

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
      finish(solvePowChallenge(challenge, difficulty))
    })
    const request: PowWorkerRequest = { challenge, difficulty }
    worker.postMessage(request)
  })
}
