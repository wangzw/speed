/**
 * The fallback page for a URL no declaration matches, or for a gated
 * declaration whose feature is absent (route-kit renders the same fallback for
 * both, with the same context).
 *
 * It is a module of its own because two compositions render it: the route
 * table's fallback inside the tenant area, and the absent slot of the two
 * callback landings (the sign-in callback wraps it in the tenant frame).
 */

import type { ReactElement } from 'react'
import Box from '@mui/material/Box'
import Link from '@mui/material/Link'
import Typography from '@mui/material/Typography'
import { useTranslation } from '@speed/i18n'
import { useNavigate } from '@speed/route-kit'
import { DEMO_NAMESPACE } from '../resources.js'

/** The tenant area's home path: the fallback's offered way out. */
export const TENANT_HOME_PATH = '/'

/**
 * The dead-link page. The address stays on the page in a secondary line: it is
 * what a user copies back to an operator when reporting the dead link.
 */
export function NotFoundPage({
  path,
}: {
  readonly path: string
}): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const navigate = useNavigate()
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
        href={TENANT_HOME_PATH}
        underline="hover"
        onClick={(event) => {
          event.preventDefault()
          navigate(TENANT_HOME_PATH)
        }}
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
