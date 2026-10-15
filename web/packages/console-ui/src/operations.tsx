/**
 * The console operations seam: the read hooks and write mutations the
 * three administration pages consume, shipped over the generated
 * @speed/api-sdk administration operations.
 *
 * The pages read and write through the operations bundle they get from
 * useConsoleOperations -- a plain object whose members are the
 * react-query read hooks and write mutations of the three admin
 * surfaces. The bundle the package ships (consoleOperations) is a thin
 * adapter over the generated operations of /tenant/admin, /settings/admin
 * and /audit/admin: the reads run the generated query options through
 * useQuery with a `select` that flattens the page envelope to the view
 * rows, and the writes call the generated mutation functions with one flat
 * input, invalidating the list the write belongs to through the generated
 * key factories. The hooks run on the host's QueryClient (the host
 * supplies it as it supplies the rest of the React tree), the mutations go
 * through the same bound client, and a console page never builds a
 * QueryClient of its own and never hand-writes a query key.
 *
 * ConsoleOperationsProvider overrides the shipped bundle: a host that
 * wants different wiring, or a suite that scripts one, mounts its own and
 * useConsoleOperations answers with it. With no provider mounted the pages
 * read the shipped consoleOperations, so a host that composes the console
 * over a bound @speed/api-sdk client needs no binding of its own.
 *
 * The member set mirrors the three modules' route surfaces one for one:
 *
 *   /tenant/admin   GET /tenants, POST /tenants, PATCH /tenants/:id,
 *                   POST /tenants/:id/status
 *   /settings/admin GET /settings, GET /settings/:namespace,
 *                   PUT /settings/:namespace/:key,
 *                   DELETE /settings/:namespace/:key
 *   /audit/admin    GET /records
 *
 * The view types below are the generated response rows themselves
 * (TenantAdminTenant, SettingsAdminSetting, AuditAdminRecord): the
 * modules' OpenAPI fragments declare their 2xx bodies, so orval emits the
 * row shapes and the console names each for its own surface rather than
 * hand-writing a parallel model. Only the write *inputs* stay hand-written
 * -- they carry route parameters beside the body, which no single
 * generated type covers.
 *
 * The settings reads and writes target the global layer: the console is
 * the platform-staff surface, and the global layer is the platform's --
 * the tenant layer belongs to that tenant's own surface.
 */

import { createContext, useContext } from 'react'
import type { ReactNode } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { UseMutationResult, UseQueryResult } from '@tanstack/react-query'
import {
  getAuditAdminRecordsListQueryOptions,
  getSettingsAdminSettingsListNamespaceQueryKey,
  getSettingsAdminSettingsListNamespaceQueryOptions,
  getSettingsAdminSettingsListQueryKey,
  getSettingsAdminSettingsListQueryOptions,
  getTenantAdminTenantsListQueryKey,
  getTenantAdminTenantsListQueryOptions,
  settingsAdminSettingsRemove,
  settingsAdminSettingsSet,
  tenantAdminTenantsCreate,
  tenantAdminTenantsSetAlias,
  tenantAdminTenantsSetStatus,
} from '@speed/api-sdk'
import type {
  AuditAdminRecord,
  SettingsAdminSetting,
  TenantAdminTenant,
  TenantAdminTenantStatus,
} from '@speed/api-sdk'

/** The settings layer the console operates on: the global (platform)
 * layer, whose rows only platform staff may read and write. */
const GLOBAL_LAYER = { layer: 'global' } as const

/** One tenant as /tenant/admin shows it -- the generated row of
 * GET /tenant/admin/tenants, named for the console's own view. */
export type ConsoleTenant = TenantAdminTenant

/** The roster's closed status set: the only values a status write may
 * carry (the read side stays open -- an answer outside the set renders
 * the generic label rather than a raw value). The generated
 * TenantAdminTenantStatus enum. */
export type ConsoleTenantStatus = TenantAdminTenantStatus

/** One setting as /settings/admin shows it -- the generated row of
 * GET /settings/admin/settings. */
export type ConsoleSetting = SettingsAdminSetting

/** One audit record as /audit/admin shows it -- the generated row of
 * GET /audit/admin/records. */
export type ConsoleAuditRecord = AuditAdminRecord

