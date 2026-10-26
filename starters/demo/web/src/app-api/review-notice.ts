/**
 * The review-only notice control's operation, the app's own API surface.
 *
 * The route is mounted by the review assembly's review-tagged module, so it is
 * not part of the baseline OpenAPI contract and the generated client cannot
 * carry it. Keeping the call here -- rather than in the component -- is the
 * app's usual layering: every backend call goes through the app's own API
 * module over the bound request seam. The route exists only in a review build;
 * a baseline deployment answers 404, and the home page never offers the control
 * because the feature document does not name it.
 */

import { speedRequest } from './runtime.js'

/** The review-only route the control posts to. */
export const REVIEW_NOTICE_CONTROL_PATH =
  '/api/v1/notifications/review/notice'

/** Asks the review assembly to dispatch one review notice to the caller. */
export function postReviewNotice(): Promise<void> {
  return speedRequest<void>({
    url: REVIEW_NOTICE_CONTROL_PATH,
    method: 'POST',
  })
}
