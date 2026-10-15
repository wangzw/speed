/**
 * A scripted console operations bundle for the console-ui suites.
 *
 * The console pages read and write through the ConsoleOperations bundle
 * the host mounts (see src/operations.tsx). In production the host binds
 * the generated @speed/api-sdk operations; here the suite binds real
 * react-query hooks over a scripted backend, so the pages exercise
 * genuine react-query states -- the first-load pending branch, a settled
 * empty answer, a failed read, and a write whose success invalidates the
 * list it belongs to -- without an HTTP transport.
 *
 * The query keys live here, in test code, because the production keys
 * live inside the generated hooks; the scripted backend is the seam's
 * only stand-in. A backend function a test scripts to reject drives the
 * corresponding failure branch.
 */

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type {
  ConsoleAuditRecord,
  ConsoleCreateTenantInput,
  ConsoleOperations,
  ConsoleRemoveSettingInput,
  ConsoleSetSettingInput,
  ConsoleSetTenantAliasInput,
  ConsoleSetTenantStatusInput,
  ConsoleSetting,
  ConsoleTenant,
} from '../src/operations.js'

/** The scripted backend the operations bundle calls. */
export interface ConsoleBackend {
  listTenants: () => Promise<readonly ConsoleTenant[]>
  createTenant: (input: ConsoleCreateTenantInput) => Promise<ConsoleTenant>
  setTenantAlias: (input: ConsoleSetTenantAliasInput) => Promise<void>
  setTenantStatus: (input: ConsoleSetTenantStatusInput) => Promise<void>
  listSettings: (
    namespace: string | undefined,
  ) => Promise<readonly ConsoleSetting[]>
  setSetting: (input: ConsoleSetSettingInput) => Promise<void>
  removeSetting: (input: ConsoleRemoveSettingInput) => Promise<void>
  listRecords: () => Promise<readonly ConsoleAuditRecord[]>
}

/**
 * A working in-memory backend for the common case, with the named
 * behaviour overridden per test. Everything defaults to a settled
 * successful answer (empty reads and resolved writes); a test that wants
 * a failure passes a rejecting function for the member it exercises.
 */
export function makeConsoleBackend(
  overrides: Partial<ConsoleBackend> = {},
): ConsoleBackend {
  return {
    listTenants: () => Promise.resolve([]),
    createTenant: (input) =>
      Promise.resolve({ id: input.id, alias: input.alias, status: 'active' }),
    setTenantAlias: () => Promise.resolve(),
    setTenantStatus: () => Promise.resolve(),
    listSettings: () => Promise.resolve([]),
    setSetting: () => Promise.resolve(),
    removeSetting: () => Promise.resolve(),
    listRecords: () => Promise.resolve([]),
    ...overrides,
  }
}

const TENANTS_KEY = ['console-test', 'tenants'] as const
const AUDIT_KEY = ['console-test', 'audit'] as const
function settingsKey(namespace: string | undefined) {
  return ['console-test', 'settings', namespace ?? null] as const
}

/**
 * Builds a ConsoleOperations whose hooks run real react-query queries and
 * mutations over the given backend. Call it in the test body and pass the
 * result to renderWithProviders; keep the returned object stable across a
 * render (a module-level const or a value captured before render).
 */
export function createConsoleOperations(
  backend: ConsoleBackend,
): ConsoleOperations {
  return {
    useTenants: () =>
      useQuery({ queryKey: TENANTS_KEY, queryFn: () => backend.listTenants() }),
    useCreateTenant: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleCreateTenantInput) =>
          backend.createTenant(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: TENANTS_KEY })
        },
      })
    },
    useSetTenantAlias: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleSetTenantAliasInput) =>
          backend.setTenantAlias(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: TENANTS_KEY })
        },
      })
    },
    useSetTenantStatus: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleSetTenantStatusInput) =>
          backend.setTenantStatus(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: TENANTS_KEY })
        },
      })
    },
    useSettings: (namespace: string | undefined) =>
      useQuery({
        queryKey: settingsKey(namespace),
        queryFn: () => backend.listSettings(namespace),
      }),
    useSetSetting: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleSetSettingInput) =>
          backend.setSetting(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: ['console-test', 'settings'] })
        },
      })
    },
    useRemoveSetting: () => {
      const client = useQueryClient()
      return useMutation({
        mutationFn: (input: ConsoleRemoveSettingInput) =>
          backend.removeSetting(input),
        onSuccess: () => {
          void client.invalidateQueries({ queryKey: ['console-test', 'settings'] })
        },
      })
    },
    useAuditRecords: () =>
      useQuery({ queryKey: AUDIT_KEY, queryFn: () => backend.listRecords() }),
  }
}
