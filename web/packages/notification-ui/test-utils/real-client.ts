import { makeRealClientRig as makeRig } from '@speed/test-utils/real-client'
import type { ObservedRequest } from '@speed/test-utils/real-client'

export {
  errorResponse,
  jsonResponse,
  signInWithPassword,
} from '@speed/test-utils/real-client'

export interface RealCall {
  readonly method: string
  readonly path: string
  readonly query: string
  readonly authorization: string | null
  readonly body: unknown
}

export type RealResponder = (call: RealCall) => Response | Promise<Response>

function projectCall(request: ObservedRequest): RealCall {
  let body: unknown = null
  if (request.rawBody !== null && request.rawBody !== '') {
    try {
      body = JSON.parse(request.rawBody)
    } catch {
      body = null
    }
  }
  return {
    method: request.method,
    path: request.path,
    query: request.query,
    authorization: request.authorization,
    body,
  }
}

export function makeRealClientRig(respond: RealResponder) {
  return makeRig(respond, projectCall)
}

export function makeRealClientRigWithoutSession(respond: RealResponder) {
  return makeRig(respond, projectCall, { refresh: false })
}
