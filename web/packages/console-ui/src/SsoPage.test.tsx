/**
 * SsoPage: the SSO-provider administration page.
 *
 * It prompts for a tenant until one is chosen, lists a tenant's providers
 * and drives delete / refresh-metadata / reverify-domains and the
 * per-domain verify and remove through the bundle.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SsoPage } from './SsoPage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createConsoleOperations,
  makeConsoleBackend,
  providerFixture,
  type ConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'

const TENANT = { id: 'acme', alias: 'Acme', status: 'active' as const }

async function pickTenant(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(screen.getByLabelText('租户'))
  await user.click(await screen.findByRole('option', { name: 'Acme' }))
}

function withProviders(
  overrides: Partial<ConsoleBackend> = {},
): ReturnType<typeof createConsoleOperations> {
  return createConsoleOperations(
    makeConsoleBackend({
      listTenants: () => Promise.resolve([TENANT]),
      listSsoProviders: () =>
        Promise.resolve([providerFixture({ name: 'Acme IdP' })]),
      ...overrides,
    }),
  )
}

async function pickProvider(user: ReturnType<typeof userEvent.setup>): Promise<void> {
  await user.click(screen.getByLabelText('提供方'))
  await user.click(await screen.findByRole('option', { name: 'Acme IdP' }))
}

describe('SsoPage', () => {
  it('prompts for a tenant before listing', () => {
    const operations = withProviders()
    renderWithProviders(<SsoPage />, { operations })
    expect(screen.getByText('请选择租户')).toBeInTheDocument()
  })

  it('lists the tenant providers', async () => {
    const user = userEvent.setup()
    const operations = withProviders()
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText('Acme IdP')).toBeInTheDocument()
    expect(screen.getByText('oidc')).toBeInTheDocument()
  })

  it('deletes a provider through the bundle', async () => {
    const user = userEvent.setup()
    const deleted: string[] = []
    const operations = withProviders({
      deleteSsoProvider: (input) => {
        deleted.push(input.id)
        return Promise.resolve()
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.click(screen.getByRole('button', { name: '删除' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    await vi.waitFor(() => {
      expect(deleted).toEqual(['idp-1'])
    })
  })

  it('refreshes metadata and re-verifies domains through the bundle', async () => {
    const user = userEvent.setup()
    const refreshed: string[] = []
    const reverified: string[] = []
    const operations = withProviders({
      refreshSsoMetadata: (input) => {
        refreshed.push(input.id)
        return Promise.resolve()
      },
      reverifySsoDomains: (input) => {
        reverified.push(input.id)
        return Promise.resolve()
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await user.click(screen.getByRole('button', { name: '刷新元数据' }))
    await user.click(screen.getByRole('button', { name: '重新验证域名' }))
    await vi.waitFor(() => {
      expect(refreshed).toEqual(['idp-1'])
      expect(reverified).toEqual(['idp-1'])
    })
  })

  it('verifies and removes a named domain through the bundle', async () => {
    const user = userEvent.setup()
    const verified: string[] = []
    const removed: string[] = []
    const operations = withProviders({
      verifySsoDomain: (input) => {
        verified.push(input.domain)
        return Promise.resolve()
      },
      removeSsoDomain: (input) => {
        removed.push(input.domain)
        return Promise.resolve()
      },
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await pickProvider(user)
    await user.type(screen.getByLabelText('域名'), 'acme.com')
    await user.click(screen.getByRole('button', { name: '验证' }))
    await user.click(screen.getByRole('button', { name: '移除' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '确认' }))
    await vi.waitFor(() => {
      expect(verified).toEqual(['acme.com'])
      expect(removed).toEqual(['acme.com'])
    })
  })

  it('renders the retryable error placeholder when the read fails', async () => {
    const user = userEvent.setup()
    const operations = withProviders({
      listSsoProviders: () => Promise.reject(new Error('boom')),
    })
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    expect(await screen.findByText('提供方加载失败')).toBeInTheDocument()
  })

  it('has no axe violations', async () => {
    const user = userEvent.setup()
    const operations = withProviders()
    renderWithProviders(<SsoPage />, { operations })
    await pickTenant(user)
    await screen.findByText('Acme IdP')
    await expectNoAxeViolations()
  })
})
