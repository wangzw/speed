/**
 * TenantSwitcher: the tenant-switch affordance over a host-owned list.
 *
 * The tenant is a request's routing fact -- the server resolves the
 * current tenant per request from the host, a subdomain or a path, and
 * authentication carries no tenant -- so switching tenants is a HOST
 * ROUTE CHANGE, not an operation on a session. This control never knows
 * a session, never sends a request and never navigates: it reports the
 * user's pick once through onSelect and lets the host change its route,
 * update the tenant context it hands to @speed/auth-core's
 * TenantProvider, and evict the departing tenant's query cache.
 *
 * A trigger button shows the current tenant: the host-supplied name of
 * the row whose id equals currentTenantId, or the noCurrentTenant text
 * while the host has none (data lag on first render, or a mount with no
 * tenant in the route). With no current tenant the trigger is disabled
 * -- there is nothing to switch from. The trigger renders with
 * `color="inherit"`, so its text follows the ambient text color instead
 * of defaulting to the primary palette color: mounted inside a colored
 * header (the common host placement, next to the user menu in an AppBar
 * whose background is the primary color), an inherited color resolves to
 * the surface's own contrastText and stays legible where a
 * primary-on-primary default would vanish on the very surface the
 * control usually sits on. On a plain surface the same inheritance
 * resolves to the surrounding text color, equally safe.
 *
 * Clicking the trigger opens the host-supplied tenant list; the
 * current-tenant row is disabled -- never switchable, never
 * re-triggering -- and the guard in the row handler keeps that
 * invariant even if a synthetic or assistive click reaches the disabled
 * row. Picking another row closes the menu and calls onSelect(id)
 * exactly once, with the picked tenant's id. After the hand-off the
 * control announces the switched-to tenant in one role="status" live
 * region (the switchedTo text) -- a context change that silently alters
 * which rows the host shows must say so out loud, and the announcement
 * belongs in a live region because a person using a screen reader cannot
 * glance at the chrome to check that the switch did what the notice
 * says. The confirmation stays on the page until the next selection
 * replaces it -- deliberately no auto-dismiss timer, so the
 * announcement's lifetime never races a reader's processing of it.
 * The announcement region is mounted from the start, empty and clipped,
 * so a live region that only receives its text later is present before
 * the text arrives: a region born together with its content is not
 * reliably announced. The `announcement` option chooses whether the
 * confirmation is also painted (`inline`, the default) or stays clipped
 * for its whole lifetime (`visually-hidden`, for a host whose chrome has
 * no room for it); the region is in the accessibility tree either way.
 *
 * A host may render its own entries after the tenant list through
 * `menuFooter`: the function receives a `close` callback that closes the
 * menu, so an entry that navigates elsewhere can dismiss the list first.
 * The footer renders under a divider, in the same menu, after the tenant
 * rows. `triggerProps` carries presentation only -- variant, size, sx
 * and startIcon -- onto the trigger button, so a host can match the
 * control to its own chrome (a contained button in a plain toolbar, a
 * small one in a dense header) without this component growing a styling
 * API of its own.
 *
 * The component is controlled and fails closed: the tenant list and the
 * current tenant id are host data, and nothing here consumes the
 * auth-core hooks, reads storage, navigates or touches the network. A
 * throwing onSelect is not a selection failure (the onSelect contract):
 * the control contains it, so it renders no error and never escapes the
 * fire-and-forget row handler as an unhandled rejection, and the
 * selection is still announced.
 *
 * The host renders this where the current tenant belongs (typically app
 * chrome next to the user menu) with the tenant list its membership
 * source provides, and treats onSelect as the moment to switch its own
 * route: it updates the tenant id it passes back as currentTenantId (so
 * the trigger converges onto the new tenant), updates the tenant context
 * it mounts on @speed/auth-core's TenantProvider, and evicts the
 * departing tenant's query cache (auth-core's evictTenantQueries, called
 * with the tenant id read before the switch). Until the host hands back
 * the new currentTenantId the trigger keeps showing the old tenant --
 * the control never changes its own display.
 */

import { useId, useState } from 'react'
import type { ReactNode } from 'react'
import Button from '@mui/material/Button'
import type { ButtonProps } from '@mui/material/Button'
import Box from '@mui/material/Box'
import Divider from '@mui/material/Divider'
import Menu from '@mui/material/Menu'
import type { SxProps, Theme } from '@mui/material/styles'
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

