/**
 * ConsoleArea: the platform frame around the routed console page.
 *
 * It renders @speed/console-ui's shell with the prefixed console declarations,
 * so the frame's navigation carries one translated entry per declared page and
 * the host's title sits in the banner. The child stands in for the page the
 * router would render; the console pages' own data reads are the package's, not
 * this area's.
 */

import { QueryClient } from '@tanstack/react-query'
import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { RequestFn } from '@speed/api-client'
import { createI18n, I18nextProvider, registerNamespace } from '@speed/i18n'
import {
  CONSOLE_UI_NAMESPACE,
  consoleUiResources,
} from '@speed/console-ui'
import type { SpeedAppServices } from '@speed/product-shell/bootstrap'
import { AppThemeProvider, UI_KIT_NAMESPACE, uiKitResources } from '@speed/ui-kit'
import consoleUiEnUS from '../../../../../web/packages/console-ui/src/locales/en-US.json' with { type: 'json' }
import { AppServicesProvider } from '../app-services.js'
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { DEMO_NAMESPACE, demoResources } from '../resources.js'
import { consoleShellRoutes } from '../routes.js'
import { createTestSession } from '../test-utils/session.js'
import { ConsoleArea } from './console-area.js'

function renderConsoleArea() {
  const i18n = createI18n({
    supportedLanguages: ['zh-CN', 'en-US'],
    defaultLanguage: 'en-US',
    storage: null,
    urlParameterName: null,
    navigatorLanguages: [],
  })
  registerNamespace(i18n, UI_KIT_NAMESPACE, uiKitResources)
  registerNamespace(i18n, CONSOLE_UI_NAMESPACE, consoleUiResources)
  registerNamespace(i18n, DEMO_NAMESPACE, demoResources)

  // The frame's top bar carries the sign-out action (src/chrome's UserMenu),
  // which reads the assembled session -- so the area needs the services
  // provider even though the routed child here is a stand-in.
  const services: SpeedAppServices = {
    session: createTestSession(),
    api: (async () => {
      throw new Error('the render harness client must not be called')
    }) as RequestFn,
    queryClient: new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    }),
  }

  return render(
    <I18nextProvider i18n={i18n}>
      <AppThemeProvider i18n={i18n}>
        <AppServicesProvider services={services}>
          <ConsoleArea routes={consoleShellRoutes}>
            <p>console page</p>
          </ConsoleArea>
        </AppServicesProvider>
      </AppThemeProvider>
    </I18nextProvider>,
  )
}

describe('ConsoleArea', () => {
  it('renders the console frame with its title and the routed page', () => {
    renderConsoleArea()
    expect(screen.getByText(demoEnUS.app.title)).toBeInTheDocument()
    expect(screen.getByText('console page')).toBeInTheDocument()
  })

  it('derives the platform navigation from the labelled console declarations', () => {
    renderConsoleArea()
    const nav = screen.getByRole('navigation')
    expect(
      within(nav)
        .getAllByRole('link')
        .map((link) => link.textContent),
    ).toEqual([
      consoleUiEnUS.tenants.title,
      consoleUiEnUS.settings.title,
      consoleUiEnUS.audit.title,
    ])
  })
})
