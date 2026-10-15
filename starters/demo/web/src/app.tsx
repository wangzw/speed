/**
 * The app's view root: the route provider around the shell the current URL
 * lands in.
 *
 * The routing composition is route-kit's: a RouteProvider installs the router
 * and holds the page table, RouteView renders the matched declaration, and
 * useNavItems derives the navigation. The app adds the shell choice -- two
 * frames over one table:
 *
 *  - The tenant area renders ProductShell, the three-branch view machine, with
 *    the nav derived from every non-console entry and the auth-ui sign-in
 *    surface and sign-out action in the host slots. The shell renders the nav
 *    and content only while the session is authenticated; a fresh visitor meets
 *    the sign-in surface, and one whose session ends meets the ended screen.
 *
 *  - The console area renders @speed/console-ui's frame instead, with no tenant
 *    frame around it. The area is chosen by the selected navigation entry: the
 *    console's pages are labelled under CONSOLE_PREFIX, so a URL in that path
 *    space selects one and the app's two areas never overlap.
 *
 * A URL no declaration matches renders the fallback page inside the tenant area.
 */

import Box from '@mui/material/Box'
import Typography from '@mui/material/Typography'
import { SignInScreen, SignOutButton } from '@speed/auth-ui'
import { useTranslation } from '@speed/i18n'
import type { AppShellNavItem } from '@speed/layout-kit'
import { ProductShell } from '@speed/product-shell'
import {
  RouteProvider,
  RouteView,
  useNavigate,
  useNavItems,
} from '@speed/route-kit'
import type { RouteContext } from '@speed/route-kit'
import type { ReactElement } from 'react'
import { useAppServices } from './app-services.js'
import { ConsoleArea } from './console/console-area.js'
import { DEMO_NAMESPACE } from './resources.js'
import { consoleShellRoutes, isConsolePath, routes } from './routes.js'

/** The app view root: routing installed once, over the shell switch. */
export function DemoApp(): ReactElement {
  return (
    <RouteProvider
      table={routes}
      fallback={(context: RouteContext) => <NotFoundPage path={context.path} />}
    >
      <AppFrame />
    </RouteProvider>
  )
}

/** Renders the area the current URL lands in: the console or the tenant shell. */
function AppFrame(): ReactElement {
  const items = useNavItems()
  const navigate = useNavigate()

  const consoleSelected = items.some(
    (item) => item.selected && isConsolePath(item.id),
  )
  if (consoleSelected) {
    return (
      <ConsoleArea routes={consoleShellRoutes}>
        <RouteView />
      </ConsoleArea>
    )
  }

  // The tenant navigation is every labelled entry outside the console area.
  // Each entry keeps its href (for a middle click or a copy) and navigates
  // through the router on a plain click, so the page never reloads.
  const navItems: readonly AppShellNavItem[] = items
    .filter((item) => !isConsolePath(item.id))
    .map((item) => ({
      id: item.id,
      label: item.label,
      href: item.href,
      selected: item.selected,
      onClick: (event) => {
        event.preventDefault()
        navigate(item.href)
      },
    }))

  return (
    <ProductShell
      navItems={navItems}
      header={<AppBrand />}
      userMenu={<UserMenu />}
      signIn={<SignInView />}
    >
      <RouteView />
    </ProductShell>
  )
}

/** The AppBar brand: the demo namespace's title. */
function AppBrand(): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  return (
    <Typography noWrap variant="h6" sx={{ color: 'inherit', fontWeight: 600 }}>
      {t('app.title')}
    </Typography>
  )
}

/** The sign-in slot: auth-ui's sign-in family over the assembled session. */
function SignInView(): ReactElement {
  const { session } = useAppServices()
  return <SignInScreen session={session} />
}

/** The user menu: the assembled session's sign-out action. */
function UserMenu(): ReactElement {
  const { session } = useAppServices()
  return <SignOutButton session={session} />
}

/** The fallback page for a URL no declaration matches. */
function NotFoundPage({ path }: { readonly path: string }): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  return (
    <Box
      component="section"
      sx={{ display: 'flex', flexDirection: 'column', gap: 2 }}
    >
      <Typography component="h1" variant="h4">
        {t('notFound.heading')}
      </Typography>
      <Typography variant="body1">{t('notFound.body', { path })}</Typography>
    </Box>
  )
}
