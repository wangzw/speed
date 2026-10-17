/**
 * TenantSwitcher behaviour: the trigger shows the host-supplied current
 * tenant and opens the host-supplied list; picking a row that is not the
 * current tenant closes the menu, calls onSelect exactly once with the
 * picked id, and announces the switched-to tenant in one role="status"
 * notice; the current-tenant row is disabled and can never re-trigger a
 * selection. The component is controlled: it never changes its own
 * display, so the trigger keeps showing the old tenant label until the
 * host hands back a new currentTenantId. A throwing onSelect is contained
 * -- it renders no error, never escapes as an unhandled rejection, and
 * the selection is still announced (a throwing host callback is not a
 * selection failure). With no current tenant the trigger is disabled and
 * shows the noCurrentTenant text, and nothing opens. Text expectations
 * read the bundle values, never inline language.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { switchLanguage } from '@speed/i18n'
import { createAppTheme } from '@speed/ui-kit'
import { TenantSwitcher } from './TenantSwitcher.js'
import type { TenantSwitcherProps } from './TenantSwitcher.js'
import { renderWithProviders } from '../test-utils/render.js'
import { expectNoAxeViolations } from '../test-utils/axe.js'
import zhCN from './locales/zh-CN.json' with { type: 'json' }
import enUS from './locales/en-US.json' with { type: 'json' }

const TENANTS = [
  { id: 'tenant-1', name: 'Sunshine Dental' },
  { id: 'tenant-2', name: 'Bright Smile Clinic' },
] as const

const NO_CURRENT_ZH = zhCN.tenantSwitcher.noCurrentTenant
const NO_CURRENT_EN = enUS.tenantSwitcher.noCurrentTenant

/** The zh switchedTo text with one tenant name interpolated. */
function SWITCHED_TO_ZH(tenant: string): string {
  return zhCN.tenantSwitcher.switchedTo.replace('{{tenant}}', tenant)
}

/** The en switchedTo text with one tenant name interpolated. */
function SWITCHED_TO_EN(tenant: string): string {
  return enUS.tenantSwitcher.switchedTo.replace('{{tenant}}', tenant)
}

/** Renders the switcher over the host-supplied list, current tenant-1. */
function renderSwitcher(props: Partial<TenantSwitcherProps> = {}) {
  const onSelect = vi.fn()
  const result = renderWithProviders(
    <TenantSwitcher
      tenants={TENANTS}
      currentTenantId="tenant-1"
      onSelect={onSelect}
      {...props}
    />,
  )
  return { ...result, onSelect }
}

/** Opens the list from the named trigger and returns the two rows. */
async function openMenu(
  user: ReturnType<typeof userEvent.setup>,
  triggerName = 'Sunshine Dental',
) {
  await user.click(screen.getByRole('button', { name: triggerName }))
  return {
    current: await screen.findByRole('menuitem', { name: 'Sunshine Dental' }),
    other: await screen.findByRole('menuitem', { name: 'Bright Smile Clinic' }),
  }
}

/** The WCAG 2.1 contrast ratio between two hex colours. */
function contrastRatio(a: string, b: string): number {
  const channel = (value: number): number => {
    const c = value / 255
    return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4)
  }
  const luminance = (hex: string): number => {
    const value = hex.replace('#', '')
    const r = Number.parseInt(value.slice(0, 2), 16)
    const g = Number.parseInt(value.slice(2, 4), 16)
    const b = Number.parseInt(value.slice(4, 6), 16)
    return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b)
  }
  const lighter = Math.max(luminance(a), luminance(b))
  const darker = Math.min(luminance(a), luminance(b))
  return (lighter + 0.05) / (darker + 0.05)
}

