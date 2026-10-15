/**
 * ConsoleShell: the platform frame.
 *
 * It renders the three landmarks (a banner, a nav and a main), derives
 * the platform navigation from the labelled declarations only, links each
 * entry to its declaration's path, and moves focus to main in place from
 * the skip link. It does no path matching and marks no entry selected.
 */

import { describe, expect, it } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ConsoleShell } from './ConsoleShell.js'
import type { ConsoleRoute } from './route.js'
import { renderWithProviders } from '../test-utils/render.js'
import {
  createConsoleOperations,
  makeConsoleBackend,
} from '../test-utils/operations.js'
import { expectNoAxeViolationsForPage } from '../test-utils/axe.js'

const operations = createConsoleOperations(makeConsoleBackend())

const routes: readonly ConsoleRoute[] = [
  { path: '/tenants', view: () => null, label: 'Tenants', permission: 'tenant.read' },
  { path: '/settings', view: () => null, label: 'Settings' },
  // No label: reachable but not listed in the navigation.
  { path: '/audit/:id', view: () => null },
]

describe('ConsoleShell', () => {
  it('renders a banner, a nav and a main landmark', () => {
    renderWithProviders(
      <ConsoleShell routes={routes} title="Console">
        <h1>Home</h1>
      </ConsoleShell>,
      { operations },
    )
    expect(screen.getByRole('banner')).toBeInTheDocument()
    expect(screen.getByRole('navigation')).toBeInTheDocument()
    expect(screen.getByRole('main')).toBeInTheDocument()
  })

  it('derives the navigation from the labelled declarations only', () => {
    renderWithProviders(
      <ConsoleShell routes={routes} title="Console">
        <h1>Home</h1>
      </ConsoleShell>,
      { operations },
    )
    const nav = screen.getByRole('navigation')
    const links = within(nav).getAllByRole('link')
    expect(links.map((link) => link.textContent)).toEqual(['Tenants', 'Settings'])
    expect(links.map((link) => link.getAttribute('href'))).toEqual([
      '/tenants',
      '/settings',
    ])
  })

  it('renders the host content inside main', () => {
    renderWithProviders(
      <ConsoleShell routes={routes} title="Console">
        <h1>Home</h1>
        <p>body</p>
      </ConsoleShell>,
      { operations },
    )
    const main = screen.getByRole('main')
    expect(within(main).getByText('body')).toBeInTheDocument()
  })

  it('renders the title and the header actions in the banner', () => {
    renderWithProviders(
      <ConsoleShell
        routes={routes}
        title="Speed Console"
        headerActions={<span>account</span>}
      >
        <h1>Home</h1>
      </ConsoleShell>,
      { operations },
    )
    const banner = screen.getByRole('banner')
    expect(within(banner).getByText('Speed Console')).toBeInTheDocument()
    expect(within(banner).getByText('account')).toBeInTheDocument()
  })

  it('moves focus to main from the skip link, without navigating', async () => {
    const user = userEvent.setup()
    renderWithProviders(
      <ConsoleShell routes={routes} title="Console">
        <h1>Home</h1>
      </ConsoleShell>,
      { operations },
    )
    const skip = screen.getByRole('link', { name: /跳到内容|Skip to content/ })
    await user.click(skip)
    expect(screen.getByRole('main')).toHaveFocus()
  })

  it('has no axe violations as a page', async () => {
    renderWithProviders(
      <ConsoleShell routes={routes} title="Console">
        <h1>Home</h1>
        <p>body</p>
      </ConsoleShell>,
      { operations },
    )
    await expectNoAxeViolationsForPage()
  })
})
