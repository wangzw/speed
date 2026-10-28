/**
 * FlowExpired: the terminal notice a username step becomes when its flow
 * is gone.
 *
 * A username answer refused as `authn.flow_invalid` means the flow expired,
 * was completed elsewhere or is bound to another browser. There is no step
 * left to retry, so the surface replaces the step with one notice and one
 * action: the action returns to the channel's first step and starts a new
 * flow, and never resends the answer that the dead flow refused.
 */

import Box from '@mui/material/Box'
import Alert from '@mui/material/Alert'
import Button from '@mui/material/Button'
import { useAuthUiTranslation } from './translation.js'

export interface FlowExpiredProps {
  /** Returns to the channel's first step and starts a new flow. */
  readonly onRestart: () => void
}

/** The flow-gone notice: one sentence and one restart action. */
export function FlowExpired({ onRestart }: FlowExpiredProps) {
  const { t } = useAuthUiTranslation()
  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'flex-start',
        gap: 1.5,
        width: '100%',
      }}
    >
      <Alert severity="warning" role="alert" sx={{ width: '100%' }}>
        {t('signInFlow.flowExpired')}
      </Alert>
      <Button variant="contained" onClick={onRestart}>
        {t('signInFlow.signInAgain')}
      </Button>
    </Box>
  )
}
