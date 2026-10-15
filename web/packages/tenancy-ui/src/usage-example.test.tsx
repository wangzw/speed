/**
 * usage-example.test.tsx -- compiles and runs the wiring the README's
 * quick start documents.
 *
 * Every step of the quick start executes here: the bilingual i18n
 * instance with both namespaces registered (the tenancy-ui namespace for
 * every string the switcher renders; the ui-kit namespace because a host
 * app composes under ui-kit's AppThemeProvider and any ui-kit chrome the
 * host renders reads that namespace), the AppThemeProvider tree, and the
 * host that holds the current tenant -- a React state stand-in for the
 * host's route, mounted on @speed/auth-core's TenantProvider so the
 * switcher's currentTenantId flows from useCurrentTenant. The switcher
 * itself sends no request: selecting a row calls the host's onSelect,
 * which evicts the departing tenant's cache (the host's own
 * query-cache discipline -- see the README) and switches the host's
 * tenant state, and the trigger converges onto the new tenant while the
 * switch is announced in one role="status" region.
 */

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

// The quick start's tenant list -- the host's data, rendered verbatim.
const TENANTS: readonly TenantOption[] = [
  { id: 'tenant-1', name: 'Sunshine Dental' },
  { id: 'tenant-2', name: 'Bright Smile Clinic' },
  { id: 'tenant-3', name: 'Harbor View Orthodontics' },
]

/** The host's app chrome in the quick start: the switcher with
 * currentTenantId flowing from useCurrentTenant, so a completed switch
 * re-renders the trigger. */
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

/** The host: it holds the current tenant (its route stand-in here),
 * mounts it on auth-core's TenantProvider, and treats onSelect as the
 * moment to evict the departing tenant's cache and switch the route. */
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
          // The host's own post-selection work: the departing tenant id
          // (read before the switch) drives the query-cache eviction --
          //   queryClient.removeQueries({ queryKey: ['tenant', tenantId] })
          // by auth-core's evictTenantQueries -- then the route changes.
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

    // The trigger shows the host's current tenant and opens the
    // host-supplied list; the current row is disabled, the others are
    // not.
    await user.click(screen.getByRole('button', { name: 'Sunshine Dental' }))
    expect(
      await screen.findByRole('menuitem', { name: 'Sunshine Dental' }),
    ).toHaveAttribute('aria-disabled', 'true')
    expect(
      screen.getByRole('menuitem', { name: 'Bright Smile Clinic' }),
    ).not.toHaveAttribute('aria-disabled')

    // Pick the second tenant: onSelect fires once, the host switches its
    // route, the trigger converges onto the new current tenant and the
    // switch is announced. Nothing renders an error.
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

    // Switch back: the host is told again, exactly once, with the first
    // tenant's id, and the trigger converges back.
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
