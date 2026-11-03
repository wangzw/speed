/**
 * The console's in-page navigation seam.
 *
 * AdminShell already hands each navigation entry's activation to the
 * host's router through its `onNavigate` prop; a page that links to
 * another console page (a roster row to a tenant's detail, an audit row to
 * a record's detail) must reach the same seam so a click routes in place
 * instead of reloading the page. The shell publishes its `onNavigate` to
 * its routed content through this context; AdminLink reads it.
 *
 * With no provider (a page rendered on its own, as a unit test renders
 * one) the context is absent and AdminLink is a plain link: it keeps its
 * href and navigates the browser, the behavior the shell itself has for a
 * host that never supplies `onNavigate`.
 */

import { createContext, useContext } from 'react'
import type { MouseEvent, ReactNode } from 'react'
import type { SxProps, Theme } from '@mui/material/styles'
import Link from '@mui/material/Link'
import { isPlainPrimaryClick } from '@speed/layout-kit'

const AdminNavigateContext = createContext<((href: string) => void) | undefined>(
  undefined,
)

export interface AdminNavigationProviderProps {
  /** The host's in-place navigation callback; absent leaves links plain. */
  readonly onNavigate?: (href: string) => void
  readonly children: ReactNode
}

/** Publishes the shell's navigation callback to the pages it renders. */
export function AdminNavigationProvider({
  onNavigate,
  children,
}: AdminNavigationProviderProps) {
  return (
    <AdminNavigateContext.Provider value={onNavigate}>
      {children}
    </AdminNavigateContext.Provider>
  )
}

/** The in-page navigation callback, or undefined when no shell supplies one. */
export function useAdminNavigate(): ((href: string) => void) | undefined {
  return useContext(AdminNavigateContext)
}

export interface AdminLinkProps {
  /** The destination the link keeps even when a router intercepts the click. */
  readonly href: string
  readonly children: ReactNode
  readonly sx?: SxProps<Theme>
}

/**
 * A console page link: a real link to `href`, whose plain click is handed
 * to the host's router when the shell published one (so the session is not
 * lost to a reload) and left alone otherwise. A click with a modifier, a
 * non-primary button or one another handler claimed keeps the browser's
 * own behavior.
 */
export function AdminLink({ href, children, sx }: AdminLinkProps) {
  const navigate = useAdminNavigate()
  const onClick =
    navigate === undefined
      ? undefined
      : (event: MouseEvent<HTMLAnchorElement>) => {
          if (!isPlainPrimaryClick(event)) {
            return
          }
          event.preventDefault()
          navigate(href)
        }
  return (
    <Link href={href} onClick={onClick} sx={sx}>
      {children}
    </Link>
  )
}
