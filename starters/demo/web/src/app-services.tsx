/**
 * The app-level services provider: carries the assembled session and client
 * from the app-entry assembly down to the views that drive them.
 *
 * bootstrapSpeedApp hands each declared provider the assembled services; this
 * provider is the app's, mounted between the standard provider stack and the
 * view root (src/main.tsx). A view that drives a session operation -- the
 * sign-in surface, the sign-out action -- reads the session through
 * useAppServices rather than receiving it through props, so the shell slots
 * stay prop-light and the assembly stays the one place the services are
 * created.
 */

import { createContext, useContext } from 'react'
import type { ReactElement, ReactNode } from 'react'
import type { SpeedAppServices } from '@speed/product-shell/bootstrap'

const AppServicesContext = createContext<SpeedAppServices | null>(null)

export interface AppServicesProviderProps {
  /** The services the app-entry assembly produced. */
  readonly services: SpeedAppServices
  readonly children: ReactNode
}

export function AppServicesProvider({
  services,
  children,
}: AppServicesProviderProps): ReactElement {
  return (
    <AppServicesContext.Provider value={services}>
      {children}
    </AppServicesContext.Provider>
  )
}

/** The assembled services; throws when no provider is mounted above. */
export function useAppServices(): SpeedAppServices {
  const services = useContext(AppServicesContext)
  if (services === null) {
    throw new Error(
      'useAppServices must be called within an AppServicesProvider',
    )
  }
  return services
}
