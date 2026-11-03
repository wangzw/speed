/**
 * The workspace administration surface's breadcrumb context.
 *
 * The workspace pages reuse the platform console's page components, whose
 * `PageHeader` renders the console's own trail ("admin" down to the page).
 * Inside the workspace surface that trail is wrong: the page hangs off the
 * workspace home, not the platform console. A host therefore publishes the
 * workspace trail through this provider, and the shared page components
 * render it in preference to their own trail when it is present. Nothing
 * leaks either way -- a page outside the provider keeps its platform
 * trail, and a workspace page never shows one.
 *
 * `WorkspaceAdminProvider` mounts this provider itself when its
 * `breadcrumbs` prop is supplied. A host that renders the pages without the
 * full provider (its own frame, its own dialogs) mounts it directly, the
 * way a host composing console pages mounts `AdminSessionProvider`.
 */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'
import type { PageHeaderBreadcrumb } from '@speed/ui-kit'

/** The breadcrumbs the workspace surface publishes to the pages below. */
const WorkspaceAdminBreadcrumbContext = createContext<
  readonly PageHeaderBreadcrumb[] | undefined
>(undefined)

export interface WorkspaceAdminBreadcrumbProviderProps {
  /** The trail from the workspace home down to the page. */
  readonly breadcrumbs: readonly PageHeaderBreadcrumb[]
  readonly children: ReactNode
}

/** Publishes the workspace trail to the page components below. */
export function WorkspaceAdminBreadcrumbProvider({
  breadcrumbs,
  children,
}: WorkspaceAdminBreadcrumbProviderProps) {
  return (
    <WorkspaceAdminBreadcrumbContext.Provider value={breadcrumbs}>
      {children}
    </WorkspaceAdminBreadcrumbContext.Provider>
  )
}

/**
 * The workspace trail, or `undefined` when no provider is mounted (the
 * platform console's own trail then applies). The value is the array the
 * provider was given, so a host can memoize it without this hook copying
 * it on every render.
 */
export function useWorkspaceAdminBreadcrumbs():
  | readonly PageHeaderBreadcrumb[]
  | undefined {
  return useContext(WorkspaceAdminBreadcrumbContext)
}
