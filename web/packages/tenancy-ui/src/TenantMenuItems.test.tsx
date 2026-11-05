/**
 * TenantMenuItems behaviour: the rows render every tenant, the current one is
 * disabled, marked `aria-current` and never re-triggers a selection; picking
 * another row dismisses the host's menu first and reports the id once. A
 * throwing onSelect is contained -- the rows render no error and nothing
 * escapes as an unhandled rejection. TenantSwitchAnnouncement mounts one
 * `role="status"` region from the first render and fills it with the
 * switched-to tenant; it stays clipped in its default form.
 */

import type { ComponentProps } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import MenuList from '@mui/material/MenuList'
import { TenantMenuItems } from './TenantMenuItems.js'
import { TenantSwitchAnnouncement } from './TenantSwitchAnnouncement.js'
import { renderWithProviders } from '../test-utils/render.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }

const TENANTS = [
  { id: 'tenant-1', name: 'Sunshine Dental' },
  { id: 'tenant-2', name: 'Bright Smile Clinic' },
] as const

function renderRows(
  props: Partial<ComponentProps<typeof TenantMenuItems>> = {},
) {
  const onSelect = vi.fn()
  const onDismiss = vi.fn()
  renderWithProviders(
    // The rows are always mounted inside a menu in production; MenuList is
    // the minimal container that gives their MenuItems their menu context.
    <MenuList>
      <TenantMenuItems
        tenants={TENANTS}
        currentTenantId="tenant-1"
        onSelect={onSelect}
        onDismiss={onDismiss}
        {...props}
      />
    </MenuList>,
  )
  return { onSelect, onDismiss }
}

describe('TenantMenuItems', () => {
  it('render every tenant, with the current one marked and unselectable', () => {
    renderRows()
    const current = screen.getByRole('menuitem', { name: 'Sunshine Dental' })
    const other = screen.getByRole('menuitem', { name: 'Bright Smile Clinic' })
    expect(current).toHaveAttribute('aria-disabled', 'true')
    expect(current).toHaveAttribute('aria-current', 'true')
    expect(other).not.toHaveAttribute('aria-disabled')
    expect(other).not.toHaveAttribute('aria-current')
  })

  it('dismiss the host menu before reporting the picked id', async () => {
    const calls: string[] = []
    const onSelect = vi.fn(() => {
      calls.push('select')
    })
    const onDismiss = vi.fn(() => {
      calls.push('dismiss')
    })
    renderRows({ onSelect, onDismiss })
    await userEvent.setup().click(
      screen.getByRole('menuitem', { name: 'Bright Smile Clinic' }),
    )
    expect(onSelect).toHaveBeenCalledExactlyOnceWith('tenant-2')
    expect(calls).toEqual(['dismiss', 'select'])
  })

  it('never report a pick from the current-tenant row', () => {
    const { onSelect, onDismiss } = renderRows()
    // A disabled row takes no pointer input; a synthetic or assistive click
    // still has to be refused by the row's own guard.
    fireEvent.click(screen.getByRole('menuitem', { name: 'Sunshine Dental' }))
    expect(onSelect).not.toHaveBeenCalled()
    expect(onDismiss).not.toHaveBeenCalled()
  })

  it('contain a throwing onSelect instead of escaping it', async () => {
    const onSelect = vi.fn(() => {
      throw new Error('host failure')
    })
    renderRows({ onSelect })
    await expect(
      userEvent
        .setup()
        .click(screen.getByRole('menuitem', { name: 'Bright Smile Clinic' })),
    ).resolves.toBeUndefined()
    expect(onSelect).toHaveBeenCalledOnce()
  })
})

describe('TenantSwitchAnnouncement', () => {
  it('mount one empty live region before any selection', () => {
    renderWithProviders(<TenantSwitchAnnouncement tenantName={null} />)
    expect(screen.getByRole('status')).toHaveTextContent('')
  })

  it('announce the switched-to tenant, clipped by default', () => {
    renderWithProviders(
      <TenantSwitchAnnouncement tenantName="Bright Smile Clinic" />,
    )
    const region = screen.getByRole('status')
    expect(region).toHaveTextContent(
      zhCN.tenantSwitcher.switchedTo.replace(
        '{{tenant}}',
        'Bright Smile Clinic',
      ),
    )
    expect(getComputedStyle(region).position).toBe('absolute')
  })

  it('paint the confirmation when asked inline', () => {
    renderWithProviders(
      <TenantSwitchAnnouncement
        tenantName="Bright Smile Clinic"
        announcement="inline"
      />,
    )
    expect(getComputedStyle(screen.getByRole('status')).position).not.toBe(
      'absolute',
    )
  })
})
