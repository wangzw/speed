/**
 * The tenant-switch affordance over a host-owned list. It reads no
 * session, sends no request and never navigates; it reports the pick once
 * through `onSelect`.
 */

import { useId, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import Button from '@mui/material/Button'
import type { ButtonProps } from '@mui/material/Button'
import Box from '@mui/material/Box'
import Divider from '@mui/material/Divider'
import Menu from '@mui/material/Menu'
import type { SxProps, Theme } from '@mui/material/styles'
import { useInertBackground } from '@speed/ui-kit'
import { useTenancyUiTranslation } from './internal/translation.js'
import { TenantMenuItems } from './TenantMenuItems.js'
import { TenantSwitchAnnouncement } from './TenantSwitchAnnouncement.js'

/** One switchable tenant: the id onSelect is called with, and the name
 * the trigger and the list show. */
export interface TenantOption {
  /** Stable tenant id, the value onSelect is called with. */
  readonly id: string
  /** Display name for the trigger and the list rows. */
  readonly name: string
}

/** The controlled switcher's props: the host's tenants and current id, the
 * selection callback, and presentation options for the trigger and menu. */
export interface TenantSwitcherProps {
  /** The tenants the host offers, host data in host order. The row whose
   * id equals currentTenantId is the current one and renders disabled. */
  readonly tenants: readonly TenantOption[]
  /** The id of the tenant the host's route currently resolves to, or
   * null when the host has none yet (no current tenant: the trigger is
   * disabled and shows the noCurrentTenant text). */
  readonly currentTenantId: string | null
  /** Fired once when the user picks a non-current row, with its id. A
   * throw is contained and never renders an error. */
  readonly onSelect: (tenantId: string) => void
  /** Host entries rendered after the tenant rows, under a divider in the
   * same menu. `close` closes the menu. */
  readonly menuFooter?: (close: () => void) => ReactNode
  /** Whether the switched-to confirmation is painted beside the trigger
   * (`inline`, the default) or stays visually hidden. */
  readonly announcement?: 'inline' | 'visually-hidden'
  /** Presentation for the trigger button: variant, size, sx and startIcon. */
  readonly triggerProps?: Pick<
    ButtonProps,
    'variant' | 'size' | 'sx' | 'startIcon'
  >
  /** Renders the trigger as an icon-only control for narrow chrome. The
   * accessible name stays the tenant name. */
  readonly compact?: boolean
}

function TenantGlyph() {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 24 24"
      width={18}
      height={18}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M4 20.5h16" />
      <path d="M6 20.5V6.5l6-2.5 6 2.5v14" />
      <path d="M9.5 9.5h1.2M13.3 9.5h1.2M9.5 13h1.2M13.3 13h1.2M10.5 20.5v-3.5h3v3.5" />
    </svg>
  )
}

/** Renders the controlled tenant switcher: the trigger and the tenant
 * menu. */
export function TenantSwitcher({
  tenants,
  currentTenantId,
  onSelect,
  menuFooter,
  announcement = 'inline',
  triggerProps,
  compact = false,
}: TenantSwitcherProps) {
  const { t } = useTenancyUiTranslation()
  const menuId = useId()
  const [anchorEl, setAnchorEl] = useState<HTMLElement | null>(null)
  const [selectedName, setSelectedName] = useState<string | null>(null)

  const menuOpen = anchorEl !== null
  const menuRef = useRef<HTMLDivElement>(null)
  useInertBackground(menuOpen, menuRef)
  const currentTenant =
    tenants.find((tenant) => tenant.id === currentTenantId) ?? null
  const closeMenu = (): void => setAnchorEl(null)
  const triggerLabel =
    currentTenant === null
      ? t('tenantSwitcher.noCurrentTenant')
      : currentTenant.name
  const { startIcon, ...buttonProps } = triggerProps ?? {}
  const triggerSx = (
    compact ? [{ minWidth: 0, px: 1 }, buttonProps.sx] : buttonProps.sx
  ) as SxProps<Theme> | undefined

  const select = (tenantId: string): void => {
    const picked = tenants.find((tenant) => tenant.id === tenantId) ?? null
    if (picked === null) {
      return
    }
    try {
      onSelect(picked.id)
    } catch {
      // Contained: the host's failure, not this control's to report.
    }
    setSelectedName(picked.name)
  }

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
        <Button
          type="button"
          variant="outlined"
          // Inherit the text color: a primary-on-primary default vanishes on a
          // host AppBar whose background is the primary color.
          color="inherit"
          disabled={currentTenant === null}
          aria-haspopup="menu"
          aria-expanded={menuOpen}
          aria-controls={menuOpen ? menuId : undefined}
          {...(compact ? { 'aria-label': triggerLabel } : {})}
          onClick={(event) => {
            setAnchorEl(event.currentTarget)
          }}
          {...(compact ? buttonProps : triggerProps)}
          sx={triggerSx}
        >
          {compact ? (startIcon ?? <TenantGlyph />) : triggerLabel}
        </Button>
        <TenantSwitchAnnouncement
          tenantName={selectedName}
          announcement={announcement}
        />
      </Box>
      <Menu
        ref={menuRef}
        id={menuId}
        anchorEl={anchorEl}
        open={menuOpen}
        onClose={closeMenu}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'left' }}
        transformOrigin={{ vertical: 'top', horizontal: 'left' }}
      >
        <TenantMenuItems
          tenants={tenants}
          currentTenantId={currentTenantId}
          onSelect={select}
          onDismiss={closeMenu}
        />
        {menuFooter === undefined ? null : (
          <>
            <Divider />
            {menuFooter(closeMenu)}
          </>
        )}
      </Menu>
    </Box>
  )
}
