/**
 * The shell chrome the app's two areas share: the brand and the sign-out
 * action both the tenant frame and the console frame render in their top bar.
 *
 * The tenant area (product-shell's frame) and the console area (console-ui's
 * frame) are two different shells -- the console carries no tenant switcher --
 * but their top bars are the same chrome, so both render the brand and the
 * sign-out action from here. One definition keeps the two bars from drifting
 * into two products.
 */

import Typography from '@mui/material/Typography'
import { SignOutButton } from '@speed/auth-ui'
import { useTranslation } from '@speed/i18n'
import type { ReactElement } from 'react'
import { useAppServices } from './app-services.js'
import { DEMO_NAMESPACE } from './resources.js'

/** The top bar's brand: the demo namespace's title, in the header's own type. */
export function AppBrand(): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  return (
    <Typography noWrap variant="h6" sx={{ color: 'inherit', fontWeight: 600 }}>
      {t('app.title')}
    </Typography>
  )
}

/** The top bar's sign-out action: the assembled session's SignOutButton. */
export function UserMenu(): ReactElement {
  const { session } = useAppServices()
  return <SignOutButton session={session} />
}
