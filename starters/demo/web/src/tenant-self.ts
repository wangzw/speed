import { useSyncExternalStore } from 'react'
import { tenantSelfTenantsList } from './app-api/index.js'

export interface SelfTenant {
  readonly name: string
  readonly displayName: string
  readonly kind: SelfTenantKind
}

export type SelfTenantKind = 'business' | 'personal'

export interface SelfTenantState {
  readonly tenants: readonly SelfTenant[]
  readonly settled: boolean
  readonly failed: boolean
}

const EMPTY: SelfTenantState = {
  tenants: [],
  settled: false,
  failed: false,
}

let state: SelfTenantState = EMPTY
let epoch = 0
let sequence = 0
const listeners = new Set<() => void>()

function publish(next: SelfTenantState): void {
  state = next
  for (const listener of listeners) {
    listener()
  }
}

export function getSelfTenantState(): SelfTenantState {
  return state
}

export function subscribeSelfTenants(listener: () => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export function useSelfTenantState(): SelfTenantState {
  return useSyncExternalStore(subscribeSelfTenants, getSelfTenantState)
}

export function clearSelfTenants(): void {
  epoch += 1
  if (state === EMPTY) {
    return
  }
  publish(EMPTY)
}

// Only the newest read publishes: a slower earlier read must not overwrite a later answer.
export async function loadSelfTenants(): Promise<SelfTenantState> {
  const started = epoch
  const startedSequence = ++sequence
  const applies = (): boolean =>
    started === epoch && startedSequence === sequence
  try {
    const answer = await tenantSelfTenantsList()
    if (applies()) {
      publish({
        tenants: answer.tenants.map(({ name, display_name, kind }) => ({
          name,
          displayName: typeof display_name === 'string' ? display_name : '',
          kind,
        })),
        settled: true,
        failed: false,
      })
    }
  } catch {
    if (applies()) {
      publish({ tenants: state.tenants, settled: false, failed: true })
    }
  }
  return state
}

export function selfTenantName(name: string): string | null {
  return state.tenants.some((tenant) => tenant.name === name) ? name : null
}

export function selfTenantDisplayName(name: string): string | null {
  const tenant = state.tenants.find((row) => row.name === name)
  if (tenant === undefined) {
    return null
  }
  return tenant.displayName.trim() === '' ? tenant.name : tenant.displayName
}
