/**
 * The demo host's SMS page: the admin package's template page with the review
 * build's verdict control beside it.
 *
 * The template page itself is the platform package's; this wrapper is the host's
 * composition point. The verdict control renders only in a review run, because
 * the review assembly alone publishes its feature; a baseline deployment shows
 * the package page unchanged.
 */

import type { ReactElement } from 'react'
import { SmsPage } from '@speed/admin-ui'
import { ReviewSmsControl } from './review-sms-control.js'

/** The admin SMS route's view in the demo host. */
export function ReviewSmsPage(): ReactElement {
  return (
    <>
      <SmsPage />
      <ReviewSmsControl />
    </>
  )
}
