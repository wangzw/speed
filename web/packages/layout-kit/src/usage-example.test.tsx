import { act, fireEvent } from '@testing-library/react'
import { useState } from 'react'
import { describe, expect, it } from 'vitest'
import { switchLanguage } from '@speed/i18n'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }
import uiKitZhCN from '../../ui-kit/src/locales/zh-CN.json' with { type: 'json' }
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import { mockMatchMedia } from '@speed/test-utils/matchMedia'
import { AppShell } from './components/AppShell.js'
import { RouteGuard, type RouteGuardStatus } from './components/RouteGuard.js'

const NAV_ITEMS = [
  { id: 'home', label: 'Home', href: '/home', selected: true },
  { id: 'reports', label: 'Reports', href: '/reports' },
] as const

function AppContent({ initialStatus }: { readonly initialStatus: RouteGuardStatus }) {
  const [status] = useState<RouteGuardStatus>(initialStatus)
  return (
    <AppShell navItems={NAV_ITEMS} header="My App">
      {}
      <h1>Protected screen</h1>
      {}
      <RouteGuard status={status} headingLevel="h2">
        <p>Protected screen content</p>
      </RouteGuard>
    </AppShell>
  )
}

describe('README usage example', () => {
  it('renders the quick-start composition with the zh-CN built-in texts', async () => {
    mockMatchMedia(true)
    const utils = renderWithProviders(<AppContent initialStatus="allowed" />)
    expect(utils.getByText('My App')).toBeInTheDocument()
    expect(utils.getByRole('link', { name: 'Home' })).toHaveAttribute('href', '/home')
    expect(utils.getByRole('link', { name: 'Home' })).toHaveAttribute('aria-current', 'page')
    expect(utils.getByRole('link', { name: 'Reports' })).toHaveAttribute('href', '/reports')
    expect(utils.getByText('Protected screen content')).toBeInTheDocument()
    expect(
      utils.getByRole('navigation', { name: zhCN.appShell.navLabel }),
    ).toBeInTheDocument()
    expect(utils.getByRole('banner')).toBeInTheDocument()
    expect(utils.getByRole('main')).toBeInTheDocument()
    await expectNoAxeViolations()
  })

  it('follows a language switch into the en-US built-in texts', async () => {
    mockMatchMedia(true)
    const utils = renderWithProviders(<AppContent initialStatus="allowed" />)
    expect(
      utils.getByRole('navigation', { name: zhCN.appShell.navLabel }),
    ).toBeInTheDocument()
    await act(async () => {
      await switchLanguage(utils.i18n, 'en-US')
    })
    expect(
      utils.getByRole('navigation', { name: enUS.appShell.navLabel }),
    ).toBeInTheDocument()
    expect(
      utils.queryByRole('navigation', { name: zhCN.appShell.navLabel }),
    ).not.toBeInTheDocument()
  })

  it('gates the content region on RouteGuard status, reusing ui-kit noPermission for denied at the forwarded heading level', async () => {
    mockMatchMedia(true)
    const utils = renderWithProviders(<AppContent initialStatus="denied" />)
    expect(utils.queryByText('Protected screen content')).not.toBeInTheDocument()
    expect(
      utils.getByRole('heading', {
        level: 2,
        name: uiKitZhCN.emptyState.noPermission.title,
      }),
    ).toBeInTheDocument()
    expect(utils.getByRole('heading', { level: 1, name: 'Protected screen' })).toBeInTheDocument()
  })

  it('opens and closes the mobile nav drawer via the shipped toggle labels', async () => {
    mockMatchMedia(false)
    const utils = renderWithProviders(<AppContent initialStatus="allowed" />)
    const toggle = utils.getByRole('button', { name: zhCN.appShell.openNav })
    fireEvent.click(toggle)
    expect(
      utils.getByRole('button', { name: zhCN.appShell.closeNav, hidden: true }),
    ).toBeInTheDocument()
  })
})
