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
 * shows the noCurrentTenant text, and nothing opens. The optional props
 * extend this without changing it: menuFooter renders host entries after
 * the rows under a divider and receives a close callback, announcement
 * keeps the status region mounted and can leave it clipped, and
 * triggerProps spreads presentation onto the trigger. Text expectations
 * read the bundle values, never inline language.
 */

import { describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import MenuItem from '@mui/material/MenuItem'
import { buttonClasses } from '@mui/material/Button'
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

/** Whether the element is clipped to the 1x1 visually-hidden box. jsdom
 * renders no layout, but it resolves the emotion class rules into
 * computed styles, so the clip recipe is observable. */
function isVisuallyHidden(element: Element): boolean {
  const style = getComputedStyle(element)
  return (
    style.position === 'absolute' &&
    ((style.clip !== '' && style.clip !== 'auto') ||
      (style.clipPath !== '' && style.clipPath !== 'none'))
  )
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
    expect(trigger).toHaveClass(buttonClasses.colorInherit)
    expect(trigger).not.toHaveClass(buttonClasses.colorPrimary)
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

  it('mount the announcement region empty and clipped before the first selection, then paint it inline', async () => {
    const user = userEvent.setup()
    renderSwitcher()
    // A live region born together with its text is not reliably
    // announced: the region is in the tree from the first render, empty
    // and clipped, and the first selection fills a region the screen
    // reader already knows.
    const before = screen.getByRole('status')
    expect(before).toBeEmptyDOMElement()
    expect(isVisuallyHidden(before)).toBe(true)
    const { other } = await openMenu(user)
    await user.click(other)
    const after = screen.getByRole('status')
    expect(after).toHaveTextContent(SWITCHED_TO_ZH('Bright Smile Clinic'))
    // Inline is the default: the confirmation is painted beside the
    // trigger once a selection fills it.
    expect(isVisuallyHidden(after)).toBe(false)
  })

  it('keep the status region visually hidden while announcing when announcement is visually-hidden', async () => {
    const user = userEvent.setup()
    renderSwitcher({ announcement: 'visually-hidden' })
    expect(isVisuallyHidden(screen.getByRole('status'))).toBe(true)
    const { other } = await openMenu(user)
    await user.click(other)
    const status = screen.getByRole('status')
    expect(status).toHaveTextContent(SWITCHED_TO_ZH('Bright Smile Clinic'))
    // The region stays in the accessibility tree -- clipped, not
    // display:none, which would make it not live -- and never paints.
    expect(isVisuallyHidden(status)).toBe(true)
    expect(getComputedStyle(status).display).not.toBe('none')
  })

  it('render the menu footer after the tenant rows under a divider and close the menu from its callback', async () => {
    const user = userEvent.setup()
    const onSelect = vi.fn()
    const menuFooter = vi.fn((close: () => void) => (
      <MenuItem onClick={close}>Create tenant</MenuItem>
    ))
    renderWithProviders(
      <TenantSwitcher
        tenants={TENANTS}
        currentTenantId="tenant-1"
        onSelect={onSelect}
        menuFooter={menuFooter}
      />,
    )
    await user.click(screen.getByRole('button', { name: 'Sunshine Dental' }))
    const lastRow = await screen.findByRole('menuitem', {
      name: 'Bright Smile Clinic',
    })
    const divider = screen.getByRole('separator')
    const footerItem = screen.getByRole('menuitem', { name: 'Create tenant' })
    // The footer sits after every tenant row, separated from them.
    expect(
      lastRow.compareDocumentPosition(divider) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    expect(
      divider.compareDocumentPosition(footerItem) &
        Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy()
    // The supplied close callback dismisses the list; the footer is host
    // text and reports nothing through onSelect.
    await user.click(footerItem)
    await waitFor(() =>
      expect(screen.queryByRole('menu')).not.toBeInTheDocument(),
    )
    expect(onSelect).not.toHaveBeenCalled()
  })

  it('spread the trigger presentation props onto the button', () => {
    renderSwitcher({
      triggerProps: {
        variant: 'contained',
        size: 'small',
        sx: { textTransform: 'none' },
        startIcon: <span data-testid="tenant-icon" />,
      },
    })
    const trigger = screen.getByRole('button', { name: 'Sunshine Dental' })
    expect(trigger).toHaveClass(buttonClasses.contained)
    expect(trigger).toHaveClass(buttonClasses.sizeSmall)
    expect(trigger).toContainElement(screen.getByTestId('tenant-icon'))
    expect(trigger).toHaveStyle({ textTransform: 'none' })
  })

  it('renders an icon-only trigger under compact, named by the tenant it stands for', async () => {
    renderSwitcher({ compact: true })
    const trigger = screen.getByRole('button', { name: 'Sunshine Dental' })
    // No visible label: the name lives on the control, so a narrow host
    // chrome still exposes the same control to assistive technology.
    expect(trigger.textContent?.trim()).toBe('')
    expect(trigger.querySelector('svg')).not.toBeNull()
    expect(trigger).toHaveAttribute('aria-haspopup', 'menu')
  })

  it('opens the same list from the compact trigger and reports the pick once', async () => {
    const user = userEvent.setup()
    const { onSelect } = renderSwitcher({ compact: true })
    const rows = await openMenu(user)
    await user.click(rows.other)
    expect(onSelect).toHaveBeenCalledExactlyOnceWith('tenant-2')
  })

  it('draws the host startIcon as the compact glyph when one is given', () => {
    renderSwitcher({
      compact: true,
      triggerProps: { startIcon: <span data-testid="tenant-icon" /> },
    })
    const trigger = screen.getByRole('button', { name: 'Sunshine Dental' })
    expect(trigger).toContainElement(screen.getByTestId('tenant-icon'))
  })

  it('pass axe with no violations over the open list', async () => {
    renderSwitcher()
    const user = userEvent.setup()
    await openMenu(user)
    await expectNoAxeViolations()
  })

  it('pass axe with the host menu footer open', async () => {
    renderSwitcher({
      menuFooter: (close) => (
        <MenuItem onClick={close}>Create tenant</MenuItem>
      ),
    })
    const user = userEvent.setup()
    await openMenu(user)
    await expectNoAxeViolations()
  })
})
