/**
 * ConsoleShell: the platform-staff console frame -- the second shell.
 *
 * It is the platform-side sibling of @speed/product-shell, and the
 * difference is the whole point of the package: the administration
 * surfaces it renders are mounted on endpoints that do not resolve a
 * tenant, so the console has no tenant switcher and no tenant-scoped
 * query key. It carries the platform navigation derived from the page
 * declarations it is given, and renders the host's routed content in a
 * `main` landmark. It does no routing: it never matches a URL, never
 * builds a link from a path and never decides selection -- the
 * declarations are collected and rendered by the application's route
 * layer, and the console's navigation entries are plain links to each
 * labelled declaration's path.
 *
 * The frame is three landmarks in one shell: a `header` banner carrying
 * the console title and any host header actions, a `nav` landmark listing
 * the labelled declarations' pages, and a `main` landmark holding the
 * host content. The banner is `position: fixed`, matching the layout-kit
 * frame's own top bar, with a spacer below it that keeps the content
 * clear of the fixed bar; the skip link lives inside the banner (the
 * first focusable element) and moves focus to `main` in place, the same
 * technique the layout-kit frame uses: it focuses the landmark
 * programmatically rather than navigating to a fragment, because a
 * fragment would rewrite `location.hash` and, in a hash-routed host,
 * that fragment is the route itself.
 *
 * The shell reads no data and holds no operations: the pages it hosts
 * reach their reads and writes through the operations bundle the host
 * mounted (see operations.tsx), and the permissions the declarations
 * carry are data the host reads to compose its own route guard.
 */

import { useId, useRef } from 'react'
import type { ReactNode } from 'react'
import type { SxProps, Theme } from '@mui/material/styles'
import AppBar from '@mui/material/AppBar'
import Box from '@mui/material/Box'
import Link from '@mui/material/Link'
import List from '@mui/material/List'
import ListItem from '@mui/material/ListItem'
import Toolbar from '@mui/material/Toolbar'
import { visuallyHiddenSx } from '@speed/ui-kit'
import type { ConsoleRoute } from './route.js'
import { useConsoleUiTranslation } from './internal/translation.js'

export interface ConsoleShellProps {
  /**
   * The console's page declarations. The shell derives the platform
   * navigation from the declarations that carry a `label` and renders
   * nothing else of them: path matching and view selection are the
   * route layer's, not the shell's.
   */
  readonly routes: readonly ConsoleRoute[]
  /** The host's routed content, rendered inside the `main` landmark. */
  readonly children: ReactNode
  /** The console title, rendered in the banner (host content). */
  readonly title?: ReactNode
  /** Banner content after the title, typically the account menu (host content). */
  readonly headerActions?: ReactNode
  /** Extra styling merged onto the root layout box. */
  readonly sx?: SxProps<Theme>
}

/** One navigation entry derived from a labelled declaration. */
interface NavEntry {
  readonly key: string
  readonly href: string
  readonly label: ReactNode
}

export function ConsoleShell({
  routes,
  children,
  title,
  headerActions,
  sx,
}: ConsoleShellProps) {
  const { t } = useConsoleUiTranslation()
  const mainId = useId()
  const mainRef = useRef<HTMLElement | null>(null)

  // The platform navigation is the labelled declarations, in declaration
  // order: a declaration without a label is a page that is reachable but
  // not listed (a detail route, say), exactly as route-kit's registration
  // seam defines. No entry is selected here -- selection needs path
  // matching, which is the route layer's.
  const navEntries: readonly NavEntry[] = routes
    .filter((route) => route.label !== undefined)
    .map((route) => ({
      key: route.path,
      href: route.path,
      label: route.label,
    }))

  return (
    <Box
      sx={{
        display: 'flex',
        flexDirection: 'column',
        minHeight: '100vh',
        ...sx,
      }}
    >
      <AppBar position="fixed" component="header">
        <Link
          href={`#${mainId}`}
          onClick={(event) => {
            event.preventDefault()
            mainRef.current?.focus()
          }}
          sx={{
            ...visuallyHiddenSx,
            '&:focus': {
              position: 'fixed',
              top: 8,
              left: 8,
              bottom: 'auto',
              zIndex: 1,
              width: 'auto',
              height: 'auto',
              clip: 'auto',
              clipPath: 'none',
              p: 1,
              bgcolor: 'background.paper',
            },
          }}
        >
          {t('shell.skipToContent')}
        </Link>
        <Toolbar sx={{ gap: 2 }}>
          {title}
          <Box sx={{ flexGrow: 1 }} />
          {headerActions}
        </Toolbar>
      </AppBar>
      <Toolbar />
      <Box
        sx={{
          display: 'flex',
          flexGrow: 1,
          flexDirection: { xs: 'column', sm: 'row' },
        }}
      >
        <Box
          component="nav"
          aria-label={t('shell.navLabel')}
          sx={{
            width: { xs: '100%', sm: 240 },
            flexShrink: 0,
            borderColor: 'divider',
            borderRight: { sm: '1px solid' },
            borderBottom: { xs: '1px solid', sm: 'none' },
          }}
        >
          <List>
            {navEntries.map((entry) => (
              <ListItem key={entry.key} disablePadding>
                <Link
                  href={entry.href}
                  underline="none"
                  sx={{
                    display: 'block',
                    width: '100%',
                    px: 2,
                    py: 1.5,
                    color: 'text.primary',
                  }}
                >
                  {entry.label}
                </Link>
              </ListItem>
            ))}
          </List>
        </Box>
        <Box
          component="main"
          id={mainId}
          ref={mainRef}
          tabIndex={-1}
          sx={{ flexGrow: 1, minWidth: 0, p: 3 }}
        >
          {children}
        </Box>
      </Box>
    </Box>
  )
}
