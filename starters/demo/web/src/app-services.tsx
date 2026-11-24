import { createContext, useContext } from 'react'
import type { ReactElement, ReactNode } from 'react'
import type { SpeedAppServices } from '@speed/product-shell/bootstrap'

const AppServicesContext = createContext<SpeedAppServices | null>(null)

export interface AppServicesProviderProps {
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

export function useAppServices(): SpeedAppServices {
  const services = useContext(AppServicesContext)
  if (services === null) {
    throw new Error(
      'useAppServices must be called within an AppServicesProvider',
    )
  }
  return services
}
