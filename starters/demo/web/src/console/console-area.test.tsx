/**
 * ConsoleArea: the platform frame around the routed console page.
 *
 * It renders @speed/console-ui's shell with the prefixed console declarations,
 * so the frame's navigation carries one translated entry per declared page and
 * the host's title sits in the banner. The child stands in for the page the
 * router would render; the console pages' own data reads are the package's, not
 * this area's.
 */

import { render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { createI18n, I18nextProvider, registerNamespace } from '@speed/i18n'
import {
  CONSOLE_UI_NAMESPACE,
  consoleUiResources,
} from '@speed/console-ui'
import { AppThemeProvider, UI_KIT_NAMESPACE, uiKitResources } from '@speed/ui-kit'
import consoleUiEnUS from '../../../../../web/packages/console-ui/src/locales/en-US.json' with { type: 'json' }
import demoEnUS from '../locales/en-US.json' with { type: 'json' }
import { DEMO_NAMESPACE, demoResources } from '../resources.js'
import { consoleShellRoutes } from '../routes.js'
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

  return render(
    <I18nextProvider i18n={i18n}>
      <AppThemeProvider i18n={i18n}>
        <ConsoleArea routes={consoleShellRoutes}>
          <p>console page</p>
        </ConsoleArea>
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
