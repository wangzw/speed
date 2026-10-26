/**
 * The review build's tenant notice control.
 *
 * This is a review-assembly-only control: the route it posts to is mounted by a
 * module compiled only under the review build tag, and the home page renders
 * this component only while the feature document carries
 * `review.notification.control`, which that module alone declares. A baseline
 * binary carries neither the route nor the feature, so the component is never
 * shown and its request is never made there.
 *
 * The control exists for the SMS template rollout journey: an ordinary tenant
 * member clicks it to dispatch one review notice to themselves, and the
 * delivery pipeline addresses them on the external channel their account can
 * reach. It is visibly labelled as a review-only control rather than presented
 * as product behaviour.
 */

import type { ReactElement } from 'react'
import Button from '@mui/material/Button'
import { useTranslation } from '@speed/i18n'
import {
  SectionCard,
  WriteFeedbackRegion,
  useWriteFeedback,
} from '@speed/ui-kit'
import { postReviewNotice } from './app-api/review-notice.js'
import { DEMO_NAMESPACE } from './resources.js'

/** The review build's tenant control: one click, one notice to the caller. */
export function ReviewNoticeControl(): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const feedback = useWriteFeedback()
  const send = (): void => {
    void feedback.run(async () => {
      await postReviewNotice()
      return t('home.reviewNotice.sent')
    })
  }
  return (
    <SectionCard
      title={t('home.reviewNotice.title')}
      description={t('home.reviewNotice.description')}
      actions={
        <Button
          variant="outlined"
          onClick={send}
          disabled={feedback.pending}
        >
          {t('home.reviewNotice.action')}
        </Button>
      }
    >
      <WriteFeedbackRegion
        outcome={feedback.outcome}
        resolve={() => t('home.reviewNotice.failed')}
      />
    </SectionCard>
  )
}
