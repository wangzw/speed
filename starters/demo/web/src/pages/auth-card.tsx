/**
 * The auth surface's card chrome: the brand, the page heading and its
 * subtitle inside the centered card.
 *
 * The sign-in page and the callback route's account-opening step both
 * render inside it, so a first social login sees the same card as the
 * sign-in page -- brand and page heading included -- rather than a bare
 * page of its own. The card supplies only the chrome; what sits inside it
 * is the caller's content.
 */

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
  /** The page heading: what the surface is asking for. */
  readonly title: string
  /** The sentence under the heading. */
  readonly subtitle: string
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
        <Typography variant="body2" color="text.secondary" sx={{ mt: 1 }}>
          {subtitle}
        </Typography>
      </Box>
      {children}
    </Card>
  )
}
