/**
 * TenantMenuItems: the tenant rows on their own, for a host that renders them
 * inside a menu it already owns -- the demo app mounts them in the account
 * menu's organization group.
 *
 * The row contract is TenantSwitcher's: the current tenant's row is disabled,
 * carries `aria-current` and a check mark, and can never re-trigger a
 * selection even when a synthetic or assistive click reaches it; picking any
 * other row reports its id once through onSelect. A throwing onSelect is
 * contained -- it is not a selection failure, so the rows render no error and
 * nothing escapes the fire-and-forget row handler as an unhandled rejection.
 *
 * The rows paint no group label and mount no announcement: the host names the
 * group around them and owns the switch confirmation's live region
 * (TenantSwitchAnnouncement), which must outlive the menu these rows close.
 * Nothing here consumes a session, reads storage, navigates or touches the
 * network -- the host decides what a selection means for its own route and
 * tenant context.
 */

import type { ReactElement, ReactNode } from 'react'
import Box from '@mui/material/Box'
import MenuItem from '@mui/material/MenuItem'
import type { SxProps, Theme } from '@mui/material/styles'
import type { TenantOption } from './TenantSwitcher.js'

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
  /** Presentation for each row, spread onto its MenuItem, so the host can
   * align the rows with the menu's other entries. */
  readonly itemSx?: SxProps<Theme>
  /** The mark rendered ahead of each row's name -- the host's organization
   * tile -- in the menu's icon cell, so the names share the icon track the
   * menu's other entries use. Absent leaves the rows text-only. */
  readonly leading?: (tenant: TenantOption) => ReactNode
}

/** The current row keeps the menu's primary text colour: it is the row the
 * menu states as current, not one that became unavailable, and the theme's
 * disabled fade would read as the latter. The check and `aria-current` carry
 * the state; MUI's `aria-disabled` still keeps it from being picked. */
const currentRowSx = {
  '&.Mui-disabled': { opacity: 1, color: 'text.primary' },
} as const

/** The current row's check, decorative: the row's `aria-current` and its
 * disabled state carry the meaning. */
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
              // The current row is disabled above; this guard keeps the
              // never-re-trigger invariant even when a synthetic or assistive
              // click reaches the row.
              if (tenant.id === currentTenantId) {
                return
              }
              onDismiss?.()
              try {
                onSelect(tenant.id)
              } catch {
                // Contained: the host's own failure to handle, not this
                // control's to render or report.
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
