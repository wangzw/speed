/**
 * Avatar contract: initials derived from a name, an image when a src is
 * given (its alt being the name), the accessible-name behavior (a named
 * avatar reads as its name, a decorative one leaves the tree), and the
 * requested size.
 */

import { describe, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import { Avatar } from './Avatar.js'
import { renderWithProviders } from '../../test-utils/render.js'
// Avatar is a widget, not page chrome: use the widget-tier scan (region
// disabled), unlike AppShell's page-tier scan.
import { expectNoAxeViolations } from '@speed/test-utils/axe'

describe('Avatar', () => {
  it('renders derived initials (first and last word) as the accessible name', () => {
    renderWithProviders(<Avatar name="Ada Lovelace" />)
    const avatar = screen.getByRole('img', { name: 'Ada Lovelace' })
    expect(avatar).toHaveTextContent('AL')
  })

  it('renders a single-word name as one initial', () => {
    renderWithProviders(<Avatar name="Ada" />)
    expect(screen.getByRole('img', { name: 'Ada' })).toHaveTextContent('A')
  })

  it('renders an image whose alt is the name', () => {
    renderWithProviders(<Avatar name="Ada" src="/ada.png" />)
    expect(screen.getByRole('img', { name: 'Ada' })).toHaveAttribute(
      'src',
      '/ada.png',
    )
  })

  it('leaves the accessibility tree when decorative', () => {
    renderWithProviders(<Avatar name="Ada" src="/ada.png" decorative />)
    expect(screen.queryByRole('img')).not.toBeInTheDocument()
  })

  it('applies the requested size', () => {
    const { container } = renderWithProviders(<Avatar name="Ada" size={56} />)
    expect(container.firstElementChild).toHaveStyle({
      width: '56px',
      height: '56px',
    })
  })

  it('has no axe violations', async () => {
    // page-has-heading-one is part of the widget tier, so the scan
    // document supplies the page heading the chrome does not render.
    renderWithProviders(
      <>
        <h1>Account</h1>
        <Avatar name="Ada Lovelace" />
      </>,
    )
    await expectNoAxeViolations()
  })
})
