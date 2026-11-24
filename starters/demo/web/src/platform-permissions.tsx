import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import type { ReactElement, ReactNode } from 'react'
import { useAuthState, useFeatureState } from '@speed/auth-core'
import type { AuthPermissionSets, AuthSession } from '@speed/auth-core'
import { authzSelfPermissions } from './app-api/index.js'
import { getCurrentTenant, useCurrentTenant } from './chrome.js'

const PLATFORM_DOMAIN = 'system'
const TENANT_DOMAIN = 'tenant'

export interface PermissionReadState {
  readonly tenantName: string | null
  readonly perms: AuthPermissionSets | null
  readonly pending: boolean
  readonly failed: boolean
  readonly retry: () => void
}

interface PermissionOutcome {
  readonly tenantName: string | null
  readonly perms: AuthPermissionSets | null
  readonly pending: boolean
  readonly failed: boolean
}

const NO_READ_OUTCOME: PermissionOutcome = {
  tenantName: null,
  perms: null,
  pending: false,
  failed: false,
}

const NO_PERMISSION_READ: PermissionReadState = {
  ...NO_READ_OUTCOME,
  retry: () => undefined,
}

const PermissionReadContext = createContext<PermissionReadState>(
  NO_PERMISSION_READ,
)

export function usePermissionRead(): PermissionReadState {
  return useContext(PermissionReadContext)
}

export function useTenantPermissionSet(): readonly string[] | null {
  const read = usePermissionRead()
  const tenantName = useCurrentTenant()
  if (read.tenantName !== tenantName || read.perms === null) {
    return null
  }
  return read.perms.tenant
}

export function PlatformPermissions({
  session,
  children,
}: {
  readonly session: AuthSession
  readonly children: ReactNode
}): ReactElement {
  const [attempt, setAttempt] = useState(0)
  const [outcome, setOutcome] = useState<PermissionOutcome>(NO_READ_OUTCOME)
  usePlatformPermissions(session, attempt, setOutcome)
  const retry = useCallback(() => setAttempt((current) => current + 1), [])
  const value = useMemo<PermissionReadState>(
    () => ({
      tenantName: outcome.tenantName,
      perms: outcome.perms,
      pending: outcome.pending,
      failed: outcome.failed,
      retry,
    }),
    [outcome, retry],
  )
  return (
    <PermissionReadContext.Provider value={value}>
      {children}
    </PermissionReadContext.Provider>
  )
}

function usePlatformPermissions(
  session: AuthSession,
  attempt: number,
  publish: (outcome: PermissionOutcome) => void,
): void {
  const snapshot = useAuthState()
  const authenticated = snapshot.state === 'authenticated'
  const principalId =
    snapshot.state === 'authenticated'
      ? (snapshot.principal?.account_id ?? '')
      : ''
  const impersonating = snapshot.impersonating
  const tenantName = useCurrentTenant()
  const features = useFeatureState()
  const ready = features.state === 'ready'
  const sequence = useRef(0)
  useEffect(() => {
    const issuedSequence = sequence.current + 1
    sequence.current = issuedSequence
    if (!authenticated || !ready || tenantName === null) {
      publish({ tenantName, perms: null, pending: false, failed: false })
      return undefined
    }
    const issuedName = tenantName
    const applies = (): boolean =>
      sequence.current === issuedSequence &&
      getCurrentTenant() === issuedName
    session.setPermissionSet(PLATFORM_DOMAIN, null)
    session.setPermissionSet(TENANT_DOMAIN, null)
    publish({
      tenantName: issuedName,
      perms: null,
      pending: true,
      failed: false,
    })
    void Promise.resolve()
      .then(() => authzSelfPermissions())
      .then((answer) => {
        if (!applies()) {
          return
        }
        session.setPermissionSet(PLATFORM_DOMAIN, answer.platform)
        session.setPermissionSet(TENANT_DOMAIN, answer.tenant)
        publish({
          tenantName: issuedName,
          perms: { system: answer.platform, tenant: answer.tenant },
          pending: false,
          failed: false,
        })
      })
      .catch(() => {
        if (!applies()) {
          return
        }
        publish({
          tenantName: issuedName,
          perms: null,
          pending: false,
          failed: true,
        })
      })
    return () => {
      sequence.current += 1
    }
  }, [
    authenticated,
    ready,
    session,
    principalId,
    impersonating,
    tenantName,
    attempt,
    publish,
  ])
}
