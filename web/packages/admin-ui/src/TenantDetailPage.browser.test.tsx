// jsdom cannot tell a natively disabled control from an aria-disabled one, so
// these focus cases run in Chromium.
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { page } from 'vitest/browser'
import { attachSession } from '@speed/auth-core'
import { CONFIRM_ARM_LOCKOUT_MS, uiKitResources } from '@speed/ui-kit'
import { TenantDetailPage } from './TenantDetailPage.js'
import type { AdminTenant } from './operations.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createAdminOperations,
  makeAdminBackend,
  tenantFixture,
} from '../test-utils/operations.js'
import {
  AUTHENTICATED_SNAPSHOT,
  createStepUpSession,
} from '../test-utils/session.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const CONFIRM_AGAIN_LABEL = (
  uiKitResources['zh-CN'] as unknown as {
    readonly confirmDialog: { readonly confirmAgainLabel: string }
  }
).confirmDialog.confirmAgainLabel

function deferred<T>(): {
  readonly promise: Promise<T>
  readonly resolve: (value: T) => void
} {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((settle) => {
    resolve = settle
  })
  return { promise, resolve }
}

function text(
  template: string,
  values: Record<string, string>,
): string {
  return template.replace(/\{\{(\w+)\}\}/g, (match, key: string) =>
    values[key] ?? match,
  )
}

async function confirmDelete(
  user: ReturnType<typeof userEvent.setup>,
  name: string,
): Promise<void> {
  const dialog = await screen.findByRole('dialog')
  await user.type(
    within(dialog).getByLabelText(
      zhCN.tenantLifecycle.deleteConfirm.nameLabel,
    ),
    name,
  )
  await user.click(
    within(dialog).getByRole('button', {
      name: zhCN.tenantLifecycle.deleteConfirm.confirmLabel,
    }),
  )
  await act(async () => {
    await new Promise((resolve) =>
      setTimeout(resolve, CONFIRM_ARM_LOCKOUT_MS + 300),
    )
  })
  await user.click(
    within(dialog).getByRole('button', { name: CONFIRM_AGAIN_LABEL }),
  )
}

describe('TenantDetailPage status write focus in Chromium', () => {
  it('keeps focus on the status switch while the write is in flight', async () => {
    await page.viewport(1200, 900)
    const settled = deferred<void>()
    const operations = createAdminOperations(
      makeAdminBackend({
        getTenant: () =>
          Promise.resolve(tenantFixture({ name: 'acme', status: 'active' })),
        setTenantStatus: () => settled.promise,
      }),
    )
    renderWithProviders(<TenantDetailPage name="acme" />, {
      operations,
      language: 'zh-CN',
    })
    const user = userEvent.setup()
    await screen.findByText(zhCN.tenants.status.active)

    const toggle = screen.getByRole('switch', {
      name: zhCN.tenantDetail.actions.disable,
    })
    await user.click(toggle)

    expect(document.activeElement).toBe(toggle)
    expect(toggle).toHaveAttribute('aria-busy', 'true')
    expect(toggle).toHaveAttribute('aria-disabled', 'true')
    expect(toggle).toBeChecked()

    await act(async () => {
      settled.resolve()
    })
    await waitFor(() => expect(toggle).not.toHaveAttribute('aria-busy'))
    expect(document.activeElement).toBe(toggle)
  })
})

describe('TenantDetailPage lifecycle focus in Chromium', () => {
  it('lands focus on the summary heading when a deletion withdraws the lifecycle card', async () => {
    await page.viewport(1200, 900)
    attachSession(
      createStepUpSession({
        ...AUTHENTICATED_SNAPSHOT,
        permissionSets: { system: ['tenant.manage'], tenant: null },
      }),
    )
    let status: AdminTenant['status'] = 'active'
    const operations = createAdminOperations(
      makeAdminBackend({
        getTenant: () =>
          Promise.resolve(
            tenantFixture({ name: 'acme', display_name: 'Acme', status }),
          ),
        deleteTenant: () => {
          status = 'deleted'
          return Promise.resolve()
        },
      }),
    )
    renderWithProviders(<TenantDetailPage name="acme" />, {
      operations,
      language: 'zh-CN',
    })
    const user = userEvent.setup()
    await screen.findAllByText('Acme')

    await user.click(
      screen.getByRole('button', {
        name: text(zhCN.tenantLifecycle.deleteAria, { name: 'acme' }),
      }),
    )
    await confirmDelete(user, 'acme')

    expect(
      await screen.findByText(zhCN.tenants.status.deleted),
    ).toBeInTheDocument()
    const summary = screen.getByRole('heading', {
      name: zhCN.tenantDetail.summary,
    })
    await waitFor(() => expect(summary).toHaveFocus())
  })
})
