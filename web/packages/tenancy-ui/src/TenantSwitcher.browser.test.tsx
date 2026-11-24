/**
 * TenantSwitcher in a real browser: the page behind the open menu leaves
 * the tab order, and closing the menu by a pick or by Escape hands focus
 * back to the trigger while the switch is still announced.
 */

import { describe, expect, it, vi } from 'vitest'
import { screen, waitFor } from '@testing-library/react'
import { userEvent } from 'vitest/browser'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import { renderWithProviders } from '../test-utils/render.js'
import { TenantSwitcher } from './TenantSwitcher.js'

const TENANTS = [
  { id: 'tenant-1', name: 'Sunshine Dental' },
  { id: 'tenant-2', name: 'Bright Smile Clinic' },
] as const

function SWITCHED_TO(name: string): string {
  return zhCN.tenantSwitcher.switchedTo.replace('{{tenant}}', name)
}

function renderSwitcher() {
  const onSelect = vi.fn()
  const { container } = renderWithProviders(
    <TenantSwitcher
      tenants={TENANTS}
      currentTenantId="tenant-1"
      onSelect={onSelect}
    />,
  )
  return { onSelect, container }
}

/** Opens the list by keyboard and returns the trigger captured before it
 * leaves the accessibility tree behind the inert background. */
async function openMenu(): Promise<HTMLElement> {
  const trigger = await screen.findByRole('button', { name: 'Sunshine Dental' })
  trigger.focus()
  await userEvent.keyboard('{Enter}')
  await screen.findByRole('menu')
  return trigger
}

describe('TenantSwitcher in Chromium', () => {
  it('marks the page behind the open menu inert and lifts it on close', async () => {
    const { onSelect } = renderSwitcher()
    const trigger = await openMenu()
    const menu = screen.getByRole('menu')

    await waitFor(() => expect(trigger.closest('[inert]')).not.toBeNull())
    expect(menu.closest('[inert]')).toBeNull()

    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(trigger.closest('[inert]')).toBeNull())
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('returns focus to the trigger after a pick and announces the switch', async () => {
    const { onSelect } = renderSwitcher()
    const trigger = await openMenu()

    await userEvent.click(
      await screen.findByRole('menuitem', { name: 'Bright Smile Clinic' }),
    )

    await waitFor(() => expect(document.activeElement).toBe(trigger))
    expect(onSelect).toHaveBeenCalledExactlyOnceWith('tenant-2')
    await waitFor(() =>
      expect(screen.getByRole('status')).toHaveTextContent(
        SWITCHED_TO('Bright Smile Clinic'),
      ),
    )
  })

  it('returns focus to the trigger after Escape closes the menu', async () => {
    renderSwitcher()
    const trigger = await openMenu()

    await userEvent.keyboard('{Escape}')

    await waitFor(() => expect(document.activeElement).toBe(trigger))
  })

  it('closes on Tab, returns focus to the trigger and lifts the background inert', async () => {
    const { container } = renderSwitcher()
    const trigger = await openMenu()
    await waitFor(() => expect(trigger.closest('[inert]')).not.toBeNull())

    await userEvent.keyboard('{Tab}')

    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    await waitFor(() => expect(document.activeElement).toBe(trigger))
    await waitFor(() => expect(trigger.closest('[inert]')).toBeNull())
    expect(container).not.toHaveAttribute('inert')
  })
})
