/** The terminal notice a channel renders when its flow is gone. */

import Box from '@mui/material/Box'
import Alert from '@mui/material/Alert'
import Button from '@mui/material/Button'
import { useAuthUiTranslation } from './translation.js'

export interface FlowExpiredProps {
  readonly onRestart: () => void
}

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
