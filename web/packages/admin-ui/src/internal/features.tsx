/** The console's feature-presence seam. */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'
import type { FeatureName } from '@speed/api-sdk/features'

type HasFeature = (name: FeatureName) => boolean

const NO_FEATURES: HasFeature = () => false

const AdminFeatureContext = createContext<HasFeature | undefined>(undefined)

export interface AdminFeatureProviderProps {
  /** The host's synchronous feature answer. */
  readonly hasFeature: HasFeature
  readonly children: ReactNode
}

/** Publishes the shell's feature answer to the pages it renders. */
export function AdminFeatureProvider({
  hasFeature,
  children,
}: AdminFeatureProviderProps) {
  return (
    <AdminFeatureContext.Provider value={hasFeature}>
      {children}
    </AdminFeatureContext.Provider>
  )
}

/** Whether the process serves the named feature. False without a provider. */
export function useAdminHasFeature(): HasFeature {
  return useContext(AdminFeatureContext) ?? NO_FEATURES
}
