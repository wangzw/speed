/** The console's in-page navigation seam. */

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
 * A console page link: a real link to `href` whose plain click goes to the
 * host's router when one is published; a modifier, non-primary or claimed
 * click keeps the browser's behavior.
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
