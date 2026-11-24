/**
 * The tenant rows on their own, for a host that renders them inside a menu
 * it already owns. The rows mount no announcement and paint no group
 * label.
 */

import type { ReactElement, ReactNode } from 'react'
import Box from '@mui/material/Box'
import MenuItem from '@mui/material/MenuItem'
import type { SxProps, Theme } from '@mui/material/styles'
import type { TenantOption } from './TenantSwitcher.js'

/** The tenant rows' props for a host-owned menu: the host's tenants and
 * current id, the selection and dismiss callbacks, and row presentation. */
export interface TenantMenuItemsProps {
  /** The tenants the host offers, in host order. */
  readonly tenants: readonly TenantOption[]
  /** The id of the tenant the host currently resolves to; that row renders
   * disabled and marked current. */
  readonly currentTenantId: string | null
  /** Fired once when the user picks a row that is not the current tenant. */
  readonly onSelect: (tenantId: string) => void
  /** Runs before onSelect: the host closes the menu it embedded these rows
   * in. */
  readonly onDismiss?: () => void
  /** Presentation for each row, spread onto its MenuItem. */
  readonly itemSx?: SxProps<Theme>
  /** A decorative mark rendered ahead of each row's name, in the menu's
   * icon cell. */
  readonly leading?: (tenant: TenantOption) => ReactNode
}

/** The current row keeps the menu's primary text colour; the check and
 * `aria-current` carry the state. */
const currentRowSx = {
  '&.Mui-disabled': { opacity: 1, color: 'text.primary' },
} as const

function CurrentGlyph() {
  return (
    <Box
      component="svg"
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 24 24"
      width={18}
      height={18}
      fill="none"
      stroke="currentColor"
      strokeWidth={2}
      strokeLinecap="round"
      strokeLinejoin="round"
      sx={{ flex: '0 0 auto', ml: 1 }}
    >
      <path d="m5 12.5 4.5 4.5L19 7" />
    </Box>
  )
}

/** Renders the tenant rows for a host-owned menu. */
export function TenantMenuItems({
  tenants,
  currentTenantId,
  onSelect,
  onDismiss,
  itemSx,
  leading,
}: TenantMenuItemsProps): ReactElement {
  return (
    <>
      {tenants.map((tenant) => {
        const current = tenant.id === currentTenantId
        return (
          <MenuItem
            key={tenant.id}
            disabled={current}
            aria-current={current ? 'true' : undefined}
            sx={[itemSx ?? {}, currentRowSx] as SxProps<Theme>}
            onClick={() => {
              if (tenant.id === currentTenantId) {
                return
              }
              onDismiss?.()
              try {
                onSelect(tenant.id)
              } catch {
                // Contained: the host's failure, not this control's to report.
              }
            }}
          >
            {leading === undefined ? null : leading(tenant)}
            <Box
              component="span"
              sx={{
                flex: '1 1 auto',
                minWidth: 0,
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
            >
              {tenant.name}
            </Box>
            {current ? <CurrentGlyph /> : null}
          </MenuItem>
        )
      })}
    </>
  )
}
