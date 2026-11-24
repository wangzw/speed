import type { ReactElement, ReactNode } from 'react'
import Box from '@mui/material/Box'
import Card from '@mui/material/Card'
import Typography from '@mui/material/Typography'
import { useTranslation } from '@speed/i18n'
import { DEMO_NAMESPACE } from '../resources.js'

export function AuthCard({
  title,
  subtitle,
  children,
}: {
  readonly title: string
  readonly subtitle?: string
  readonly children: ReactNode
}): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  return (
    <Card variant="outlined" sx={{ width: '100%', maxWidth: 400, p: 4 }}>
      <Box sx={{ textAlign: 'center', mb: 3 }}>
        <Typography
          variant="subtitle1"
          color="text.secondary"
          sx={{ fontWeight: 600 }}
        >
          {t('app.title')}
        </Typography>
        <Typography component="h1" variant="h5" sx={{ mt: 0.5 }}>
          {title}
        </Typography>
        {subtitle === undefined ? null : (
          <Typography variant="body2" color="text.secondary" sx={{ mt: 1 }}>
            {subtitle}
          </Typography>
        )}
      </Box>
      {children}
    </Card>
  )
}
