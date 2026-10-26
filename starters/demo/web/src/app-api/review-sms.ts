/**
 * The review-only SMS verdict control's operation, the app's own API surface.
 *
 * The route is mounted by the review assembly's review-tagged module, so it is
 * not part of the baseline OpenAPI contract and the generated client cannot
 * carry it. Keeping the call here -- rather than in the component -- is the
 * app's usual layering: every backend call goes through the app's own API
 * module over the bound request seam. The route exists only in a review build; a
 * baseline deployment answers 404, and the SMS page never offers the control
 * because the feature document does not name it.
 */

import { speedRequest } from './runtime.js'

/** The review-only route the control posts to. */
export const REVIEW_SMS_VERDICT_PATH = '/api/v1/system/sms/review/verdicts'

/** The per-template review states the review carrier accepts. */
export type ReviewSmsVerdictState =
  | 'pending'
  | 'approved'
  | 'rejected'
  | 'withdrawn'

/** One verdict the control asks the review carrier to record. */
export interface ReviewSmsVerdict {
  /** The vendor's per-language code the verdict belongs to. */
  readonly code: string
  /** The state the code moves to. */
  readonly state: ReviewSmsVerdictState
}

/** Asks the review assembly to move one vendor code's review verdict. */
export function postReviewSmsVerdict(
  verdict: ReviewSmsVerdict,
): Promise<void> {
  return speedRequest<void>({
    url: REVIEW_SMS_VERDICT_PATH,
    method: 'POST',
    data: verdict,
  })
}
