import { solvePowChallenge } from './pow.js'

export interface PowWorkerRequest {
  readonly challenge: string
  readonly difficulty: number
}

export interface PowWorkerReply {
  readonly proof: string | null
}

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
