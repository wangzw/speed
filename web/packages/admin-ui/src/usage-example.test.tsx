import { describe, expect, it } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import type { ReactElement } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createI18n, I18nextProvider, registerNamespace } from '@speed/i18n'
import { LAYOUT_KIT_NAMESPACE, layoutKitResources } from '@speed/layout-kit'
import {
  AppThemeProvider,
  UI_KIT_NAMESPACE,
  uiKitResources,
} from '@speed/ui-kit'
import {
  ADMIN_UI_NAMESPACE,
  AdminOperationsProvider,
  AdminShell,
  adminOperations,
  adminRoutes,
  adminUiResources,
} from './index.js'
import { bindAdminRequest } from '../test-utils/request-rig.js'
import adminUiZhCN from './locales/zh-CN.json' with { type: 'json' }

const i18n = createI18n({
  supportedLanguages: ['zh-CN', 'en-US'],
  defaultLanguage: 'zh-CN',
  storage: null,
  urlParameterName: null,
  navigatorLanguages: [],
})
registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)
registerNamespace(i18n, LAYOUT_KIT_NAMESPACE, layoutKitResources)
registerNamespace(i18n, ADMIN_UI_NAMESPACE, adminUiResources)

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
})

export function AdminApp(): ReactElement {
  const route = adminRoutes[0]
  return (
    <I18nextProvider i18n={i18n}>
      <AppThemeProvider i18n={i18n}>
        <QueryClientProvider client={queryClient}>
          <AdminOperationsProvider operations={adminOperations}>
            <AdminShell
              routes={adminRoutes}
              title="Speed Admin"
              hasFeature={() => true}
            >
              {route?.view({ path: route.path, params: {} })}
            </AdminShell>
          </AdminOperationsProvider>
        </QueryClientProvider>
      </AppThemeProvider>
    </I18nextProvider>
  )
}

describe('README quick start', () => {
  it('renders the console with its navigation and its first page', async () => {
    bindAdminRequest((call) => {
      if (call.path === '/api/v1/system/tenants') {
        return { tenants: [{ name: 'acme', display_name: 'Acme', kind: 'business', status: 'active' }] }
      }
      if (call.path === '/api/v1/system/audit/records') {
        return { entries: [], next: '' }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    render(<AdminApp />)

    const nav = screen.getByRole('navigation', {
      name: adminUiZhCN.shell.navLabel,
    })
    expect(
      within(nav)
        .getAllByRole('link')
        .map((link) => link.textContent),
    ).toEqual([
      adminUiZhCN.dashboard.title,
      adminUiZhCN.tenants.title,
      adminUiZhCN.accounts.title,
      adminUiZhCN.sessions.title,
      adminUiZhCN.impersonations.title,
      adminUiZhCN.permissions.title,
      adminUiZhCN.invitations.title,
      adminUiZhCN.sso.title,
      adminUiZhCN.settings.title,
      adminUiZhCN.sms.title,
      adminUiZhCN.audit.title,
    ])
    expect(
      await screen.findByText(adminUiZhCN.dashboard.kpi.tenants),
    ).toBeInTheDocument()
  })
})
