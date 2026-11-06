/**
 * TenantCreatePage: the shared form in platform mode, the profile fields
 * it sends, the display-name requirement, the alias rules, a refused
 * registration's error text, and the host hand-off on success.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { orgCreateUiResources } from '@speed/org-create-ui'
import type { AdminCreateTenantInput } from './operations.js'
import { TenantCreatePage } from './TenantCreatePage.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createAdminOperations,
  makeAdminBackend,
  tenantFixture,
} from '../test-utils/operations.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const orgZh = orgCreateUiResources['zh-CN'] as unknown as {
  readonly fields: {
    readonly id: string
    readonly alias: string
    readonly displayName: string
    readonly description: string
    readonly contactEmail: string
    readonly website: string
  }
  readonly validation: { readonly required: string; readonly aliasReserved: string }
  readonly submit: string
}

async function fill(
  user: ReturnType<typeof userEvent.setup>,
  overrides: Partial<Record<'alias' | 'displayName' | 'description' | 'contactEmail' | 'website', string>> = {},
) {
  const values = {
    alias: 'acme',
    displayName: 'Acme Ltd',
    description: 'A team',
    contactEmail: 'team@example.com',
    website: 'https://example.com',
    ...overrides,
  }
  for (const [field, value] of Object.entries(values)) {
    const label = orgZh.fields[field as keyof typeof orgZh.fields]
    const input = screen.getByLabelText(label)
    await user.clear(input)
    if (value !== '') {
      await user.type(input, value)
    }
  }
}

describe('TenantCreatePage', () => {
  it('requires the display name before the write', async () => {
    const user = userEvent.setup()
    const createTenant = vi.fn()
    const operations = createAdminOperations(
      makeAdminBackend({ createTenant }),
    )
    renderWithProviders(<TenantCreatePage onCreated={vi.fn()} />, {
      operations,
    })

    await user.click(screen.getByRole('button', { name: zhCN.tenants.create.submit }))

    expect(createTenant).not.toHaveBeenCalled()
    expect(
      screen.getByLabelText(orgZh.fields.displayName),
    ).toHaveAccessibleDescription(orgZh.validation.required)
  })

  it('shows a reserved alias in place before the first submit', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(makeAdminBackend())
    renderWithProviders(<TenantCreatePage onCreated={vi.fn()} />, {
      operations,
    })

    await user.type(screen.getByLabelText(orgZh.fields.alias), 'admin')

    expect(
      screen.getByLabelText(orgZh.fields.alias),
    ).toHaveAccessibleDescription(orgZh.validation.aliasReserved)
  })

  it('registers with every profile field and hands the tenant to the host', async () => {
    const user = userEvent.setup()
    let received: AdminCreateTenantInput | undefined
    const operations = createAdminOperations(
      makeAdminBackend({
        createTenant: (input) => {
          received = input
          return Promise.resolve(
            tenantFixture({
              alias: input.alias,
              displayName: input.displayName,
              description: input.description,
              contactEmail: input.contactEmail,
              website: input.website,
              status: 'active',
            }),
          )
        },
      }),
    )
    const onCreated = vi.fn()
    renderWithProviders(<TenantCreatePage onCreated={onCreated} />, {
      operations,
    })

    await fill(user, {
      alias: '  acme  ',
      displayName: '  Acme Ltd  ',
      description: '  A team  ',
    })
    await user.click(screen.getByRole('button', { name: zhCN.tenants.create.submit }))

    await waitFor(() => {
      expect(onCreated).toHaveBeenCalledTimes(1)
    })
    expect(received).toEqual({
      alias: 'acme',
      displayName: 'Acme Ltd',
      description: 'A team',
      contactEmail: 'team@example.com',
      website: 'https://example.com',
    })
    expect(onCreated.mock.calls[0]?.[0]).toMatchObject({
      alias: 'acme',
      displayName: 'Acme Ltd',
    })
  })

  it('keeps the draft and renders the conflict text when refused', async () => {
    const user = userEvent.setup()
    const operations = createAdminOperations(
      makeAdminBackend({
        createTenant: () => Promise.reject({ code: 'tenant.conflict' }),
      }),
    )
    renderWithProviders(<TenantCreatePage onCreated={vi.fn()} />, {
      operations,
    })

    await fill(user)
    await user.click(screen.getByRole('button', { name: zhCN.tenants.create.submit }))

    expect(
      await screen.findByText(zhCN.errors.tenant.conflict),
    ).toBeInTheDocument()
    expect(screen.getByLabelText(orgZh.fields.alias)).toHaveValue('acme')
    expect(screen.getByLabelText(orgZh.fields.displayName)).toHaveValue(
      'Acme Ltd',
    )
  })

  it('passes blank optional profile fields through to the operation', async () => {
    const user = userEvent.setup()
    let received: AdminCreateTenantInput | undefined
    const operations = createAdminOperations(
      makeAdminBackend({
        createTenant: (input) => {
          received = input
          return Promise.resolve(
            tenantFixture({
              alias: input.alias,
              displayName: input.displayName,
            }),
          )
        },
      }),
    )
    renderWithProviders(<TenantCreatePage onCreated={vi.fn()} />, {
      operations,
    })

    await fill(user, { description: '', contactEmail: '', website: '' })
    await user.click(screen.getByRole('button', { name: zhCN.tenants.create.submit }))

    await waitFor(() => {
      expect(received).toBeDefined()
    })
    expect(received?.description).toBe('')
    expect(received?.contactEmail).toBe('')
    expect(received?.website).toBe('')
  })
})
