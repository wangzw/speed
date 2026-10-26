/**
 * The review build's SMS verdict control.
 *
 * This is a review-assembly-only control: the route it posts to is mounted by a
 * module compiled only under the review build tag, and the SMS page renders this
 * component only while the feature document carries `review.sms.control`, which
 * that module alone declares. A baseline binary carries neither the route nor
 * the feature, so the component is never shown and its request is never made
 * there.
 *
 * The control exists for the SMS template rollout journey: the vendor's review
 * is an input a process that reaches no vendor cannot answer by itself, so a
 * person types the vendor code one language was registered under and picks the
 * verdict. It is visibly labelled as a review-only control rather than
 * presented as product behaviour, and it changes one code's verdict only.
 */

import type { ReactElement } from 'react'
import { useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import TextField from '@mui/material/TextField'
import { useFeature } from '@speed/auth-core'
import { useTranslation } from '@speed/i18n'
import {
  SectionCard,
  WriteFeedbackRegion,
  useWriteFeedback,
} from '@speed/ui-kit'
import {
  postReviewSmsVerdict,
  type ReviewSmsVerdictState,
} from './app-api/review-sms.js'
import { DEMO_NAMESPACE } from './resources.js'

/** The verdicts the review carrier accepts for one vendor code. */
const STATES: readonly ReviewSmsVerdictState[] = [
  'pending',
  'approved',
  'rejected',
  'withdrawn',
]

/** The review build's SMS verdict control. */
export function ReviewSmsControl(): ReactElement | null {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const served = useFeature('review.sms.control')
  const [code, setCode] = useState('')
  const [state, setState] = useState<ReviewSmsVerdictState>('approved')
  const feedback = useWriteFeedback()

  if (!served) {
    return null
  }

  const submit = (): void => {
    const target = code.trim()
    void feedback.run(async () => {
      await postReviewSmsVerdict({ code: target, state })
      return t('smsReview.set', {
        code: target,
        state: t(`smsReview.state.${state}`),
      })
    })
  }

  return (
    <SectionCard
      title={t('smsReview.title')}
      description={t('smsReview.description')}
      sx={{ mt: 3 }}
    >
      <Box
        sx={{
          display: 'flex',
          flexWrap: 'wrap',
          alignItems: 'flex-end',
          columnGap: 2,
          rowGap: 1,
        }}
      >
        <TextField
          size="small"
          value={code}
          onChange={(event) => setCode(event.target.value)}
          label={t('smsReview.codeLabel')}
        />
        <TextField
          select
          size="small"
          value={state}
          onChange={(event) =>
            setState(event.target.value as ReviewSmsVerdictState)
          }
          label={t('smsReview.stateLabel')}
          slotProps={{ select: { native: true } }}
          sx={{ minWidth: 160 }}
        >
          {STATES.map((option) => (
            <option key={option} value={option}>
              {t(`smsReview.state.${option}`)}
            </option>
          ))}
        </TextField>
        <Button
          type="button"
          variant="contained"
          disabled={feedback.pending}
          loading={feedback.pending}
          onClick={submit}
        >
          {t('smsReview.submit')}
        </Button>
      </Box>
      <WriteFeedbackRegion
        outcome={feedback.outcome}
        resolve={() => t('smsReview.failed')}
      />
    </SectionCard>
  )
}
