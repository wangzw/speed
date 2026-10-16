/**
 * Compiles and executes the README's Quick start composition.
 *
 * The composition below mirrors README.md's: the bilingual i18n instance
 * with the ui-kit, layout-kit and console-ui namespaces, the query
 * client, the console operations bundle the package ships
 * (`consoleOperations`), and the provider stack around ConsoleShell with
 * the console's declarations. The only test-side addition is the
 * scripted request function bound into the api-sdk runtime seam (see
 * test-utils/request-rig), because the shipped bundle drives the
 * generated surface and a test has no backend. Everything else is the
 * documented composition verbatim, so the README cannot drift from the
 * API.
 */

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
  CONSOLE_UI_NAMESPACE,
  ConsoleOperationsProvider,
  ConsoleShell,
  consoleOperations,
  consoleRoutes,
  consoleUiResources,
} from './index.js'
import { bindConsoleRequest } from '../test-utils/request-rig.js'
import consoleUiZhCN from './locales/zh-CN.json' with { type: 'json' }

const i18n = createI18n({
  supportedLanguages: ['zh-CN', 'en-US'],
  defaultLanguage: 'zh-CN',
  storage: null,
  urlParameterName: null,
  navigatorLanguages: [],
})
registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)
registerNamespace(i18n, LAYOUT_KIT_NAMESPACE, layoutKitResources)
registerNamespace(i18n, CONSOLE_UI_NAMESPACE, consoleUiResources)

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
})

export function ConsoleApp(): ReactElement {
  // The host's route layer renders the matched declaration's view; the
  // console's navigation is derived from the labelled declarations.
  const route = consoleRoutes[0]
  return (
    <I18nextProvider i18n={i18n}>
      <AppThemeProvider i18n={i18n}>
        <QueryClientProvider client={queryClient}>
          <ConsoleOperationsProvider operations={consoleOperations}>
            <ConsoleShell routes={consoleRoutes} title="Speed Console">
              {route?.view({ path: route.path, params: {} })}
            </ConsoleShell>
          </ConsoleOperationsProvider>
        </QueryClientProvider>
      </AppThemeProvider>
    </I18nextProvider>
  )
}

describe('README quick start', () => {
  it('renders the console with its navigation and its first page', async () => {
    bindConsoleRequest((call) => {
      if (call.path === '/tenant/admin/tenants') {
        return { tenants: [{ id: 'acme', alias: 'Acme', status: 'active' }] }
      }
      throw new Error(`unexpected request: ${call.path}`)
    })
    render(<ConsoleApp />)

    // The shell's own navigation (the console's `nav` landmark inside the
    // layout-kit AppShell frame) is named; the roster page's PageHeader
    // adds a second nav landmark (the breadcrumb trail), so target the
    // shell's by its accessible name.
    const nav = screen.getByRole('navigation', {
      name: consoleUiZhCN.shell.navLabel,
    })
    expect(
      within(nav)
        .getAllByRole('link')
        .map((link) => link.textContent),
    ).toEqual([
      '租户',
      '账户',
      '会话',
      '冒充',
      '权限',
      '邀请与成员',
      'SSO 提供方',
      '设置',
      '短信模板',
      '审计记录',
    ])
    expect(await screen.findByText('Acme')).toBeInTheDocument()
  })
})
