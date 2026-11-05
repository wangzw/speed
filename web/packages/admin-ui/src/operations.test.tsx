/**
 * The operations seam: the shipped bundle's generated bindings, the
 * provider hook's fallback, and the key-splitting helper.
 *
 * `adminOperations` reads and writes through the generated
 * @speed/api-sdk surface; these tests bind a scripted request function
 * into the runtime seam and assert the endpoint each member drives, so a
 * member wired to the wrong operation fails here. With no
 * AdminOperationsProvider mounted, useAdminOperations answers with the
 * shipped adminOperations, so a page reads the generated surface with no
 * host-side binding.
 */

import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { getAuthnAdminAccountsSessionsQueryKey } from '@speed/api-sdk'
import { switchLanguage } from '@speed/i18n'
import {
  adminOperations,
  settingKeyRest,
  useAdminOperations,
} from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import { bindAdminRequest } from '../test-utils/request-rig.js'
import { recordFixture } from '../test-utils/operations.js'

describe('useAdminOperations', () => {
  it('falls back to the shipped bundle when no provider is mounted', async () => {
    const calls = bindAdminRequest((call) => {
      if (call.path === '/api/v1/system/tenants') {
        return { tenants: [{ id: 'acme', alias: 'Acme', status: 'active' }] }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    function Roster() {
      const { data } = useAdminOperations().useTenants()
      return <p>{data ? `roster:${data.map((t) => t.alias).join(',')}` : 'loading'}</p>
    }
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    // No AdminOperationsProvider in the tree: the page reads the
    // shipped adminOperations through the generated endpoint.
    render(
      <QueryClientProvider client={queryClient}>
        <Roster />
      </QueryClientProvider>,
    )
    expect(await screen.findByText('roster:Acme')).toBeInTheDocument()
    expect(calls).toContainEqual({ method: 'GET', path: '/api/v1/system/tenants' })
  })
})

describe('adminOperations', () => {
  it('reads the roster from the generated tenant endpoint', async () => {
    const calls = bindAdminRequest((call) => {
      if (call.path === '/api/v1/system/tenants') {
        return { tenants: [{ id: 'acme', alias: 'Acme', status: 'active' }] }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    function Roster() {
      const { data } = adminOperations.useTenants()
      return <p>{data ? `roster:${data.map((t) => t.alias).join(',')}` : 'loading'}</p>
    }
    renderWithProviders(<Roster />, { operations: adminOperations })
    expect(await screen.findByText('roster:Acme')).toBeInTheDocument()
    expect(calls).toContainEqual({ method: 'GET', path: '/api/v1/system/tenants' })
  })

  it('re-reads the permission catalog when the interface language changes', async () => {
    // The catalog's descriptions are rendered by the server in the request's
    // language, so a language switch must reissue the read rather than leave
    // the previous language's text cached under the same key.
    let answers = 0
    const calls = bindAdminRequest((call) => {
      if (call.path === '/api/v1/system/permissions') {
        answers += 1
        return {
          permissions: [
            { permission: `catalog.${answers}`, description: `d${answers}` },
          ],
        }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    function Catalog() {
      const { data } = adminOperations.usePermissions()
      return <p>{data ? data.map((p) => p.permission).join(',') : 'loading'}</p>
    }
    const { i18n } = renderWithProviders(<Catalog />, {
      operations: adminOperations,
    })
    const reads = () =>
      calls.filter((call) => call.path === '/api/v1/system/permissions').length
    expect(await screen.findByText('catalog.1')).toBeInTheDocument()
    expect(reads()).toBe(1)

    await act(async () => {
      await switchLanguage(i18n, 'en-US')
    })

    // The new key is a new read: the second answer replaces the first.
    expect(await screen.findByText('catalog.2')).toBeInTheDocument()
    expect(reads()).toBe(2)
  })

  it('writes a new tenant to the generated registration endpoint', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest((call) => {
      if (call.path === '/api/v1/system/tenants') {
        return { tenant: { id: 'acme', alias: 'Acme', status: 'active' } }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    function Create() {
      const create = adminOperations.useCreateTenant()
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
    renderWithProviders(<Create />, { operations: adminOperations })
    await user.click(screen.getByRole('button', { name: 'register' }))
    await waitFor(() => {
      expect(calls).toContainEqual({
        method: 'POST',
        path: '/api/v1/system/tenants',
      })
    })
  })

  it('reads the global settings layer from the generated settings endpoint', async () => {
    const calls = bindAdminRequest((call) => {
      if (call.path === '/api/v1/system/settings') {
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
            has_row: false,
            value: '3',
          },
        ]
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    function Settings() {
      const { data } = adminOperations.useSettings(undefined)
      return <p>{data ? `settings:${data.length}` : 'loading'}</p>
    }
    renderWithProviders(<Settings />, { operations: adminOperations })
    expect(await screen.findByText('settings:1')).toBeInTheDocument()
    expect(calls).toContainEqual({
      method: 'GET',
      path: '/api/v1/system/settings',
      query: { layer: 'global' },
    })
  })

  it('reads the audit journal from the generated records endpoint', async () => {
    const calls = bindAdminRequest((call) => {
      if (call.path === '/api/v1/system/audit/records') {
        return { entries: [], next: '' }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    function Audit() {
      const { data } = adminOperations.useAuditRecords()
      return <p>{data ? `audit:${data.entries.length}` : 'loading'}</p>
    }
    renderWithProviders(<Audit />, { operations: adminOperations })
    expect(await screen.findByText('audit:0')).toBeInTheDocument()
    expect(calls).toContainEqual({
      method: 'GET',
      path: '/api/v1/system/audit/records',
    })
  })

  it('streams the audit export from the generated endpoint', async () => {
    const calls = bindAdminRequest((call) => {
      if (call.path === '/api/v1/system/audit/export') {
        return new Blob(['{"id":"rec-1"}\n'], {
          type: 'application/x-ndjson',
        })
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    const exportRecords = adminOperations.exportAuditRecords
    expect(exportRecords).toBeDefined()
    const body = await exportRecords!({
      since: '2026-10-15T00:00:00Z',
      action: ['tenant.create'],
    })
    expect(body).toBeInstanceOf(Blob)
    expect(calls).toContainEqual({
      method: 'GET',
      path: '/api/v1/system/audit/export',
      query: { since: '2026-10-15T00:00:00Z', action: ['tenant.create'] },
    })
  })

  it('reads a single tenant from the generated detail endpoint', async () => {
    const calls = bindAdminRequest((call) => {
      if (call.path === '/api/v1/system/tenants/acme') {
        return { tenant: { id: 'acme', alias: 'Acme', status: 'active' } }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    function Detail() {
      const { data } = adminOperations.useTenant({ id: 'acme' })
      return <p>{data ? `tenant:${data.alias}` : 'loading'}</p>
    }
    renderWithProviders(<Detail />, { operations: adminOperations })
    expect(await screen.findByText('tenant:Acme')).toBeInTheDocument()
    expect(calls).toContainEqual({
      method: 'GET',
      path: '/api/v1/system/tenants/acme',
    })
  })

  it('reads a single audit record from the generated detail endpoint', async () => {
    const calls = bindAdminRequest((call) => {
      if (call.path === '/api/v1/system/audit/records/rec-1') {
        return recordFixture({ id: 'rec-1' })
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    function Detail() {
      const { data } = adminOperations.useAuditRecord({ id: 'rec-1' })
      return <p>{data ? `record:${data.id}` : 'loading'}</p>
    }
    renderWithProviders(<Detail />, { operations: adminOperations })
    expect(await screen.findByText('record:rec-1')).toBeInTheDocument()
    expect(calls).toContainEqual({
      method: 'GET',
      path: '/api/v1/system/audit/records/rec-1',
    })
  })

  it('drives the tenant status write endpoint', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest(answer)
    function Writes() {
      const status = adminOperations.useSetTenantStatus()
      return (
        <button
          onClick={() => status.mutate({ id: 'acme', status: 'disabled' })}
        >
          setStatus
        </button>
      )
    }
    renderWithProviders(<Writes />, { operations: adminOperations })
    await user.click(screen.getByRole('button', { name: 'setStatus' }))
    await waitFor(() => {
      expect(calls).toContainEqual({
        method: 'POST',
        path: '/api/v1/system/tenants/acme/status',
      })
    })
  })

  it('drives the settings write endpoints', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest(answer)
    function Writes() {
      const set = adminOperations.useSetSetting()
      const remove = adminOperations.useRemoveSetting()
      return (
        <>
          <button
            onClick={() =>
              set.mutate({ namespace: 'mail', key: 'mail.retry', value: '5' })
            }
          >
            setSetting
          </button>
          <button
            onClick={() =>
              remove.mutate({ namespace: 'mail', key: 'mail.retry' })
            }
          >
            removeSetting
          </button>
        </>
      )
    }
    renderWithProviders(<Writes />, { operations: adminOperations })
    await user.click(screen.getByRole('button', { name: 'setSetting' }))
    await user.click(screen.getByRole('button', { name: 'removeSetting' }))
    await waitFor(() => {
      expect(calls).toContainEqual({
        method: 'PUT',
        path: '/api/v1/system/settings/mail/mail.retry',
        query: { layer: 'global' },
      })
      expect(calls).toContainEqual({
        method: 'DELETE',
        path: '/api/v1/system/settings/mail/mail.retry',
        query: { layer: 'global' },
      })
    })
  })

  it('refreshes the detail read after a status write, which the list key does not cover', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest((call) => {
      if (call.method === 'GET' && call.path === '/api/v1/system/tenants/acme') {
        return { tenant: { id: 'acme', alias: 'Acme', status: 'active' } }
      }
      if (call.method === 'POST' && call.path === '/api/v1/system/tenants/acme/status') {
        return undefined
      }
      throw new Error(`unexpected request: ${call.method} ${call.path}`)
    })
    const detailReads = (): number =>
      calls.filter(
        (call) =>
          call.method === 'GET' && call.path === '/api/v1/system/tenants/acme',
      ).length
    function Detail() {
      const query = adminOperations.useTenant({ id: 'acme' })
      const status = adminOperations.useSetTenantStatus()
      return (
        <>
          <p>{query.data ? `tenant:${query.data.alias}` : 'loading'}</p>
          <button
            onClick={() => status.mutate({ id: 'acme', status: 'disabled' })}
          >
            disable
          </button>
        </>
      )
    }
    renderWithProviders(<Detail />, { operations: adminOperations })
    await screen.findByText('tenant:Acme')
    expect(detailReads()).toBe(1)
    await user.click(screen.getByRole('button', { name: 'disable' }))
    // The detail key ['/api/v1/system/tenants/acme'] is not prefixed by the
    // list key ['/api/v1/system/tenants'], so the write must invalidate it
    // on its own or the detail page keeps the pre-write value.
    await waitFor(() => {
      expect(detailReads()).toBeGreaterThan(1)
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

/**
 * The administration surfaces' members, driven through the shipped bundle
 * over the scripted request rig, so a member wired to the wrong endpoint
 * fails here. A probe answers `{}` for every leg (the generated read's
 * `select` then yields undefined, which is enough to settle the read);
 * the assertion is on the recorded request legs.
 */
function answer(): unknown {
  return {}
}

/** Mounts every tenant-scoped authorization read. */
function AuthzReads() {
  const permissions = adminOperations.usePermissions()
  const roles = adminOperations.useRoles('acme')
  const grants = adminOperations.useGrants('acme', 'editor')
  const bindings = adminOperations.useBindings('acme')
  const accountBindings = adminOperations.useAccountBindings({
    tenant: 'acme',
    account: 'acc-1',
  })
  const settled = [permissions, roles, grants, bindings, accountBindings].every(
    (query) => query.isSuccess,
  )
  return <p>{settled ? 'ready' : 'loading'}</p>
}

/** Buttons firing every authorization write. */
function AuthzWrites() {
  const writeRole = adminOperations.useWriteRole()
  const removeRole = adminOperations.useRemoveRole()
  const grant = adminOperations.useGrantPermission()
  const revoke = adminOperations.useRevokePermission()
  const bind = adminOperations.useBindRole()
  const unbind = adminOperations.useUnbindRole()
  return (
    <>
      <button onClick={() => writeRole.mutate({ tenant: 'acme', role: 'editor', description: 'd' })}>writeRole</button>
      <button onClick={() => removeRole.mutate({ tenant: 'acme', role: 'editor' })}>removeRole</button>
      <button onClick={() => grant.mutate({ tenant: 'acme', role: 'editor', permission: 'deal:read' })}>grant</button>
      <button onClick={() => revoke.mutate({ tenant: 'acme', role: 'editor', permission: 'deal:read' })}>revoke</button>
      <button onClick={() => bind.mutate({ tenant: 'acme', account: 'acc-1', role: 'editor' })}>bind</button>
      <button onClick={() => unbind.mutate({ tenant: 'acme', account: 'acc-1', role: 'editor' })}>unbind</button>
    </>
  )
}

/** Mounts every account / session / impersonation read. */
function AccountReads() {
  const accounts = adminOperations.useAccounts()
  const account = adminOperations.useAccount({ id: 'acc-1' })
  const factors = adminOperations.useAccountFactors({ id: 'acc-1' })
  const secrets = adminOperations.useAccountSecrets({ id: 'acc-1' })
  const sessions = adminOperations.useAccountSessions({ id: 'acc-1' })
  const impersonations = adminOperations.useImpersonations({ tenant: 'acme' })
  const settled = [accounts, account, factors, secrets, sessions, impersonations].every(
    (query) => query.isSuccess,
  )
  return <p>{settled ? 'ready' : 'loading'}</p>
}

/** Buttons firing every account / session / impersonation write. */
function AccountWrites() {
  const createAccount = adminOperations.useCreateAccount()
  const patchAccount = adminOperations.usePatchAccount()
  const setStatus = adminOperations.useSetAccountStatus()
  const setMFARequired = adminOperations.useSetMFARequired()
  const resetPassword = adminOperations.useResetPassword()
  const addIdentifier = adminOperations.useAddIdentifier()
  const createSecret = adminOperations.useCreateSecret()
  const del = adminOperations.useDeleteAccount()
  const removeIdentifier = adminOperations.useRemoveIdentifier()
  const resetMFA = adminOperations.useResetMFA()
  const revokeSessions = adminOperations.useRevokeAccountSessions()
  const revokeSecret = adminOperations.useRevokeSecret()
  const revokeSession = adminOperations.useRevokeSession()
  const revokeImpersonation = adminOperations.useRevokeImpersonation()
  return (
    <>
      <button onClick={() => createAccount.mutate({ kind: 'user' })}>createAccount</button>
      <button onClick={() => patchAccount.mutate({ id: 'acc-1', display_name: 'X' })}>patchAccount</button>
      <button onClick={() => setStatus.mutate({ id: 'acc-1', status: 'disabled' })}>setStatus</button>
      <button onClick={() => setMFARequired.mutate({ id: 'acc-1', required: true })}>setMFARequired</button>
      <button onClick={() => resetPassword.mutate({ id: 'acc-1', password: 'p' })}>resetPassword</button>
      <button onClick={() => addIdentifier.mutate({ id: 'acc-1', kind: 'username', value: 'x' })}>addIdentifier</button>
      <button onClick={() => createSecret.mutate({ id: 'acc-1', label: 'l' })}>createSecret</button>
      <button onClick={() => del.mutate({ id: 'acc-1' })}>del</button>
      <button onClick={() => removeIdentifier.mutate({ id: 'acc-1', identifier: 'ada@example.com' })}>removeIdentifier</button>
      <button onClick={() => resetMFA.mutate({ id: 'acc-1' })}>resetMFA</button>
      <button onClick={() => revokeSessions.mutate({ id: 'acc-1' })}>revokeSessions</button>
      <button onClick={() => revokeSecret.mutate({ id: 'acc-1', secret: 'sec-1' })}>revokeSecret</button>
      <button onClick={() => revokeSession.mutate({ accountId: 'acc-1', sessionId: 'sess-1' })}>revokeSession</button>
      <button onClick={() => revokeImpersonation.mutate({ id: 'imp-1', tenant: 'acme' })}>revokeImpersonation</button>
    </>
  )
}

/** Mounts the invitation / membership / SSO reads. */
function TenantReads() {
  const invitations = adminOperations.useInvitations('acme')
  const members = adminOperations.useMembers('acme')
  const providers = adminOperations.useSsoProviders('acme')
  const provider = adminOperations.useSsoProvider({ tenant: 'acme', id: 'idp-1' })
  const settled = [invitations, members, providers, provider].every((query) => query.isSuccess)
  return <p>{settled ? 'ready' : 'loading'}</p>
}

/** Buttons firing the invitation / membership / SSO writes. */
function TenantWrites() {
  const issue = adminOperations.useIssueInvitation()
  const revoke = adminOperations.useRevokeInvitation()
  const writeMember = adminOperations.useWriteMember()
  const setMemberSsoExempt = adminOperations.useSetMemberSsoExempt()
  const removeMember = adminOperations.useRemoveMember()
  const createProvider = adminOperations.useCreateSsoProvider()
  const updateProvider = adminOperations.useUpdateSsoProvider()
  const claimDomain = adminOperations.useClaimSsoDomain()
  const deleteProvider = adminOperations.useDeleteSsoProvider()
  const removeDomain = adminOperations.useRemoveSsoDomain()
  const verifyDomain = adminOperations.useVerifySsoDomain()
  const refresh = adminOperations.useRefreshSsoMetadata()
  const reverify = adminOperations.useReverifySsoDomains()
  return (
    <>
      <button onClick={() => issue.mutate({ tenant: 'acme', email: 'a@b.co', expiresAt: undefined })}>issue</button>
      <button onClick={() => revoke.mutate({ tenant: 'acme', id: 'inv-1' })}>revokeInvite</button>
      <button onClick={() => writeMember.mutate({ tenant: 'acme', account: 'acc-1', active: true, sso_exempt: false })}>writeMember</button>
      <button onClick={() => setMemberSsoExempt.mutate({ tenant: 'acme', account: 'acc-1', sso_exempt: true })}>setMemberSsoExempt</button>
      <button onClick={() => removeMember.mutate({ tenant: 'acme', account: 'acc-1' })}>removeMember</button>
      <button onClick={() => createProvider.mutate({ tenant: 'acme', protocol: 'oidc', name: 'n', provisioning: 'p' })}>createProvider</button>
      <button onClick={() => updateProvider.mutate({ id: 'idp-1', tenant: 'acme', protocol: 'oidc', name: 'n', provisioning: 'p' })}>updateProvider</button>
      <button onClick={() => claimDomain.mutate({ id: 'idp-1', tenant: 'acme', domain: 'acme.com' })}>claimDomain</button>
      <button onClick={() => deleteProvider.mutate({ id: 'idp-1', tenant: 'acme' })}>deleteProvider</button>
      <button onClick={() => removeDomain.mutate({ id: 'idp-1', tenant: 'acme', domain: 'acme.com' })}>removeDomain</button>
      <button onClick={() => verifyDomain.mutate({ id: 'idp-1', tenant: 'acme', domain: 'acme.com' })}>verifyDomain</button>
      <button onClick={() => refresh.mutate({ id: 'idp-1', tenant: 'acme' })}>refresh</button>
      <button onClick={() => reverify.mutate({ id: 'idp-1', tenant: 'acme' })}>reverify</button>
    </>
  )
}

/** Mounts the SMS template read and its writes. */
function SmsSurface() {
  const templates = adminOperations.useSmsTemplates()
  const register = adminOperations.useRegisterSmsTemplate()
  const offer = adminOperations.useOfferSmsTemplate()
  const setEnabled = adminOperations.useSetSmsTemplateEnabled()
  const remove = adminOperations.useRemoveSmsTemplate()
  return (
    <>
      <p>{templates.isSuccess ? 'ready' : 'loading'}</p>
      <button onClick={() => register.mutate({ name: 'n', params: ['p'], codes: { 'zh-CN': '1' } })}>register</button>
      <button onClick={() => offer.mutate({ name: 'n', content: 'c', kind: 'k', locale: 'zh-CN', params: ['p'], remark: 'r' })}>offer</button>
      <button onClick={() => setEnabled.mutate({ name: 'n', enabled: false })}>setEnabled</button>
      <button onClick={() => remove.mutate({ name: 'n' })}>remove</button>
    </>
  )
}

describe('adminOperations administration surfaces', () => {
  it('drives the authorization read endpoints', async () => {
    const calls = bindAdminRequest(answer)
    renderWithProviders(<AuthzReads />, { operations: adminOperations })
    expect(await screen.findByText('ready')).toBeInTheDocument()
    for (const path of [
      '/api/v1/system/permissions',
      '/api/v1/system/tenants/acme/roles',
      '/api/v1/system/tenants/acme/roles/editor/grants',
      '/api/v1/system/tenants/acme/bindings',
      '/api/v1/system/tenants/acme/bindings/acc-1',
    ]) {
      expect(calls).toContainEqual({ method: 'GET', path })
    }
  })

  it('drives the authorization write endpoints', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest(answer)
    renderWithProviders(<AuthzWrites />, { operations: adminOperations })
    for (const name of ['writeRole', 'removeRole', 'grant', 'revoke', 'bind', 'unbind']) {
      await user.click(screen.getByRole('button', { name }))
    }
    await waitFor(() => {
      expect(calls).toContainEqual({ method: 'PUT', path: '/api/v1/system/tenants/acme/roles/editor' })
      expect(calls).toContainEqual({ method: 'DELETE', path: '/api/v1/system/tenants/acme/roles/editor' })
      expect(calls).toContainEqual({ method: 'PUT', path: '/api/v1/system/tenants/acme/roles/editor/grants/deal:read' })
      expect(calls).toContainEqual({ method: 'DELETE', path: '/api/v1/system/tenants/acme/roles/editor/grants/deal:read' })
      expect(calls).toContainEqual({ method: 'PUT', path: '/api/v1/system/tenants/acme/bindings/acc-1/editor' })
      expect(calls).toContainEqual({ method: 'DELETE', path: '/api/v1/system/tenants/acme/bindings/acc-1/editor' })
    })
  })

  it('drives the account, session and impersonation read endpoints', async () => {
    const calls = bindAdminRequest(answer)
    renderWithProviders(<AccountReads />, { operations: adminOperations })
    expect(await screen.findByText('ready')).toBeInTheDocument()
    for (const path of [
      '/api/v1/system/auth/accounts',
      '/api/v1/system/auth/accounts/acc-1',
      '/api/v1/system/auth/accounts/acc-1/factors',
      '/api/v1/system/auth/accounts/acc-1/secrets',
      '/api/v1/system/auth/accounts/acc-1/sessions',
      '/api/v1/system/tenants/acme/impersonations',
    ]) {
      expect(calls.some((c) => c.method === 'GET' && c.path === path)).toBe(true)
    }
  })

  it('holds the impersonation record read while its caller disables it', async () => {
    const calls = bindAdminRequest(answer)
    function Held() {
      const impersonations = adminOperations.useImpersonations(
        { tenant: 'acme' },
        { enabled: false },
      )
      return <p>{impersonations.isPending ? 'held' : 'settled'}</p>
    }
    renderWithProviders(<Held />, { operations: adminOperations })
    expect(await screen.findByText('held')).toBeInTheDocument()
    // Flush the queue so an accidental request would have been recorded.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(
      calls.some(
        (call) =>
          call.method === 'GET' && call.path === '/api/v1/system/tenants/acme/impersonations',
      ),
    ).toBe(false)
  })

  it('drives the account, session and impersonation write endpoints', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest(answer)
    renderWithProviders(<AccountWrites />, { operations: adminOperations })
    for (const name of ['createAccount', 'patchAccount', 'setStatus', 'setMFARequired', 'resetPassword', 'addIdentifier', 'createSecret', 'del', 'removeIdentifier', 'resetMFA', 'revokeSessions', 'revokeSecret', 'revokeSession', 'revokeImpersonation']) {
      await user.click(screen.getByRole('button', { name }))
    }
    await waitFor(() => {
      expect(calls).toContainEqual({ method: 'POST', path: '/api/v1/system/auth/accounts' })
      expect(calls).toContainEqual({ method: 'PATCH', path: '/api/v1/system/auth/accounts/acc-1' })
      expect(calls).toContainEqual({ method: 'POST', path: '/api/v1/system/auth/accounts/acc-1/status' })
      expect(calls).toContainEqual({ method: 'POST', path: '/api/v1/system/auth/accounts/acc-1/mfa-required' })
      expect(calls).toContainEqual({ method: 'POST', path: '/api/v1/system/auth/accounts/acc-1/password' })
      expect(calls).toContainEqual({ method: 'POST', path: '/api/v1/system/auth/accounts/acc-1/identifiers' })
      expect(calls).toContainEqual({ method: 'POST', path: '/api/v1/system/auth/accounts/acc-1/secrets' })
      expect(calls).toContainEqual({ method: 'DELETE', path: '/api/v1/system/auth/accounts/acc-1' })
      expect(calls).toContainEqual({ method: 'DELETE', path: '/api/v1/system/auth/accounts/acc-1/identifiers/ada@example.com' })
      expect(calls).toContainEqual({ method: 'POST', path: '/api/v1/system/auth/accounts/acc-1/mfa-reset' })
      expect(calls).toContainEqual({ method: 'POST', path: '/api/v1/system/auth/accounts/acc-1/revoke-sessions' })
      expect(calls).toContainEqual({ method: 'DELETE', path: '/api/v1/system/auth/accounts/acc-1/secrets/sec-1' })
      expect(calls).toContainEqual({ method: 'DELETE', path: '/api/v1/system/auth/sessions/sess-1' })
      expect(calls).toContainEqual({ method: 'DELETE', path: '/api/v1/system/tenants/acme/impersonations/imp-1' })
    })
  })

  it('drives the invitation, membership and SSO read endpoints', async () => {
    const calls = bindAdminRequest(answer)
    renderWithProviders(<TenantReads />, { operations: adminOperations })
    expect(await screen.findByText('ready')).toBeInTheDocument()
    for (const path of [
      '/api/v1/tenants/acme/invitations',
      '/api/v1/tenants/acme/members',
      '/api/v1/tenants/acme/sso/providers',
      '/api/v1/tenants/acme/sso/providers/idp-1',
    ]) {
      expect(calls).toContainEqual({ method: 'GET', path })
    }
  })

  it('drives the invitation, membership and SSO write endpoints', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest(answer)
    renderWithProviders(<TenantWrites />, { operations: adminOperations })
    for (const name of ['issue', 'revokeInvite', 'writeMember', 'setMemberSsoExempt', 'removeMember', 'createProvider', 'updateProvider', 'claimDomain', 'deleteProvider', 'removeDomain', 'verifyDomain', 'refresh', 'reverify']) {
      await user.click(screen.getByRole('button', { name }))
    }
    await waitFor(() => {
      expect(calls).toContainEqual({ method: 'POST', path: '/api/v1/tenants/acme/invitations' })
      expect(calls).toContainEqual({ method: 'DELETE', path: '/api/v1/tenants/acme/invitations/inv-1' })
      expect(calls).toContainEqual({ method: 'PUT', path: '/api/v1/tenants/acme/members/acc-1' })
      expect(calls).toContainEqual({ method: 'PUT', path: '/api/v1/tenants/acme/members/acc-1/sso-exempt' })
      expect(calls).toContainEqual({ method: 'POST', path: '/api/v1/tenants/acme/sso/providers' })
      expect(calls).toContainEqual({ method: 'PUT', path: '/api/v1/tenants/acme/sso/providers/idp-1' })
      expect(calls).toContainEqual({ method: 'POST', path: '/api/v1/tenants/acme/sso/providers/idp-1/domains' })
      expect(calls).toContainEqual({ method: 'DELETE', path: '/api/v1/tenants/acme/members/acc-1' })
      expect(calls).toContainEqual({ method: 'DELETE', path: '/api/v1/tenants/acme/sso/providers/idp-1' })
      expect(calls).toContainEqual({ method: 'DELETE', path: '/api/v1/tenants/acme/sso/providers/idp-1/domains/acme.com' })
      expect(calls).toContainEqual({ method: 'POST', path: '/api/v1/tenants/acme/sso/providers/idp-1/domains/acme.com/verify' })
      expect(calls).toContainEqual({ method: 'POST', path: '/api/v1/tenants/acme/sso/providers/idp-1/metadata/refresh' })
      expect(calls).toContainEqual({ method: 'POST', path: '/api/v1/tenants/acme/sso/providers/idp-1/reverify-domains' })
    })
  })

  it('drives the SMS template endpoints', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest(answer)
    renderWithProviders(<SmsSurface />, { operations: adminOperations })
    await screen.findByText('ready')
    for (const name of ['register', 'offer', 'setEnabled', 'remove']) {
      await user.click(screen.getByRole('button', { name }))
    }
    await waitFor(() => {
      expect(calls).toContainEqual({ method: 'GET', path: '/api/v1/system/sms/templates' })
      expect(calls).toContainEqual({ method: 'POST', path: '/api/v1/system/sms/templates' })
      expect(calls).toContainEqual({ method: 'POST', path: '/api/v1/system/sms/templates/submissions' })
      expect(calls).toContainEqual({ method: 'POST', path: '/api/v1/system/sms/templates/n/enabled' })
      expect(calls).toContainEqual({ method: 'DELETE', path: '/api/v1/system/sms/templates/n' })
    })
  })
})

/**
 * The write contract: a successful write invalidates every read its result
 * changes, including reads outside the section that started it. Each probe
 * mounts the reads a write leaves stale and counts the request legs the
 * invalidation adds. A refetch is the observable proof that the generated
 * key matched; a spy on invalidateQueries would not prove the predicate
 * matched the cached key.
 */
describe('adminOperations write invalidation', () => {
  it('re-reads every account-binding query under the tenant after a role removal', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest(answer)
    function Probe() {
      const roles = adminOperations.useRoles('acme')
      const grants = adminOperations.useGrants('acme', 'editor')
      const bindings = adminOperations.useBindings('acme')
      const own = adminOperations.useAccountBindings({
        tenant: 'acme',
        account: 'acc-1',
      })
      const foreign = adminOperations.useAccountBindings({
        tenant: 'other',
        account: 'acc-1',
      })
      const removeRole = adminOperations.useRemoveRole()
      const settled = [roles, grants, bindings, own, foreign].every(
        (query) => query.isSuccess,
      )
      return (
        <>
          <p>{settled ? 'ready' : 'loading'}</p>
          <button
            type="button"
            onClick={() =>
              removeRole.mutate({ tenant: 'acme', role: 'editor' })
            }
          >
            removeRole
          </button>
        </>
      )
    }
    renderWithProviders(<Probe />, { operations: adminOperations })
    await screen.findByText('ready')
    const reads = (path: string): number =>
      calls.filter((call) => call.method === 'GET' && call.path === path).length
    const paths = {
      roles: '/api/v1/system/tenants/acme/roles',
      grants: '/api/v1/system/tenants/acme/roles/editor/grants',
      bindings: '/api/v1/system/tenants/acme/bindings',
      accountBindings: '/api/v1/system/tenants/acme/bindings/acc-1',
      otherTenant: '/api/v1/system/tenants/other/bindings/acc-1',
    }
    for (const path of Object.values(paths)) {
      expect(reads(path)).toBe(1)
    }
    await user.click(screen.getByRole('button', { name: 'removeRole' }))
    await waitFor(() => {
      for (const path of [
        paths.roles,
        paths.grants,
        paths.bindings,
        paths.accountBindings,
      ]) {
        expect(reads(path)).toBeGreaterThan(1)
      }
    })
    // The removal names no account, so it invalidates by tenant; that
    // bound is what leaves another tenant's account-binding read alone.
    expect(reads(paths.otherTenant)).toBe(1)
  })

  it('re-reads the search page after an identifier removal, symmetric with adding one', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest(answer)
    function Probe() {
      const accounts = adminOperations.useAccounts({
        identifier_prefix: 'ada',
        identifier_kind: 'email',
      })
      const account = adminOperations.useAccount({ id: 'acc-1' })
      const removeIdentifier = adminOperations.useRemoveIdentifier()
      const settled = accounts.isSuccess && account.isSuccess
      return (
        <>
          <p>{settled ? 'ready' : 'loading'}</p>
          <button
            type="button"
            onClick={() =>
              removeIdentifier.mutate({
                id: 'acc-1',
                identifier: 'ada@example.com',
              })
            }
          >
            removeIdentifier
          </button>
        </>
      )
    }
    renderWithProviders(<Probe />, { operations: adminOperations })
    await screen.findByText('ready')
    const reads = (path: string): number =>
      calls.filter((call) => call.method === 'GET' && call.path === path).length
    expect(reads('/api/v1/system/auth/accounts')).toBe(1)
    expect(reads('/api/v1/system/auth/accounts/acc-1')).toBe(1)
    await user.click(screen.getByRole('button', { name: 'removeIdentifier' }))
    await waitFor(() => {
      expect(reads('/api/v1/system/auth/accounts')).toBeGreaterThan(1)
      expect(reads('/api/v1/system/auth/accounts/acc-1')).toBeGreaterThan(1)
    })
  })

  it('re-reads the account sessions when a status write disables it', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest(answer)
    function Probe() {
      const sessions = adminOperations.useAccountSessions({ id: 'acc-1' })
      const otherSessions = adminOperations.useAccountSessions({
        id: 'acc-2',
      })
      const setStatus = adminOperations.useSetAccountStatus()
      const settled = sessions.isSuccess && otherSessions.isSuccess
      return (
        <>
          <p>{settled ? 'ready' : 'loading'}</p>
          <button
            type="button"
            onClick={() =>
              setStatus.mutate({ id: 'acc-1', status: 'disabled' })
            }
          >
            disable
          </button>
        </>
      )
    }
    renderWithProviders(<Probe />, { operations: adminOperations })
    await screen.findByText('ready')
    const reads = (path: string): number =>
      calls.filter((call) => call.method === 'GET' && call.path === path).length
    expect(reads('/api/v1/system/auth/accounts/acc-1/sessions')).toBe(1)
    expect(reads('/api/v1/system/auth/accounts/acc-2/sessions')).toBe(1)
    await user.click(screen.getByRole('button', { name: 'disable' }))
    await waitFor(() => {
      expect(reads('/api/v1/system/auth/accounts/acc-1/sessions')).toBeGreaterThan(1)
    })
    // The write is scoped to the account it disables.
    expect(reads('/api/v1/system/auth/accounts/acc-2/sessions')).toBe(1)
  })

  it('leaves the sessions read alone when a status write re-enables the account', async () => {
    // Judging "no second read" needs the write's invalidation work to have
    // settled; a fixed flush passes while a refetch it scheduled is still
    // pending, so a refetch on a later tick would slip through. Fake timers
    // let the case advance past a retry-scale delay, which flushes any
    // invalidation the write scheduled for a later tick.
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const user = userEvent.setup({
        advanceTimers: vi.advanceTimersByTimeAsync,
      })
      const calls = bindAdminRequest(answer)
      function Probe() {
        const [settled, setSettled] = useState(false)
        const sessions = adminOperations.useAccountSessions({ id: 'acc-1' })
        const setStatus = adminOperations.useSetAccountStatus()
        return (
          <>
            <p>{sessions.isSuccess ? (settled ? 'settled' : 'ready') : 'loading'}</p>
            <button
              type="button"
              onClick={() =>
                setStatus.mutate(
                  { id: 'acc-1', status: 'active' },
                  { onSuccess: () => setSettled(true) },
                )
              }
            >
              enable
            </button>
          </>
        )
      }
      const { queryClient } = renderWithProviders(<Probe />, {
        operations: adminOperations,
      })
      await screen.findByText('ready')
      const reads = (): number =>
        calls.filter(
          (call) =>
            call.method === 'GET' &&
            call.path === '/api/v1/system/auth/accounts/acc-1/sessions',
        ).length
      expect(reads()).toBe(1)
      await user.click(screen.getByRole('button', { name: 'enable' }))
      await screen.findByText('settled')
      // The write's onSuccess invalidates the reads its result changes; any
      // refetch that invalidation starts must settle before the counter can
      // rule a second read out.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(2_000)
      })
      // The settled state that carries the write's invalidation work is the
      // query cache: no read may be in flight when the count is judged.
      await waitFor(() => {
        expect(queryClient.isFetching()).toBe(0)
        expect(
          queryClient.getQueryState(
            getAuthnAdminAccountsSessionsQueryKey('acc-1'),
          )?.fetchStatus,
        ).toBe('idle')
      })
      expect(reads()).toBe(1)
    } finally {
      vi.useRealTimers()
    }
  })

  it('re-reads the account sessions after an MFA reset, which revokes them', async () => {
    const user = userEvent.setup()
    const calls = bindAdminRequest(answer)
    function Probe() {
      const factors = adminOperations.useAccountFactors({ id: 'acc-1' })
      const sessions = adminOperations.useAccountSessions({ id: 'acc-1' })
      const otherSessions = adminOperations.useAccountSessions({
        id: 'acc-2',
      })
      const resetMFA = adminOperations.useResetMFA()
      const settled =
        factors.isSuccess && sessions.isSuccess && otherSessions.isSuccess
      return (
        <>
          <p>{settled ? 'ready' : 'loading'}</p>
          <button
            type="button"
            onClick={() => resetMFA.mutate({ id: 'acc-1' })}
          >
            resetMFA
          </button>
        </>
      )
    }
    renderWithProviders(<Probe />, { operations: adminOperations })
    await screen.findByText('ready')
    const reads = (path: string): number =>
      calls.filter((call) => call.method === 'GET' && call.path === path).length
    expect(reads('/api/v1/system/auth/accounts/acc-1/factors')).toBe(1)
    expect(reads('/api/v1/system/auth/accounts/acc-1/sessions')).toBe(1)
    expect(reads('/api/v1/system/auth/accounts/acc-2/sessions')).toBe(1)
    await user.click(screen.getByRole('button', { name: 'resetMFA' }))
    await waitFor(() => {
      expect(reads('/api/v1/system/auth/accounts/acc-1/factors')).toBeGreaterThan(1)
      expect(reads('/api/v1/system/auth/accounts/acc-1/sessions')).toBeGreaterThan(1)
    })
    // The reset is scoped to the account it resets.
    expect(reads('/api/v1/system/auth/accounts/acc-2/sessions')).toBe(1)
  })
})