describe('TenantSwitcher', () => {
  it('default the trigger to an inheriting color, never the primary palette color', async () => {
    // A trigger defaulting to the primary palette color vanishes on the
    // very surface it usually sits on -- an AppBar whose background IS
    // the primary color. color="inherit" makes the text follow the
    // ambient color: the AppBar's own contrastText there, the
    // surrounding text color on a plain surface.
    renderSwitcher()
    const trigger = screen.getByRole('button', { name: 'Sunshine Dental' })
    expect(trigger).toHaveClass('MuiButton-colorInherit')
    expect(trigger).not.toHaveClass('MuiButton-colorPrimary')
    // The token relationship the inheritance relies on, guarded here
    // because jsdom resolves no cascaded styles: while an AppBar keeps
    // painting primary.main, its own contrastText must stay at WCAG AA
    // distance from it, or an inherited control is legible nowhere.
    const { theme } = createAppTheme()
    const ratio = contrastRatio(
      theme.palette.primary.main,
      theme.palette.primary.contrastText,
    )
    expect(ratio).toBeGreaterThanOrEqual(4.5)
    expect(theme.palette.primary.main).not.toBe(theme.palette.primary.contrastText)
  })

  it('show the current tenant and open the full list, current row disabled', async () => {
    renderSwitcher()
    const trigger = screen.getByRole('button', { name: 'Sunshine Dental' })
    expect(trigger).toBeEnabled()
    const user = userEvent.setup()
    const { current, other } = await openMenu(user)
    // MUI v9 renders a disabled MenuItem as aria-disabled="true" (no
    // native disabled attribute on the li), which is what assistive
    // tech reads; jest-dom's toBeDisabled only knows the native
    // attribute, so the a11y semantics are asserted directly.
    expect(current).toHaveAttribute('aria-disabled', 'true')
    expect(other).not.toHaveAttribute('aria-disabled')
    expect(screen.getByRole('menu')).toBeInTheDocument()
  })

  it('never re-trigger a selection from the current-tenant row', async () => {
    const { onSelect } = renderSwitcher()
    const user = userEvent.setup()
    const { current } = await openMenu(user)
    // fireEvent bypasses the pointer-events check user-event enforces on
    // disabled controls -- the disabled row is the inertness guard under
    // test, so the click must reach it synthetically.
    fireEvent.click(current)
    await waitFor(() => {
      // Nothing happened: no selection, menu still open, row still
      // disabled.
      expect(onSelect).not.toHaveBeenCalled()
      expect(
        screen.getByRole('menuitem', { name: 'Sunshine Dental' }),
      ).toHaveAttribute('aria-disabled', 'true')
    })
  })

  it('select on picking another tenant, calling onSelect once and announcing the switch', async () => {
    const { onSelect } = renderSwitcher()
    const user = userEvent.setup()
    const { other } = await openMenu(user)
    await user.click(other)
    // The one hand-off: onSelect fires exactly once with the picked id,
    // never the current tenant.
    expect(onSelect).toHaveBeenCalledTimes(1)
    expect(onSelect).toHaveBeenCalledWith('tenant-2')
    // The list closed and nothing rendered an error (nothing failed --
    // the component never reports a switch outcome, which is the host's).
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    await waitFor(() =>
      expect(screen.queryByRole('menu')).not.toBeInTheDocument(),
    )
    // The selection is announced out loud in one live region.
    const status = screen.getByRole('status')
    expect(status).toHaveTextContent(SWITCHED_TO_ZH('Bright Smile Clinic'))
    // Controlled: the component does not change its own display -- the
    // trigger keeps the host-supplied current label until the host hands
    // back a new currentTenantId.
    expect(
      screen.getByRole('button', { name: 'Sunshine Dental' }),
    ).toBeInTheDocument()
  })

  it('announce a tenant name with markup characters verbatim', async () => {
    const user = userEvent.setup()
    renderSwitcher({
      tenants: [
        { id: 'tenant-1', name: 'Sunshine Dental' },
        { id: 'tenant-2', name: 'Acme & Co <ops>' },
      ],
    })
    await user.click(screen.getByRole('button', { name: 'Sunshine Dental' }))
    await user.click(
      await screen.findByRole('menuitem', { name: 'Acme & Co <ops>' }),
    )
    // The name is user input, not markup: it reaches the live region as the
    // text the user typed, never as HTML entities.
    const status = screen.getByRole('status')
    expect(status).toHaveTextContent(SWITCHED_TO_ZH('Acme & Co <ops>'))
    expect(status.textContent).not.toContain('&amp;')
    expect(status.textContent).not.toContain('&lt;')
  })

  it('converge the trigger onto the new tenant once the host hands back the new id', async () => {
    const { onSelect, rerender } = renderSwitcher()
    const user = userEvent.setup()
    const { other } = await openMenu(user)
    await user.click(other)
    expect(onSelect).toHaveBeenCalledWith('tenant-2')
    // The host completes its route change and passes the new current
    // tenant back: the trigger converges onto it.
    rerender(
      <TenantSwitcher
        tenants={TENANTS}
        currentTenantId="tenant-2"
        onSelect={onSelect}
      />,
    )
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Bright Smile Clinic' }),
      ).toBeInTheDocument(),
    )
  })

  it('contain a throwing onSelect: no error renders, the selection is still announced', async () => {
    const onUnhandled = vi.fn<(reason: unknown) => void>()
    const listener = (event: PromiseRejectionEvent) => {
      onUnhandled(event.reason)
    }
    window.addEventListener('unhandledrejection', listener)
    const onSelect = vi.fn(() => {
      throw new Error('host route change failed')
    })
    try {
      renderSwitcher({ onSelect })
      const user = userEvent.setup()
      const { other } = await openMenu(user)
      await user.click(other)
      // The host callback threw, but a throwing onSelect is not a
      // selection failure: the component reports the pick once, renders
      // no error, and the throw was contained instead of becoming an
      // unhandled rejection of the fire-and-forget handler.
      expect(onSelect).toHaveBeenCalledTimes(1)
      expect(onSelect).toHaveBeenCalledWith('tenant-2')
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      expect(screen.getByRole('status')).toHaveTextContent(
        SWITCHED_TO_ZH('Bright Smile Clinic'),
      )
      // unhandledrejection dispatches after the microtask queue drains;
      // yield once, then assert none ever fired.
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 0))
      })
      expect(onUnhandled).not.toHaveBeenCalled()
    } finally {
      window.removeEventListener('unhandledrejection', listener)
    }
  })

  it('replace the announcement when a second selection lands', async () => {
    const { onSelect, rerender } = renderSwitcher()
    const user = userEvent.setup()
    const { other } = await openMenu(user)
    await user.click(other)
    expect(screen.getByRole('status')).toHaveTextContent(
      SWITCHED_TO_ZH('Bright Smile Clinic'),
    )
    // The host hands back tenant-2, so the trigger relabels and the user
    // switches back to tenant-1.
    rerender(
      <TenantSwitcher
        tenants={TENANTS}
        currentTenantId="tenant-2"
        onSelect={onSelect}
      />,
    )
    const rows = await openMenu(user, 'Bright Smile Clinic')
    await user.click(rows.current)
    expect(onSelect).toHaveBeenLastCalledWith('tenant-1')
    // One live region, its text replaced by the newer selection.
    expect(screen.getAllByRole('status')).toHaveLength(1)
    expect(screen.getByRole('status')).toHaveTextContent(
      SWITCHED_TO_ZH('Sunshine Dental'),
    )
  })

  it('fail closed with a disabled trigger and no list when there is no current tenant', async () => {
    const { onSelect } = renderSwitcher({ currentTenantId: null })
    const trigger = screen.getByRole('button', { name: NO_CURRENT_ZH })
    expect(trigger).toBeDisabled()
    expect(trigger).toHaveAttribute('aria-haspopup', 'menu')
    // fireEvent bypasses the pointer-events check user-event enforces on
    // disabled controls -- a disabled trigger is the inertness guard
    // under test, so the click must reach it synthetically.
    fireEvent.click(trigger)
    // Nothing opened, nothing selected.
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('render the noCurrentTenant text on an English-starting instance', () => {
    renderWithProviders(
      <TenantSwitcher
        tenants={TENANTS}
        currentTenantId={null}
        onSelect={vi.fn()}
      />,
      { language: 'en-US' },
    )
    expect(
      screen.getByRole('button', { name: NO_CURRENT_EN }),
    ).toBeDisabled()
  })

  it('render the en-US bundle on an English-starting instance', async () => {
    renderWithProviders(
      <TenantSwitcher
        tenants={TENANTS}
        currentTenantId="tenant-1"
        onSelect={vi.fn()}
      />,
      { language: 'en-US' },
    )
    const user = userEvent.setup()
    const { other } = await openMenu(user)
    await user.click(other)
    expect(screen.getByRole('status')).toHaveTextContent(
      SWITCHED_TO_EN('Bright Smile Clinic'),
    )
  })

  it('re-render the announcement in the switched language', async () => {
    const { i18n } = renderSwitcher()
    const user = userEvent.setup()
    const { other } = await openMenu(user)
    await user.click(other)
    expect(screen.getByRole('status')).toHaveTextContent(
      SWITCHED_TO_ZH('Bright Smile Clinic'),
    )
    await act(async () => {
      await switchLanguage(i18n, 'en-US')
    })
    expect(screen.getByRole('status')).toHaveTextContent(
      SWITCHED_TO_EN('Bright Smile Clinic'),
    )
  })

  it('pass axe with no violations over the open list', async () => {
    renderSwitcher()
    const user = userEvent.setup()
    await openMenu(user)
    await expectNoAxeViolations()
  })
})
