/**
 * The business slot's page: the one page the skeleton ships, holding the
 * place a product's first business surface takes. It renders no domain data --
 * the skeleton carries none -- only the demo namespace's copy saying where a
 * product's pages belong.
 */

import type { ReactElement } from 'react'
import Box from '@mui/material/Box'
import Typography from '@mui/material/Typography'
import { useTranslation } from '@speed/i18n'
import { DEMO_NAMESPACE } from '../resources.js'

export function HomePage(): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  return (
    <Box
      component="section"
      sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}
    >
      <Typography component="h1" variant="h4">
        {t('home.heading')}
      </Typography>
      <Typography variant="body1">{t('home.body')}</Typography>
    </Box>
  )
}
