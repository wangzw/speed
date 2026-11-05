/**
 * The host's own tenant list: the answer of the caller tenant read
 * (`GET /api/v1/auth/me/tenants`, the tenant.self module) as the tenant
 * switcher's rows and as the alias source for tenant-scoped requests.
 *
 * Two facts are held apart. `tenants` is the list itself, ordered by
 * identifier as the read answers it, each row carrying its host-answered
 * `kind`. `settled` says whether the LAST read produced a real answer: a
 * failure is not an empty list, so it leaves the rows already on hand in
 * place and clears the flag. The settlement and the failure rendering read
 * exactly that distinction: a failed read must never settle a current tenant
 * or be mistaken for "no memberships".
 *
 * The list belongs to an identity, not to the page: a sign-out clears it, and
 * the always-mounted root clears and re-reads it whenever the acting account
 * changes. Clear bumps an epoch so a read that lands after the identity moved
 * cannot publish the previous account's rows.
 *
 * It is module state rather than React state because the tenantProvider the
 * assembly reads on every attempt (src/main.tsx) and the host's chrome both
 * need it outside a component tree; components subscribe through
 * useSelfTenantState.
 */

import { useSyncExternalStore } from 'react'
import { tenantSelfTenantsList } from './app-api/index.js'

/** One tenant the account holds an active membership in. */
export interface SelfTenant {
  /** The tenant's stable identifier. */
  readonly id: string
  /** The name the request surface addresses the tenant by. */
  readonly alias: string
  /** The tenant's kind, as the host's resolver answered it through the read.
   * The set is closed: `business` or `personal`. The switcher labels and the
   * default settlement branch on this value, never on the identifier. */
  readonly kind: SelfTenantKind
}

/** The two kinds the caller tenant read answers. The vocabulary is the
 * platform's closed set; the generated client types this field as the same
 * union. */
export type SelfTenantKind = 'business' | 'personal'

/** The list, whether the last read settled as an answer, and whether it
 * failed. */
export interface SelfTenantState {
  readonly tenants: readonly SelfTenant[]
  /** True only after a read resolved with the server's answer. A failed read
   * (5xx, network, timeout, a 404 when the deployment does not mount
   * tenant.self) marks it false while keeping `tenants`. */
  readonly settled: boolean
  /** True when the last read ended in failure. A read that has not answered
   * yet and a list that was never read are both false; that is what tells an
   * in-flight read apart from a failed one, which `settled` alone cannot. */
  readonly failed: boolean
}

const EMPTY: SelfTenantState = {
  tenants: [],
  settled: false,
  failed: false,
}

let state: SelfTenantState = EMPTY
// Changes whenever the list stops belonging to the account it was read for.
// A read captures this at start and drops its answer if it moved meanwhile.
let epoch = 0
// The issue order of the reads. A read publishes only while it is still the
// newest one started, so a slow failure cannot overwrite a newer success --
// the retry on the organization failure page starts a read per click, and the
// last outcome to land must be the newest read's, not the slowest one's.
let sequence = 0
const listeners = new Set<() => void>()

function publish(next: SelfTenantState): void {
  state = next
  for (const listener of listeners) {
    listener()
  }
}

/** The list as it stands, for a caller outside React. */
export function getSelfTenantState(): SelfTenantState {
  return state
}

/** Subscribes to list changes; returns the unsubscribe. */
export function subscribeSelfTenants(listener: () => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

/** The list as React state. The snapshot object is stable between changes, as
 * useSyncExternalStore requires. */
export function useSelfTenantState(): SelfTenantState {
  return useSyncExternalStore(subscribeSelfTenants, getSelfTenantState)
}

/**
 * Forgets the list. Called on sign-out and on every identity change: a list
 * read for one account must not settle the next account's tenant or be
 * announced on its requests.
 */
export function clearSelfTenants(): void {
  epoch += 1
  if (state === EMPTY) {
    return
  }
  publish(EMPTY)
}

/**
 * Reads the caller's memberships. A success replaces the list and marks it
 * settled; a failure keeps the rows already on hand, marks it unsettled and
 * records the failure, so a failed read is never mistaken for "no
 * memberships" and is told apart from a read still in flight. Returns the
 * state the read left behind.
 */
export async function loadSelfTenants(): Promise<SelfTenantState> {
  const started = epoch
  const startedSequence = ++sequence
  const applies = (): boolean =>
    started === epoch && startedSequence === sequence
  try {
    const answer = await tenantSelfTenantsList()
    if (applies()) {
      publish({
        tenants: answer.tenants.map(({ id, alias, kind }) => ({
          id,
          alias,
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

/** One listed tenant's alias, or null when the list does not hold it. */
export function selfTenantAlias(tenantId: string): string | null {
  return state.tenants.find((tenant) => tenant.id === tenantId)?.alias ?? null
}