/** POST /tenant/admin/tenants body: a tenant not registered yet. */
export interface ConsoleCreateTenantInput {
  readonly id: string
  readonly alias: string
}

/** PATCH /tenant/admin/tenants/:id body: a new alias. */
export interface ConsoleSetTenantAliasInput {
  readonly id: string
  readonly alias: string
}

/** POST /tenant/admin/tenants/:id/status body: a new state. */
export interface ConsoleSetTenantStatusInput {
  readonly id: string
  readonly status: ConsoleTenantStatus
}

/**
 * PUT /settings/admin/settings/:namespace/:key body: the new value's text.
 * `namespace` is the module that declared the item (a module name may
 * contain a dot), and `key` is the rest of the key under it -- the full
 * key is `<namespace>.<key>` -- because that is how the route addresses
 * the item. ConsoleSetting carries the full key and the namespace; a
 * caller writing a row splits the full key with `settingKeyRest`.
 */
export interface ConsoleSetSettingInput {
  readonly namespace: string
  readonly key: string
  readonly value: string
}

/** DELETE /settings/admin/settings/:namespace/:key target (see ConsoleSetSettingInput). */
export interface ConsoleRemoveSettingInput {
  readonly namespace: string
  readonly key: string
}

/**
 * The `:key` route segment of a setting whose full key is `<namespace>.<key>`:
 * the rest of the key under the namespace. The namespace may itself contain
 * a dot, so the split is by the known namespace prefix, not the first dot.
 */
export function settingKeyRest(
  fullKey: string,
  namespace: string,
): string {
  const prefix = `${namespace}.`
  return fullKey.startsWith(prefix)
    ? fullKey.slice(prefix.length)
    : fullKey
}

/**
 * The console's data operations, in react-query hook shape. Every member
 * is a hook the pages call during render (on the host's QueryClient);
 * consoleOperations below binds them to the generated @speed/api-sdk
 * operations of the same shape, and a host or suite may bind its own.
 */
export interface ConsoleOperations {
  /** GET /tenant/admin/tenants -- the whole roster, in the roster's order. */
  readonly useTenants: () => UseQueryResult<readonly ConsoleTenant[], unknown>
  /** POST /tenant/admin/tenants -- register a tenant not registered yet. */
  readonly useCreateTenant: () => UseMutationResult<
    ConsoleTenant,
    unknown,
    ConsoleCreateTenantInput
  >
  /** PATCH /tenant/admin/tenants/:id -- rename a tenant's alias. */
  readonly useSetTenantAlias: () => UseMutationResult<
    void,
    unknown,
    ConsoleSetTenantAliasInput
  >
  /** POST /tenant/admin/tenants/:id/status -- enable or disable a tenant. */
  readonly useSetTenantStatus: () => UseMutationResult<
    void,
    unknown,
    ConsoleSetTenantStatusInput
  >
  /** GET /settings/admin/settings[/:namespace] -- the declared items' views. */
  readonly useSettings: (
    namespace: string | undefined,
  ) => UseQueryResult<readonly ConsoleSetting[], unknown>
  /** PUT /settings/admin/settings/:namespace/:key -- write one item's value. */
  readonly useSetSetting: () => UseMutationResult<
    void,
    unknown,
    ConsoleSetSettingInput
  >
  /** DELETE /settings/admin/settings/:namespace/:key -- remove one item's override. */
  readonly useRemoveSetting: () => UseMutationResult<
    void,
    unknown,
    ConsoleRemoveSettingInput
  >
  /** GET /audit/admin/records -- one page of audit records, newest first. */
  readonly useAuditRecords: () => UseQueryResult<
    readonly ConsoleAuditRecord[],
    unknown
  >
}

/**
 * The shipped operations bundle: every member is the generated
 * @speed/api-sdk administration operation of the same shape, adapted to
 * the view shapes above. `useConsoleOperations` answers with it whenever
 * no ConsoleOperationsProvider overrides it, so the console pages run on
 * the generated surface with no host-side wiring.
 */
