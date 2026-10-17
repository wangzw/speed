/**
 * The console's feature-presence seam.
 *
 * The console shell receives the host's synchronous feature answer
 * (`hasFeature`) and publishes it to the pages it renders, the same way it
 * publishes its in-page navigation callback. The package itself never reads
 * the session or the feature state; a page that renders a feature-gated
 * control asks this hook, and the host decides what "present" means.
 *
 * With no provider (a page rendered on its own, as a unit test renders
 * one) every feature is absent: the fail-closed direction, so a control
 * whose feature was never supplied is hidden rather than shown.
 */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'
import type { FeatureName } from '@speed/api-sdk/features'

type HasFeature = (name: FeatureName) => boolean

const NO_FEATURES: HasFeature = () => false

const ConsoleFeatureContext = createContext<HasFeature | undefined>(undefined)

export interface ConsoleFeatureProviderProps {
  /** The host's synchronous feature answer. */
  readonly hasFeature: HasFeature
  readonly children: ReactNode
}

/** Publishes the shell's feature answer to the pages it renders. */
export function ConsoleFeatureProvider({
  hasFeature,
  children,
}: ConsoleFeatureProviderProps) {
  return (
    <ConsoleFeatureContext.Provider value={hasFeature}>
      {children}
    </ConsoleFeatureContext.Provider>
  )
}

/** Whether the process serves the named feature. False without a provider. */
export function useConsoleHasFeature(): HasFeature {
  return useContext(ConsoleFeatureContext) ?? NO_FEATURES
}