export interface TenantSwitcherProps {
  /** The tenants the host offers, host data in host order. The row whose
   * id equals currentTenantId is the current one and renders disabled. */
  readonly tenants: readonly TenantOption[]
  /** The id of the tenant the host's route currently resolves to, or
   * null when the host has none yet (no current tenant: the trigger is
   * disabled and shows the noCurrentTenant text). */
  readonly currentTenantId: string | null
  /** Fired exactly once when the user picks a row that is not the current
   * tenant, with the picked tenant's id. The host switches its route,
   * updates its tenant context and evicts the departing tenant's query
   * cache there; throwing from this callback is not a selection failure
   * and never renders an error, and the component contains it, so it
   * cannot escape as an unhandled rejection either. */
  readonly onSelect: (tenantId: string) => void
  /** Host entries rendered after the tenant list, under a divider, in the
   * same menu. The callback receives a `close` function that closes the
   * menu; an entry that navigates elsewhere calls it first. Omitted, the
   * menu contains only the tenant rows. */
  readonly menuFooter?: (close: () => void) => ReactNode
  /** Whether the switched-to confirmation is painted beside the trigger
   * (`inline`, the default) or stays visually hidden (`visually-hidden`).
   * Both mount one `role="status"` region from the start; the hidden form
   * keeps it in the accessibility tree and announcing while painting
   * nothing, for a host whose chrome has no room for the confirmation. */
  readonly announcement?: 'inline' | 'visually-hidden'
  /** Presentation for the trigger button: variant, size, sx and startIcon
   * are spread onto it, so the control can match the host's own chrome.
   * Omitted, the trigger is the default outlined button inheriting the
   * ambient text color. */
  readonly triggerProps?: Pick<
    ButtonProps,
    'variant' | 'size' | 'sx' | 'startIcon'
  >
  /** Renders the trigger as an icon-only control for chrome too narrow for
   * the tenant name, such as a collapsed navigation rail. The accessible
   * name stays the name the text trigger would show, so a screen reader
   * hears the same control either way; `triggerProps.startIcon` becomes
   * the icon when the host gives one, and the package's own glyph fills
   * in otherwise. The list, the disabled current row and the selection
   * announcement are unchanged. */
  readonly compact?: boolean
}

/**
 * The compact trigger's glyph: a building, drawn decorative -- the button's
 * accessible name is the tenant name, so the icon adds no text.
 */
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
  // The display name of the last tenant the user selected, or null
  // before any selection. It backs the single role="status"
  // confirmation and stays until the next selection replaces it.
  const [selectedName, setSelectedName] = useState<string | null>(null)

  const menuOpen = anchorEl !== null
  const currentTenant =
    tenants.find((tenant) => tenant.id === currentTenantId) ?? null
  const closeMenu = (): void => setAnchorEl(null)
  // The name the trigger stands for, whether it renders as text or as an
  // icon-only control's accessible name.
  const triggerLabel =
    currentTenant === null
      ? t('tenantSwitcher.noCurrentTenant')
      : currentTenant.name
  const { startIcon, ...buttonProps } = triggerProps ?? {}
  // MUI's 64px button floor and 16px side padding are sized for a label; the
  // icon form drops both, or it would still be wider than the rail it
  // renders in. The host's own sx still applies, after ours.
  const triggerSx = (
    compact ? [{ minWidth: 0, px: 1 }, buttonProps.sx] : buttonProps.sx
  ) as SxProps<Theme> | undefined

  const select = (tenantId: string): void => {
    // The selection is a synchronous hand-off to the host: the rows close
    // the list, report the pick once, and the pick is announced here. A
    // throwing onSelect is contained here -- it is not a selection failure
    // (the onSelect contract), so it renders no error, never becomes an
    // unhandled rejection from this fire-and-forget handler, and the
    // selection is still announced.
    const picked = tenants.find((tenant) => tenant.id === tenantId) ?? null
    if (picked === null) {
      return
    }
    try {
      onSelect(picked.id)
    } catch {
      // Contained: the host's own failure to handle, not this control's
      // to render or report.
    }
    setSelectedName(picked.name)
  }

  return (
    <Box>
      <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
        <Button
          type="button"
          variant="outlined"
          // Inherit the ambient text color instead of defaulting to the
          // primary palette color: hosts mount this control inside a
          // colored AppBar (whose own text is its contrastText), where a
          // primary-colored default reads primary-on-primary. On a plain
          // surface the inherited color is the surrounding text color,
          // equally legible -- the file header has the full rationale.
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