export const consoleOperations: ConsoleOperations = {
  useTenants: () =>
    useQuery({
      ...getTenantAdminTenantsListQueryOptions(),
      select: (page) => page.tenants,
    }),

  useCreateTenant: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleCreateTenantInput) =>
        tenantAdminTenantsCreate({ id: input.id, alias: input.alias }).then(
          (answer) => answer.tenant,
        ),
      onSuccess: () => {
        void queryClient.invalidateQueries({
          queryKey: getTenantAdminTenantsListQueryKey(),
        })
      },
    })
  },

  useSetTenantAlias: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleSetTenantAliasInput) =>
        tenantAdminTenantsSetAlias(input.id, { alias: input.alias }),
      onSuccess: () => {
        void queryClient.invalidateQueries({
          queryKey: getTenantAdminTenantsListQueryKey(),
        })
      },
    })
  },

  useSetTenantStatus: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleSetTenantStatusInput) =>
        tenantAdminTenantsSetStatus(input.id, { status: input.status }),
      onSuccess: () => {
        void queryClient.invalidateQueries({
          queryKey: getTenantAdminTenantsListQueryKey(),
        })
      },
    })
  },

  // The filter picks between the all-namespaces read and the one-namespace
  // read; both queries are declared every render and exactly one is enabled,
  // so the pages never call a hook conditionally (the rules of hooks) and an
  // unfiltered read never fires the per-namespace request.
  useSettings: (namespace) => {
    const all = useQuery({
      ...getSettingsAdminSettingsListQueryOptions({ ...GLOBAL_LAYER }),
      enabled: namespace === undefined,
    })
    const one = useQuery({
      ...getSettingsAdminSettingsListNamespaceQueryOptions(namespace ?? '', {
        ...GLOBAL_LAYER,
      }),
      enabled: namespace !== undefined,
    })
    return namespace === undefined ? all : one
  },

  useSetSetting: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleSetSettingInput) =>
        settingsAdminSettingsSet(
          input.namespace,
          input.key,
          { value: input.value },
          { ...GLOBAL_LAYER },
        ),
      onSuccess: (_answer, input) => {
        invalidateSetting(queryClient, input.namespace)
      },
    })
  },

  useRemoveSetting: () => {
    const queryClient = useQueryClient()
    return useMutation({
      mutationFn: (input: ConsoleRemoveSettingInput) =>
        settingsAdminSettingsRemove(input.namespace, input.key, {
          ...GLOBAL_LAYER,
        }),
      onSuccess: (_answer, input) => {
        invalidateSetting(queryClient, input.namespace)
      },
    })
  },

  useAuditRecords: () =>
    useQuery({
      ...getAuditAdminRecordsListQueryOptions(),
      select: (page) => page.entries,
    }),
}

/** Invalidates both settings reads a write can leave stale: the
 * all-namespaces list and the written item's own namespace list. The keys
 * come from the generated factories, so they cannot drift from the reads. */
function invalidateSetting(
  queryClient: ReturnType<typeof useQueryClient>,
  namespace: string,
): void {
  void queryClient.invalidateQueries({
    queryKey: getSettingsAdminSettingsListQueryKey({ ...GLOBAL_LAYER }),
  })
  void queryClient.invalidateQueries({
    queryKey: getSettingsAdminSettingsListNamespaceQueryKey(namespace, {
      ...GLOBAL_LAYER,
    }),
  })
}

const ConsoleOperationsContext = createContext<ConsoleOperations | null>(null)

export interface ConsoleOperationsProviderProps {
  /** The operations bundle every console page under this provider reads through. */
  readonly operations: ConsoleOperations
  readonly children: ReactNode
}

/**
 * Overrides the shipped console operations for the pages below it. A host
 * mounts one only to change the wiring; with none mounted the pages read
 * the shipped consoleOperations. The value is expected to be stable across
 * renders (a module-level object or a memo), because the pages call its
 * hook members during render.
 */
export function ConsoleOperationsProvider({
  operations,
  children,
}: ConsoleOperationsProviderProps) {
  return (
    <ConsoleOperationsContext.Provider value={operations}>
      {children}
    </ConsoleOperationsContext.Provider>
  )
}

/**
 * The operations bundle the pages read through: the enclosing provider's
 * when one is mounted, the shipped consoleOperations otherwise.
 */
export function useConsoleOperations(): ConsoleOperations {
  const operations = useContext(ConsoleOperationsContext)
  return operations ?? consoleOperations
}
