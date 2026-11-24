/** The flow vocabulary the sign-in family shares: open and settled replies, and
 * the answer step each open reply expects. */

import type { AuthnFlowState } from '@speed/auth-core'

export type OpenFlowState = Extract<AuthnFlowState, { seq: number }>

export type SettledFlowState = Exclude<AuthnFlowState, OpenFlowState>

export type AnswerStep = 'answer' | 'factor' | 'enroll' | 'complete'

export function isOpenFlow(state: AuthnFlowState): state is OpenFlowState {
  return 'seq' in state
}

export function isSettledFlow(state: AuthnFlowState): state is SettledFlowState {
  return !isOpenFlow(state)
}

/**
 * The answer step name an open reply expects. It is a function of the
 * record's live state, not the step's rendered kind: a `second` state answers
 * `factor` even when next.kind is `challenge`; `first`, `processing`, `signup`
 * and `reprove` answer `answer`, and only `ready` takes `complete`.
 */
export function answerStepFor(state: OpenFlowState): AnswerStep {
  switch (state.state) {
    case 'second':
      return 'factor'
    case 'enroll':
      return 'enroll'
    case 'ready':
      return 'complete'
    default:
      return 'answer'
  }
}

export function answerSends(
  step: OpenFlowState,
  input: Record<string, string>,
): boolean {
  switch (step.next.kind) {
    case 'factor': {
      const chosen = step.next.factors.find(
        (factor) => factor.id === input.factor,
      )
      return chosen?.kind === 'sms'
    }
    case 'challenge':
      return step.next.fields.some(
        (field) => field.kind === 'phone' && (input[field.name] ?? '') !== '',
      )
    default:
      return false
  }
}

export function stepMaySend(step: OpenFlowState): boolean {
  switch (step.next.kind) {
    case 'factor':
      return step.next.factors.some((factor) => factor.kind === 'sms')
    case 'challenge':
      return step.next.fields.some((field) => field.kind === 'phone')
    default:
      return false
  }
}
