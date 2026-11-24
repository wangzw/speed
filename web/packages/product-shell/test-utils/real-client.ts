import type { AccessTokenStore } from '@speed/api-client'
import type { AuthSession } from '@speed/auth-core'
import {
  makeRealClientRig as makeRig,
} from '@speed/test-utils/real-client'
import type { ObservedRequest } from '@speed/test-utils/real-client'

export {
  errorResponse,
  jsonResponse,
} from '@speed/test-utils/real-client'
export {
  authenticatedCompletion,
  methodList,
  openFlow,
  principal,
  tokenResponse,
} from '@speed/test-utils/session-harness'

export interface RealCall {
  readonly method: string
  readonly path: string
  readonly authorization: string | null
  readonly body: string | null
}

export type RealResponder = (
  call: RealCall,
) => Response | Promise<Response>

export interface RealClientRig {
  readonly session: AuthSession
  readonly store: AccessTokenStore
  readonly calls: RealCall[]
}

function projectCall(request: ObservedRequest): RealCall {
  return {
    method: request.method,
    path: request.path,
    authorization: request.authorization,
    body: request.rawBody,
  }
}

export function makeRealClientRig(respond: RealResponder): RealClientRig {
  const { session, store, calls } = makeRig(respond, projectCall)
  return { session, store, calls }
}
