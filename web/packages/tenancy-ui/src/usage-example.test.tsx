import type { ReactElement } from 'react'
import { useState } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  I18nextProvider,
  createI18n,
  registerNamespace,
} from '@speed/i18n'
import {
  UI_KIT_NAMESPACE,
  uiKitResources,
  AppThemeProvider,
} from '@speed/ui-kit'
import { TenantProvider, useCurrentTenant } from '@speed/auth-core'
import { TENANCY_UI_NAMESPACE, tenancyUiResources } from './resources.js'
import { TenantSwitcher } from './TenantSwitcher.js'
import type { TenantOption } from './TenantSwitcher.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const TENANTS: readonly TenantOption[] = [
  { id: 'tenant-1', name: 'Sunshine Dental' },
  { id: 'tenant-2', name: 'Bright Smile Clinic' },
  { id: 'tenant-3', name: 'Harbor View Orthodontics' },
]

function Header({ onSelect }: { onSelect: (id: string) => void }): ReactElement {
  const current = useCurrentTenant()
  return (
    <header>
      <TenantSwitcher
        tenants={TENANTS}
        currentTenantId={current?.tenantId ?? null}
        onSelect={onSelect}
      />
    </header>
  )
}

function DemoHost({
  onSelect,
}: {
  onSelect: (id: string) => void
}): ReactElement {
  const [tenantId, setTenantId] = useState<string>('tenant-1')
  return (
    <TenantProvider tenantId={tenantId}>
      <Header
        onSelect={(id) => {
          onSelect(id)
          setTenantId(id)
        }}
      />
    </TenantProvider>
  )
}

describe('the README quick start, exercised end to end', () => {
  it('shows the current tenant, reports the pick once and converges the trigger', async () => {
    const onSelect = vi.fn()
    const i18n = createI18n({
      supportedLanguages: ['zh-CN', 'en-US'],
      defaultLanguage: 'zh-CN',
      storage: null,
      urlParameterName: null,
      navigatorLanguages: [],
    })
    registerNamespace(i18n, TENANCY_UI_NAMESPACE, tenancyUiResources)
    registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)
    render(
      <I18nextProvider i18n={i18n}>
        <AppThemeProvider i18n={i18n}>
          <DemoHost onSelect={onSelect} />
        </AppThemeProvider>
      </I18nextProvider>,
    )
    const user = userEvent.setup()

    await user.click(screen.getByRole('button', { name: 'Sunshine Dental' }))
    expect(
      await screen.findByRole('menuitem', { name: 'Sunshine Dental' }),
    ).toHaveAttribute('aria-disabled', 'true')
    expect(
      screen.getByRole('menuitem', { name: 'Bright Smile Clinic' }),
    ).not.toHaveAttribute('aria-disabled')

    await user.click(
      screen.getByRole('menuitem', { name: 'Bright Smile Clinic' }),
    )
    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(onSelect).toHaveBeenCalledWith('tenant-2')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent(
      zhCN.tenantSwitcher.switchedTo.replace('{{tenant}}', 'Bright Smile Clinic'),
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Bright Smile Clinic' }),
      ).toBeEnabled(),
    )

    await user.click(screen.getByRole('button', { name: 'Bright Smile Clinic' }))
    await user.click(
      await screen.findByRole('menuitem', { name: 'Sunshine Dental' }),
    )
    expect(onSelect).toHaveBeenCalledTimes(2)
    expect(onSelect).toHaveBeenLastCalledWith('tenant-1')
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Sunshine Dental' }),
      ).toBeEnabled(),
    )
    expect(screen.getByRole('status')).toHaveTextContent(
      zhCN.tenantSwitcher.switchedTo.replace('{{tenant}}', 'Sunshine Dental'),
    )
  })
})
