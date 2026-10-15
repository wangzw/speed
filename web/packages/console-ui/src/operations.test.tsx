/**
 * The operations seam: the shipped bundle's generated bindings, the
 * provider hook's fallback, and the key-splitting helper.
 *
 * `consoleOperations` reads and writes through the generated
 * @speed/api-sdk surface; these tests bind a scripted request function
 * into the runtime seam and assert the endpoint each member drives, so a
 * member wired to the wrong operation fails here. With no
 * ConsoleOperationsProvider mounted, useConsoleOperations answers with the
 * shipped consoleOperations, so a page reads the generated surface with no
 * host-side binding.
 */

import { describe, expect, it } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  consoleOperations,
  settingKeyRest,
  useConsoleOperations,
} from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { bindConsoleRequest } from '../test-utils/request-rig.js'

describe('useConsoleOperations', () => {
  it('falls back to the shipped bundle when no provider is mounted', async () => {
    const calls = bindConsoleRequest((call) => {
      if (call.path === '/tenant/admin/tenants') {
        return { tenants: [{ id: 'acme', alias: 'Acme', status: 'active' }] }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    function Roster() {
      const { data } = useConsoleOperations().useTenants()
      return <p>{data ? `roster:${data.map((t) => t.alias).join(',')}` : 'loading'}</p>
    }
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    // No ConsoleOperationsProvider in the tree: the page reads the
    // shipped consoleOperations through the generated endpoint.
    render(
      <QueryClientProvider client={queryClient}>
        <Roster />
      </QueryClientProvider>,
    )
    expect(await screen.findByText('roster:Acme')).toBeInTheDocument()
    expect(calls).toContainEqual({ method: 'GET', path: '/tenant/admin/tenants' })
  })
})

describe('consoleOperations', () => {
  it('reads the roster from the generated tenant endpoint', async () => {
    const calls = bindConsoleRequest((call) => {
      if (call.path === '/tenant/admin/tenants') {
        return { tenants: [{ id: 'acme', alias: 'Acme', status: 'active' }] }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    function Roster() {
      const { data } = consoleOperations.useTenants()
      return <p>{data ? `roster:${data.map((t) => t.alias).join(',')}` : 'loading'}</p>
    }
    renderWithProviders(<Roster />, { operations: consoleOperations })
    expect(await screen.findByText('roster:Acme')).toBeInTheDocument()
    expect(calls).toContainEqual({ method: 'GET', path: '/tenant/admin/tenants' })
  })

  it('writes a new tenant to the generated registration endpoint', async () => {
    const user = userEvent.setup()
    const calls = bindConsoleRequest((call) => {
      if (call.path === '/tenant/admin/tenants') {
        return { tenant: { id: 'acme', alias: 'Acme', status: 'active' } }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    function Create() {
      const create = consoleOperations.useCreateTenant()
      return (
        <button
          type="button"
          onClick={() => {
            create.mutate({ id: 'acme', alias: 'Acme' })
          }}
        >
          register
        </button>
      )
    }
    renderWithProviders(<Create />, { operations: consoleOperations })
    await user.click(screen.getByRole('button', { name: 'register' }))
    await waitFor(() => {
      expect(calls).toContainEqual({
        method: 'POST',
        path: '/tenant/admin/tenants',
      })
    })
  })

  it('reads the global settings layer from the generated settings endpoint', async () => {
    const calls = bindConsoleRequest((call) => {
      if (call.path === '/settings/admin/settings') {
        return [
          {
            key: 'mail.retry',
            namespace: 'mail',
            type: 'int',
            description: '',
            domain: '',
            sensitive: false,
            default: '3',
            written: '',
            value: '3',
          },
        ]
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    function Settings() {
      const { data } = consoleOperations.useSettings(undefined)
      return <p>{data ? `settings:${data.length}` : 'loading'}</p>
    }
    renderWithProviders(<Settings />, { operations: consoleOperations })
    expect(await screen.findByText('settings:1')).toBeInTheDocument()
    expect(calls).toContainEqual({
      method: 'GET',
      path: '/settings/admin/settings',
    })
  })

  it('reads the audit journal from the generated records endpoint', async () => {
    const calls = bindConsoleRequest((call) => {
      if (call.path === '/audit/admin/records') {
        return { entries: [], next: '' }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    function Audit() {
      const { data } = consoleOperations.useAuditRecords()
      return <p>{data ? `audit:${data.length}` : 'loading'}</p>
    }
    renderWithProviders(<Audit />, { operations: consoleOperations })
    expect(await screen.findByText('audit:0')).toBeInTheDocument()
    expect(calls).toContainEqual({
      method: 'GET',
      path: '/audit/admin/records',
    })
  })
})

describe('settingKeyRest', () => {
  it('strips the namespace prefix from the full key', () => {
    expect(settingKeyRest('mail.retry', 'mail')).toBe('retry')
  })

  it('splits by the known namespace, not the first dot', () => {
    // A module name may itself contain a dot (authn.session), so the
    // split is by the namespace the caller passes.
    expect(settingKeyRest('authn.session.timeout', 'authn.session')).toBe(
      'timeout',
    )
  })

  it('leaves a key that does not carry the namespace prefix unchanged', () => {
    expect(settingKeyRest('retry', 'mail')).toBe('retry')
  })
})
