import type { ReactElement } from 'react'
import Box from '@mui/material/Box'
import Link from '@mui/material/Link'
import Typography from '@mui/material/Typography'
import { useTranslation } from '@speed/i18n'
import { useRouteLinkProps } from '@speed/route-kit'
import { DEMO_NAMESPACE } from '../resources.js'

export const TENANT_HOME_PATH = '/'

export function NotFoundPage({
  path,
}: {
  readonly path: string
}): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const homeLink = useRouteLinkProps(TENANT_HOME_PATH)
  return (
    <Box
      component="section"
      sx={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'flex-start',
        gap: 2,
      }}
    >
      <Typography component="h1" variant="h4">
        {t('notFound.heading')}
      </Typography>
      <Typography variant="body1">{t('notFound.body')}</Typography>
      <Link
        href={homeLink.href}
        onClick={homeLink.onClick}
        underline="hover"
      >
        {t('notFound.home')}
      </Link>
      <Typography
        variant="caption"
        color="text.secondary"
        sx={{ wordBreak: 'break-all' }}
      >
        {t('notFound.path', { path })}
      </Typography>
    </Box>
  )
}
