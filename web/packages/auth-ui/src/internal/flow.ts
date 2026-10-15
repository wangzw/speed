/**
 * The flow vocabulary the sign-in family shares.
 *
 * A login is a server-side flow the view advances, not one request: the
 * family begins one with session.startFlow and answers each of its
 * steps with session.answerFlow until it settles. This module holds the
 * pure pieces every channel form needs to read a flow reply the same
 * way:
 *
 *  - which reply is an open step (it carries a seq and a next) and
 *    which is a settled completion (it carries an outcome);
 *  - the answer step a given open step expects. The server pairs the
 *    answer's `step` field with the record's live state -- `answer` only
 *    in `first`, `factor` only in `second`, `enroll` only in `enroll`,
 *    `complete` only in `ready` -- so the step name is a function of the
 *    reply's state, never of the step's rendered kind. A factor's
 *    challenge (state `second`, next.kind `challenge`) is still answered
 *    with `factor`, which is exactly the distinction this reader keeps.
 */

import type { AuthnFlowState } from '@speed/auth-core'

/** An open flow: the server is waiting for the next answer. */
export type OpenFlowState = Extract<AuthnFlowState, { seq: number }>

/** A settled flow: an outcome, never an answer. */
export type SettledFlowState = Exclude<AuthnFlowState, OpenFlowState>

/** The `step` field an answer to this open step must carry. */
export type AnswerStep = 'answer' | 'factor' | 'enroll' | 'complete'

export function isOpenFlow(state: AuthnFlowState): state is OpenFlowState {
  return 'seq' in state
}

export function isSettledFlow(state: AuthnFlowState): state is SettledFlowState {
  return !isOpenFlow(state)
}

/**
 * The answer step name the given open reply expects, derived from the
 * record's live state (see the module header). `first` and the
 * transient `processing` both answer the method with `answer`; only
 * `ready` takes the result with `complete`.
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
