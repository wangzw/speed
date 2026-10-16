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
 *    the nav derived from every non-console entry plus the host's own console
 *    entry, and the auth-ui sign-in surface and sign-out action in the host
 *    slots. The shell renders the nav and content only while the session is
 *    authenticated; a fresh visitor meets the sign-in surface, and one whose
 *    session ends meets the ended screen. The routed page sits in a padded
 *    content region so it does not butt against the chrome.
 *
 *  - The console area renders @speed/console-ui's frame instead, with no tenant
 *    frame around it. The area is chosen by the selected navigation entry: the
 *    console's pages are labelled under CONSOLE_PREFIX, so a URL in that path
 *    space selects one and the app's two areas never overlap. It carries the
 *    same session gate the tenant shell does (the console frame is not
 *    ProductShell) and a host-composed route guard over each page's permission
 *    code.
 *
 * A URL no declaration matches renders the fallback page inside the tenant area.
 */

import Box from '@mui/material/Box'
import Paper from '@mui/material/Paper'
import Typography from '@mui/material/Typography'
import { useAuthState } from '@speed/auth-core'
import type { AuthSnapshot } from '@speed/auth-core'
import { SignInScreen, SignOutButton } from '@speed/auth-ui'
import { useTranslation } from '@speed/i18n'
import { RouteGuard } from '@speed/layout-kit'
import type { AppShellNavItem, RouteGuardStatus } from '@speed/layout-kit'
import { ProductShell } from '@speed/product-shell'
import {
  RouteProvider,
  RouteView,
  useNavigate,
  useNavItems,
} from '@speed/route-kit'
import type { RouteContext } from '@speed/route-kit'
import type { ReactElement, ReactNode } from 'react'
import { useAppServices } from './app-services.js'
import { ConsoleArea } from './console/console-area.js'
import { DemoNavLabel } from './nav-label.js'
import { DEMO_NAMESPACE } from './resources.js'
import {
  CONSOLE_PREFIX,
  consoleShellRoutes,
  isConsolePath,
  routes,
} from './routes.js'

/** The platform-staff permission domain the console routes' codes resolve in:
 * auth-core names 'system' the tenant-independent platform-staff set. */
const CONSOLE_PERMISSION_DOMAIN = 'system'

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
      <SessionGate>
        <ConsoleArea routes={consoleShellRoutes}>
          <ConsoleRouteGuard>
            <RouteView />
          </ConsoleRouteGuard>
        </ConsoleArea>
      </SessionGate>
    )
  }

  // The tenant navigation is every labelled entry outside the console area,
  // plus the host's own console entry (the console's pages are filtered out
  // above, so nothing else points into the console). Each entry keeps its href
  // (for a middle click or a copy) and navigates through the router on a plain
  // click, so the page never reloads.
  const consoleEntry: AppShellNavItem = {
    id: 'console',
    label: <DemoNavLabel labelKey="nav.console" />,
    href: CONSOLE_PREFIX,
    selected: false,
    onClick: (event) => {
      event.preventDefault()
      navigate(CONSOLE_PREFIX)
    },
  }
  const navItems: readonly AppShellNavItem[] = [
    ...items
      .filter((item) => !isConsolePath(item.id))
      .map((item): AppShellNavItem => ({
        id: item.id,
        label: item.label,
        href: item.href,
        selected: item.selected,
        onClick: (event) => {
          event.preventDefault()
          navigate(item.href)
        },
      })),
    consoleEntry,
  ]

  return (
    <ProductShell
      navItems={navItems}
      header={<AppBrand />}
      userMenu={<UserMenu />}
      signIn={<SignInView />}
    >
      <Box sx={{ p: 3 }}>
        <RouteView />
      </Box>
    </ProductShell>
  )
}

/**
 * The console area's session gate. The tenant area is gated by ProductShell
 * itself, but the console renders its own frame, so it needs the same branch:
 * an anonymous viewer -- a fresh visitor, or one whose authenticated session
 * turned anonymous mid-use (the same snapshot flip) -- meets the host's
 * sign-in surface instead of a console whose requests can never answer.
 */
function SessionGate({
  children,
}: {
  readonly children: ReactNode
}): ReactElement {
  const snapshot = useAuthState()
  if (snapshot.state !== 'authenticated') {
    return <SignInView />
  }
  return <>{children}</>
}

/**
 * The console's route-level gate, composed the host way: layout-kit's
 * RouteGuard fed a status the host derives from the matched page's permission
 * code (the console declarations carry tenant.read / settings.read /
 * audit.read). A code the principal is known not to hold is denied; a code
 * held, a page with no code, or -- the skeleton's case, which attaches no
 * platform permission list -- an unknown set passes, since the server stays
 * the authority on the console's administration surfaces (the console-ui
 * design: a caller lacking a permission is refused server-side rather than
 * hidden client-side).
 */
function ConsoleRouteGuard({
  children,
}: {
  readonly children: ReactNode
}): ReactElement {
  const snapshot = useAuthState()
  const items = useNavItems()
  const selected = items.find((item) => item.selected && isConsolePath(item.id))
  const permission = consoleShellRoutes.find(
    (route) => route.path === selected?.id,
  )?.permission
  return (
    <RouteGuard status={consoleGuardStatus(snapshot, permission)}>
      {children}
    </RouteGuard>
  )
}

/** The RouteGuard status for one console page under the current session. */
function consoleGuardStatus(
  snapshot: AuthSnapshot,
  permission: string | undefined,
): RouteGuardStatus {
  if (permission === undefined) {
    return 'allowed'
  }
  if (snapshot.state !== 'authenticated') {
    // No session yet: the session gate above turned this viewer away, so the
    // guard only ever settles once authenticated; pending is fail-closed.
    return 'pending'
  }
  const set = snapshot.permissionSets[CONSOLE_PERMISSION_DOMAIN]
  return set === null || set.includes(permission) ? 'allowed' : 'denied'
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

/**
 * The sign-in slot: the host's chrome around auth-ui's sign-in family. The
 * screen itself renders no heading, branding or layout -- those are host
 * content -- so the host frames it in a centered card with the brand and a
 * heading; the channel list declares only the password channel, because the
 * skeleton delivers no SMS code and offering a channel nothing can finish is a
 * promise no phone keeps.
 */
function SignInView(): ReactElement {
  const { session } = useAppServices()
  const { t } = useTranslation(DEMO_NAMESPACE)
  return (
    <Box
      component="main"
      sx={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        bgcolor: 'background.default',
        p: 2,
      }}
    >
      <Paper elevation={2} sx={{ width: '100%', maxWidth: 400, p: 4 }}>
        <Box sx={{ textAlign: 'center', mb: 3 }}>
          <Typography
            variant="subtitle1"
            color="text.secondary"
            sx={{ fontWeight: 600 }}
          >
            {t('app.title')}
          </Typography>
          <Typography component="h1" variant="h5" sx={{ mt: 0.5 }}>
            {t('signIn.heading')}
          </Typography>
          <Typography variant="body2" color="text.secondary" sx={{ mt: 1 }}>
            {t('signIn.subtitle')}
          </Typography>
        </Box>
        <SignInScreen session={session} channels={['password']} />
      </Paper>
    </Box>
  )
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
