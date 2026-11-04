/**
 * The shell chrome the app's two areas share: the brand and the user menu the
 * tenant frame and the admin frame both render in their top bar.
 *
 * The tenant area (product-shell's frame) and the admin area (admin-ui's
 * frame) are two different shells -- the admin carries no tenant switcher --
 * but their top bars are the same chrome, so both render the brand and the
 * account controls from here. One definition keeps the two bars from drifting
 * into two products.
 *
 * The pieces:
 *
 *  - AppBrand: the demo namespace's product title.
 *  - TenantMenu: tenancy-ui's TenantSwitcher over the caller's own tenant
 *    list (the caller tenant read). product-shell places the switcher in the
 *    userMenu slot; the admin banner, which resolves no tenant, does not
 *    render it. Its switch confirmation is announced but never painted.
 *  - UserMenu: an avatar trigger opening the account menu -- the same entries
 *    the sidebar account entry carries, from one shared list: the
 *    notification centre, the four account panes, the workspace admin group
 *    (only while the caller's admission supplies it) and sign-out. While an
 *    impersonation is in force, auth-ui's end item sits first: closing the
 *    banner must not strand the operator without a way to end it.
 *
 * The module also owns the device side of the color-mode preference: the
 * storage helpers the account page and the tenant frame use to mirror the
 * account's choice, and the boot value src/main.tsx hands the theme provider.
 * The preference is the three-valued one ui-kit exposes -- light, dark or
 * system -- and an account with no saved value follows the device.
 */

import { useCallback, useId, useRef, useState, useSyncExternalStore } from 'react'
import type { ReactElement } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Divider from '@mui/material/Divider'
import IconButton from '@mui/material/IconButton'
import Menu from '@mui/material/Menu'
import MenuItem from '@mui/material/MenuItem'
import type { Theme } from '@mui/material/styles'
import Typography from '@mui/material/Typography'
import { evictTenantQueries, useAuthState } from '@speed/auth-core'
import { ImpersonationEndMenuItem } from '@speed/auth-ui'
import { useTranslation } from '@speed/i18n'
import { Avatar, avatarInitials } from '@speed/layout-kit'
import type { AppShellNavItem } from '@speed/layout-kit'
import { useNavigate } from '@speed/route-kit'
import { TenantSwitcher } from '@speed/tenancy-ui'
import type { TenantOption } from '@speed/tenancy-ui'
import { themePresets } from '@speed/theme-presets'
import type { AppThemeModePreference } from '@speed/ui-kit'
import { useAppServices } from './app-services.js'
import {
  NotificationsNavIcon,
  PreferencesNavIcon,
  ProfileNavIcon,
  SecurityNavIcon,
  SessionsNavIcon,
} from './nav-icons.js'
import {
  ACCOUNT_SECTION_PATHS,
  NOTIFICATIONS_PATH,
} from './paths.js'
import { DEMO_NAMESPACE } from './resources.js'
import { selfTenantAlias } from './tenant-self.js'
import type { SelfTenantKind } from './tenant-self.js'

/**
 * One tenant the switcher offers. Its two identifiers are distinct on
 * purpose, and telling them apart is the whole reason this type is not
 * tenancy-ui's TenantOption:
 *
 *  - `id` is the tenant's stable identity -- the value the switcher
 *    compares a pick against and reports back (TenantOption.id). It is
 *    what the chrome shows as "the current tenant".
 *  - `alias` is the name the request surface carries. The server resolves
 *    a request's tenant off its surface by this name (the resolve layer
 *    reads the X-Tenant header and looks it up in the roster's alias
 *    column), so the value the client announces (the tenantProvider, wired
 *    in src/main.tsx) is this one and never `id`. A rename moves the alias
 *    and leaves the id, so the two diverge over a tenant's life; announcing
 *    the id would put every request behind a name the roster no longer
 *    answers to, and the server would refuse it 404.
 *
 * `name` is the display label the switcher renders.
 *
 * It is tenancy-ui's TenantOption -- the switcher takes the list as that type,
 * so DemoTenant drops straight in -- plus the alias, which the switcher neither
 * reads nor shows.
 */
export interface DemoTenant extends TenantOption {
  /** The name the request surface carries (the roster alias). */
  readonly alias: string
  /** The tenant's kind from the caller tenant read. Absent on a row the host
   * adds from its own cache (the current tenant a platform staff member
   * entered, which that read did not list); such a row renders its alias. */
  readonly kind?: SelfTenantKind
}

/**
 * The host's current tenant, held as a request-time fact. Two values come
 * off it and they must not be conflated: the identity the switcher compares
 * against (currentTenantId) and the alias every tenant-scoped request
 * announces (getCurrentTenantAlias). The server resolves the tenant off the
 * request surface, so the call must carry the alias, not the id. It is the
 * one place the host holds the current tenant -- the tenant area's React
 * state (src/app.tsx) projects it for rendering, and a switch writes both,
 * so the value a request carries and the value the chrome shows agree.
 *
 * It starts empty: the app settles it from the caller tenant list (the first
 * membership that is not the caller's personal tenant, else the first row) in
 * src/app.tsx, and until then no alias is announced, which keeps a
 * tenant-scoped request from firing before the current tenant's alias is
 * known.
 */
let currentTenantId: string | null = null

// The listeners notified when the current tenant moves. This cell is the one
// place the host holds the tenant, and a component mounted above the tenant
// frame -- src/platform-permissions.tsx, which reads the caller's permission
// sets -- cannot take it up as a prop, so it subscribes here instead.
const currentTenantListeners = new Set<() => void>()

/** The tenant the switcher treats as current (its TenantOption.id). */
export function getCurrentTenantId(): string | null {
  return currentTenantId
}

/**
 * Records the host's current tenant; the switcher's pick (a tenant id) lands
 * here. Listeners are notified only when the value moves, so a write of the
 * value already held does not re-run the reads keyed on the tenant.
 */
export function setCurrentTenantId(tenantId: string | null): void {
  if (currentTenantId === tenantId) {
    return
  }
  currentTenantId = tenantId
  for (const listener of currentTenantListeners) {
    listener()
  }
}

/**
 * Subscribes to the current tenant's changes; returns the unsubscribe. It is
 * the store contract useSyncExternalStore takes, usable by any component that
 * needs the tenant as a React input.
 */
export function subscribeCurrentTenant(listener: () => void): () => void {
  currentTenantListeners.add(listener)
  return () => {
    currentTenantListeners.delete(listener)
  }
}

/**
 * The current tenant as React state, for a component that must re-run
 * something when the tenant settles or the viewer switches. The tenant
 * frame's own state projects this cell; reading the cell through
 * useSyncExternalStore is what lets a component mounted above the frame
 * follow it too.
 */
export function useCurrentTenantId(): string | null {
  return useSyncExternalStore(subscribeCurrentTenant, getCurrentTenantId)
}

/**
 * The alias -- the request-surface name -- of one tenant, in the given list;
 * null when the id is null or not on the list. Kept separate from the tenant
 * id so a rename (which moves the alias and not the id) cannot leak the id
 * into the field the server resolves against.
 */
export function tenantAlias(
  tenantId: string | null,
  tenants: readonly DemoTenant[],
): string | null {
  if (tenantId === null) {
    return null
  }
  return tenants.find((tenant) => tenant.id === tenantId)?.alias ?? null
}

/**
 * The names and kinds the host has recorded for known tenant ids: the aliases
 * the roster reported, the aliases learned from an operation's answer, and the
 * kind an operation's answer carried. The switcher's rows come from the caller
 * tenant list; this cache is the source for a tenant that list does not hold --
 * a platform staff member entering a tenant they are not a member of, and the
 * personal tenant the backfill just created, whose membership re-read may not
 * have landed yet. A tenant the cache does not hold and the caller list does
 * not list has no alias, and the request surface announces none rather than a
 * stale default.
 */
interface RecordedTenant {
  readonly alias: string
  readonly kind?: SelfTenantKind
}

let recordedTenants: ReadonlyMap<string, RecordedTenant> = new Map()

/** Records the roster's current aliases, keyed by tenant id. The roster read
 * carries no kind: a kind an operation's answer already recorded for one of
 * these ids is kept, and a fresh id gets none. */
export function setTenantAliases(aliases: ReadonlyMap<string, string>): void {
  recordedTenants = new Map(
    [...aliases].map(([id, alias]) => [
      id,
      { alias, kind: recordedTenants.get(id)?.kind },
    ]),
  )
}

/**
 * Records one tenant's current alias, leaving the rest as they were, and its
 * kind when the answer carries one. The personal tenant's alias is written
 * here before the frame announces it as the current tenant: no roster read
 * carries it, so without this record the request surface could not name the
 * tenant it is about to act in. The kind rides along so the switcher can label
 * that row before the membership re-read lists it.
 */
export function setTenantAlias(
  tenantId: string,
  alias: string,
  kind?: SelfTenantKind,
): void {
  const next = new Map(recordedTenants)
  next.set(tenantId, {
    alias,
    kind: kind ?? next.get(tenantId)?.kind,
  })
  recordedTenants = next
}

/**
 * One tenant's alias as the host recorded it, or null when the cache does not
 * hold that id. The tenant list the switcher renders adds the current tenant
 * from here when the caller tenant read does not list it -- a platform staff
 * member entering a tenant they are not a member of -- so the trigger can name
 * it and leave it.
 */
export function getRecordedTenantAlias(tenantId: string): string | null {
  return recordedTenants.get(tenantId)?.alias ?? null
}

/** One tenant's kind as an operation's answer recorded it, or null when none
 * did. The switcher labels a row the caller tenant read does not list by
 * this, rather than by deriving the personal identifier again. */
export function getRecordedTenantKind(tenantId: string): SelfTenantKind | null {
  return recordedTenants.get(tenantId)?.kind ?? null
}

/**
 * The value the client announces on every request as X-Tenant: the current
 * tenant's ALIAS, the name the request surface carries -- the roster's or the
 * host's recorded alias when there is one, the caller tenant list's
 * otherwise. Null when no source names the tenant. This is the tenantProvider
 * the assembly reads per attempt (src/main.tsx); it is not
 * getCurrentTenantId, whose value the switcher keys on.
 */
export function getCurrentTenantAlias(): string | null {
  if (currentTenantId === null) {
    return null
  }
  return (
    recordedTenants.get(currentTenantId)?.alias ??
    selfTenantAlias(currentTenantId) ??
    null
  )
}

/**
 * The current tenant as React state over the module cell above, with a
 * switch that clears the departing tenant's permission set and evicts its
 * cached rows.
 *
 * A switch is a host route change, not a session operation, and everything a
 * tenant-scoped read produced belongs to the tenant it was read in. Two facts
 * must therefore be withdrawn synchronously when the tenant moves. The
 * tenant-domain permission set is the first: it is the set held in the
 * tenant the request resolved to, so the setter resets it to unknown here,
 * before it announces the new tenant -- the re-read that follows attaches the
 * new tenant's set, and no frame in between can pair the new tenant with the
 * previous one's permissions. This is the one path every switch funnels
 * through; setCurrentTenantId alone carries no session and cannot do it.
 * The cached rows are the second: every tenant-scoped read is keyed under the
 * tenant it was read in, so the setter hands the departing id to auth-core's
 * evictTenantQueries before announcing the new one -- read *before* the
 * write, because the new tenant's id names nothing to remove. A
 * null-to-something transition (the first tenant the frame settles) is not a
 * switch: nothing was read for "no tenant", so it clears and evicts nothing.
 */
export function useTenantSelection(
  initialTenantId: string | null,
): readonly [string | null, (tenantId: string | null) => void] {
  const { queryClient, session } = useAppServices()
  const [tenantId, setTenantId] = useState<string | null>(initialTenantId)
  // The committed id, kept beside the state so the setter reads the tenant
  // actually left rather than a value captured when the callback was built.
  const committed = useRef<string | null>(initialTenantId)
  const select = useCallback(
    (nextTenantId: string | null): void => {
      const previous = committed.current
      if (previous !== null && previous !== nextTenantId) {
        // The tenant-domain set belongs to the tenant it was read in. Clear
        // it before the new tenant is announced: the read that follows the
        // switch attaches the new tenant's set, and nothing renders with the
        // previous tenant's permissions against the new tenant.
        session.setPermissionSet('tenant', null)
        evictTenantQueries(queryClient, previous)
      }
      committed.current = nextTenantId
      setCurrentTenantId(nextTenantId)
      setTenantId(nextTenantId)
    },
    [queryClient, session],
  )
  return [tenantId, select]
}

/**
 * The top bar's brand: the demo namespace's title, in the header's own
 * type. It carries the header's type but is NOT a heading element
 * (`component="div"`, never the h6 role the variant would pick): the
 * brand sits in the banner on every page, before the page's own h1, so a
 * heading element here would enter the page outline ahead of the real
 * title and invert the heading order. The header landmark already
 * contains it, so it needs no landmark role of its own.
 */
export function AppBrand(): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  return (
    <Typography
      noWrap
      variant="h6"
      component="div"
      sx={{ color: 'inherit', fontWeight: 600 }}
    >
      {t('app.title')}
    </Typography>
  )
}

/**
 * The tenant switcher over the caller's own tenant list. The current tenant is
 * a host fact (a route change, not a session op), and product-shell places the
 * switcher in the userMenu slot; the admin banner, which resolves no tenant,
 * does not render it.
 *
 * Every row the host offers is rendered. A tenant record carries no display
 * name, so a row renders its alias -- the request-surface name, never
 * translated -- except a row whose kind is `personal`, which renders the
 * host's bilingual 「工作区 / Workspace」 text. The kind comes from the caller
 * tenant read, so another account's personal tenant (which the list can hold)
 * renders its alias too: only the host's own answer marks a row personal.
 */
export function TenantMenu({
  tenants: offered,
  tenantId,
  onTenantSelect,
  compact = false,
}: {
  /** The tenant list the host offers: the caller tenant read's rows plus the
   * current tenant when the list does not hold it. Required -- the host has no
   * default roster to fall back on. */
  readonly tenants: readonly DemoTenant[]
  /** The id of the tenant the host currently resolves to, or null. */
  readonly tenantId: string | null
  /** Reports the tenant the user picked; the host switches its route/state. */
  readonly onTenantSelect: (tenantId: string) => void
  /** Icon-only trigger for the collapsed sidebar rail, where the tenant
   * name does not fit. The switch confirmation is announced but never
   * painted, in this form and the full one alike. */
  readonly compact?: boolean
}): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const tenants: readonly DemoTenant[] = offered.map((tenant) =>
    tenant.kind === 'personal'
      ? { ...tenant, name: t('workspace.personal') }
      : { ...tenant, name: tenant.alias },
  )
  return (
    <TenantSwitcher
      tenants={tenants}
      currentTenantId={tenantId}
      onSelect={onTenantSelect}
      compact={compact}
      announcement="visually-hidden"
    />
  )
}

/**
 * The roster read's non-blocking failure notice, beside the tenant switcher
 * trigger. The read names tenants outside the caller's own memberships (a
 * platform staff member entering a tenant they do not belong to); a failed
 * read leaves the current tenant and every membership fact untouched, so the
 * notice explains what is missing and offers the retry instead of deciding
 * anything. Only a caller whose platform permission set holds tenant.read
 * issues that read, so only staff ever see this notice.
 */
export function TenantRosterFailure({
  onRetry,
  compact = false,
}: {
  /** Asks the host to read the roster again. */
  readonly onRetry: () => void
  /** The collapsed sidebar rail's form: an icon-only retry, named by its
   * accessible label. */
  readonly compact?: boolean
}): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const message = t('tenantRoster.failed')
  const retryLabel = t('tenantRoster.retry')
  if (compact) {
    return (
      <IconButton
        color="inherit"
        aria-label={`${message} ${retryLabel}`}
        onClick={onRetry}
      >
        <RefreshIcon />
      </IconButton>
    )
  }
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
      <Typography variant="caption" color="error">
        {message}
      </Typography>
      <Button size="small" color="inherit" onClick={onRetry}>
        {retryLabel}
      </Button>
    </Box>
  )
}

/** A refresh glyph for the compact retry, decorative only. */
function RefreshIcon() {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 24 24"
      width={20}
      height={20}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M19 12a7 7 0 1 1-2.05-4.95" />
      <path d="M19 4.5V9h-4.5" />
    </svg>
  )
}

/** The device key the chosen colour-mode preference is remembered under. */
export const COLOR_MODE_STORAGE_KEY = 'demo-web.color-mode'

/** Whether a stored value names one of the three theme preferences. */
function isColorModePreference(
  value: unknown,
): value is AppThemeModePreference {
  return value === 'light' || value === 'dark' || value === 'system'
}

/** The colour-mode preference the device remembers, or null when none was
 * stored, the stored value names no preference, or the storage is
 * unavailable. */
export function readStoredColorModePreference(): AppThemeModePreference | null {
  try {
    const stored = window.localStorage.getItem(COLOR_MODE_STORAGE_KEY)
    return isColorModePreference(stored) ? stored : null
  } catch {
    // Storage can be refused (privacy modes, a sandboxed document). The
    // choice then lasts the page instead of failing the render.
    return null
  }
}

/** Records the user's colour-mode preference on the device. */
export function persistColorModePreference(
  preference: AppThemeModePreference,
): void {
  try {
    window.localStorage.setItem(COLOR_MODE_STORAGE_KEY, preference)
  } catch {
    // See readStoredColorModePreference: a refused storage is not a failed
    // switch.
  }
}

/**
 * The preference the app starts in: the device's stored choice, else
 * 'system'. Read at boot (src/main.tsx passes it to the assembly as the
 * theme's defaultMode), so a reload with a stored choice renders in it
 * without a flash of another preference; a device with nothing stored
 * follows the operating system through the theme provider.
 */
export function initialColorModePreference(): AppThemeModePreference {
  return readStoredColorModePreference() ?? 'system'
}

/** The device key the chosen theme preset is remembered under. */
export const THEME_PRESET_STORAGE_KEY = 'demo-web.theme-preset'

/** Whether a stored value names a preset the app ships. */
function isThemePresetId(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    themePresets.some((preset) => preset.id === value)
  )
}

/** The theme preset the device remembers, or null when none was stored (or the
 * stored value names no shipped preset, or the storage is unavailable). */
export function readStoredThemePreset(): string | null {
  try {
    const stored = window.localStorage.getItem(THEME_PRESET_STORAGE_KEY)
    return isThemePresetId(stored) ? stored : null
  } catch {
    // Storage can be refused (privacy modes, a sandboxed document). The
    // choice then lasts the page instead of failing the render.
    return null
  }
}

/** Records the user's theme-preset choice on the device. */
export function persistThemePreset(preset: string): void {
  try {
    window.localStorage.setItem(THEME_PRESET_STORAGE_KEY, preset)
  } catch {
    // See readStoredThemePreset: a refused storage is not a failed switch.
  }
}

/**
 * The preset the app starts on: the device's stored choice, else the
 * deployment's configured preset, else 'default'. Read at boot (src/main.tsx
 * passes it to the assembly as the theme's defaultPreset), so a reload with a
 * stored choice renders in it without a flash of the deployment default. The
 * deployment value is checked against the shipped shelf too: an unknown id is
 * a configuration typo, and the provider's documented fallback is the first
 * preset, not a blank theme.
 */
export function initialThemePreset(deploymentPreset?: string): string {
  const stored = readStoredThemePreset()
  if (stored !== null) {
    return stored
  }
  return isThemePresetId(deploymentPreset) ? deploymentPreset : 'default'
}

/**
 * The header's account menu: an avatar trigger opening the same entries the
 * sidebar account entry offers (AccountMenuItems), so the two surfaces stay
 * one menu. The trigger shows the signed-in user's initials (the avatar is
 * decorative) and its accessible name carries both the initials and the
 * display name -- the initials are visible text, so WCAG 2.5.3 requires them
 * inside the name; the generic label is used while anonymous. Sign-out
 * drives the session's logout, whose quiet terminal flip the shells observe.
 *
 * `workspaceItems` is the host's workspace admin group: the route-kit
 * entries the caller's tenant admits, supplied only while the admission is
 * `allowed`. Each account menu receives it from its host surface -- the app
 * passes the same list to this menu and to the sidebar's account entry -- so
 * both menus show the same group; absent leaves the group out of each.
 *
 * `closeDrawer` is the slot's temporary-drawer channel, and the host passes
 * AppShell's `rail.closeDrawer` in every layout. In the sidebar shell layout
 * below `md`, AppShell renders this menu inside the temporary drawer, so a
 * chosen destination must dismiss the drawer as well as the menu -- otherwise
 * it stays open over the page it just opened. Elsewhere the channel is a
 * no-op: the top-bar layout's temporary drawer cannot be open while the bar
 * is reachable, and the sidebar layout's permanent drawer supplies a no-op
 * close.
 */
/** The paper both account menus share: one width and surface, so the header
 * dropdown and the sidebar column read as the same menu. */
function accountMenuPaperSx(theme: Theme) {
  return {
    minWidth: theme.spacing(30),
    border: `1px solid ${theme.palette.divider}`,
    boxShadow: theme.shadows[12],
  }
}

export function UserMenu({
  narrow = false,
  closeDrawer,
  workspaceItems,
}: {
  /** Tightens the trigger for the collapsed sidebar rail, where the stock
   * padding would push it past the paper's edge. */
  readonly narrow?: boolean
  /** Closes the temporary (mobile) drawer this menu may be mounted in, before
   * navigating or signing out. The host passes the drawer slot's channel in
   * every layout; on a surface outside that drawer it is a no-op. */
  readonly closeDrawer?: () => void
  /** The workspace admin group's entries, supplied only while the caller's
   * admission allows it; absent leaves the group out. */
  readonly workspaceItems?: readonly AppShellNavItem[]
} = {}): ReactElement {
  const { session } = useAppServices()
  const navigate = useNavigate()
  const { t } = useTranslation(DEMO_NAMESPACE)
  const snapshot = useAuthState()
  const menuId = useId()
  const [anchorEl, setAnchorEl] = useState<HTMLElement | null>(null)
  const menuOpen = anchorEl !== null

  const displayName =
    snapshot.state === 'authenticated'
      ? snapshot.principal?.display_name
      : undefined
  // The glyph the avatar renders is visible text, so it belongs in the
  // trigger's accessible name (WCAG 2.5.3): a name of the display name alone
  // would omit the on-screen initials. Both come from the same derivation
  // the avatar renders, so the two cannot drift.
  const initials = avatarInitials(displayName)
  const triggerLabel =
    displayName === undefined || displayName.trim() === ''
      ? t('userMenu.accountMenu')
      : initials === ''
        ? displayName
        : `${initials} ${displayName}`

  const closeMenu = (): void => setAnchorEl(null)
  // The surface may sit inside AppShell's temporary drawer (the sidebar shell
  // layout below md), so dismissing it closes the drawer too. The host always
  // passes rail.closeDrawer: in the top-bar layout it is the navigation
  // drawer's controller (a no-op while the bar is reachable), and in the
  // sidebar layout's permanent drawer it is a no-op, so those placements get
  // the menu close alone.
  const dismissSurface = (): void => {
    closeMenu()
    closeDrawer?.()
  }
  const goTo = (path: string): void => {
    dismissSurface()
    navigate(path)
  }
  const signOut = (): void => {
    dismissSurface()
    // Logout is terminal either way (auth-core clears the local session and
    // the shell flips to its ended/sign-in branch), so a refused server
    // revocation has nothing left to retry against and needs no banner.
    void session.logout().catch(() => undefined)
  }

  return (
    <Box>
      <IconButton
        color="inherit"
        size={narrow ? 'small' : 'medium'}
        aria-label={triggerLabel}
        aria-haspopup="menu"
        aria-expanded={menuOpen}
        aria-controls={menuOpen ? menuId : undefined}
        // The trigger's accessible name is the principal's display name when
        // there is one and the generic account-menu label otherwise, so a
        // purpose-scoped identifier of its own lets a caller address it
        // regardless of which name it carries or where it sits among the
        // bar's other menus.
        data-testid="user-menu-trigger"
        onClick={(event) => setAnchorEl(event.currentTarget)}
      >
        <Avatar name={displayName} decorative size={narrow ? 28 : 32} />
      </IconButton>
      <Menu
        id={menuId}
        anchorEl={anchorEl}
        open={menuOpen}
        onClose={closeMenu}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
        transformOrigin={{ vertical: 'top', horizontal: 'right' }}
        slotProps={{ paper: { sx: accountMenuPaperSx } }}
      >
        {/* The same entries the sidebar's account menu carries, from one
            shared list. */}
        <AccountMenuItems
          onNavigate={goTo}
          onSignOut={signOut}
          onClose={closeMenu}
          onDismiss={dismissSurface}
          workspaceItems={workspaceItems}
        />
      </Menu>
    </Box>
  )
}

/** The menu entries the account panes contribute, in the order they render.
 * The preferences entry carries the menu's own copy rather than the account
 * page's section label. */
const ACCOUNT_MENU_PANES = [
  {
    section: 'profile',
    key: 'account.sections.profile',
    icon: <ProfileNavIcon size={24} />,
  },
  {
    section: 'security',
    key: 'account.sections.security',
    icon: <SecurityNavIcon size={24} />,
  },
  {
    section: 'sessions',
    key: 'account.sections.sessions',
    icon: <SessionsNavIcon size={24} />,
  },
  {
    section: 'preferences',
    key: 'userMenu.preferences',
    icon: <PreferencesNavIcon size={24} />,
  },
] as const

/**
 * One menu entry's glyph box: the navigation's own icon cell -- MUI's
 * ListItemIcon reserves theme.spacing(4.5) -- holding the navigation's 24px
 * mark centred. The cell scales with the theme's spacing base exactly as the
 * navigation's does, so the two tracks stay aligned in every preset. The
 * paper sits 9px in from the column with a 1px border, so the MenuItem's 6px
 * leading inset starts this box on the column's own 16px icon track; the
 * label then starts where a navigation label starts.
 */
const accountMenuItemIconSx = {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  flexShrink: 0,
  width: (theme: Theme) => theme.spacing(4.5),
  color: 'text.secondary',
} as const

/** The menu entry's leading inset: 16px of navigation track less the paper's
 * 9px margin and 1px border. */
const accountMenuItemSx = { pl: '6px' } as const

/** The sign-out glyph: a door with an outward arrow, decorative only. */
function SignOutIcon() {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 24 24"
      width={24}
      height={24}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M15 4h3a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1h-3" />
      <path d="M10 8l-4 4 4 4" />
      <path d="M6 12h9" />
    </svg>
  )
}

/**
 * The account menu's entries, rendered by both surfaces that offer them: the
 * header's avatar menu and the sidebar's account entry. One list keeps the
 * two menus from drifting into two different sets of actions -- the
 * notification centre, one entry per account pane, the workspace admin group
 * when the caller's admission supplies it, then sign-out, with the
 * impersonation-end item first while an impersonation is in force.
 *
 * The workspace group sits after the account panes and before the divider
 * that precedes sign-out. Its label is a plain group heading -- not a
 * menuitem and not focusable -- and each entry is a real link carrying
 * route-kit's href and click handler, so a modified or new-tab click keeps
 * the browser's behavior.
 *
 * The callbacks carry each surface's own dismissal: it closes its menu and,
 * when it is mounted in AppShell's temporary drawer, that drawer too, before
 * it navigates or signs out. `itemSx` is the only presentation difference the
 * list accepts -- the sidebar passes its navigation-track inset, the header
 * keeps the menu's default spacing -- so the entries themselves are identical.
 */
function AccountMenuItems({
  onNavigate,
  onSignOut,
  onClose,
  onDismiss,
  itemSx,
  workspaceItems,
}: {
  /** Opens a destination; the surface closes itself first. */
  readonly onNavigate: (path: string) => void
  /** Signs the session out; the surface closes itself first. */
  readonly onSignOut: () => void
  /** Closes the surface after the impersonation-end item activates. */
  readonly onClose: () => void
  /** Closes the surface after a workspace entry activates; when the surface
   * sits in a temporary drawer, it closes that too. */
  readonly onDismiss?: () => void
  /** The entry inset the sidebar's navigation track needs; absent keeps the
   * menu's default spacing. */
  readonly itemSx?: typeof accountMenuItemSx
  /** The workspace admin group's entries, in the admission's order. Absent or
   * empty leaves the group out. */
  readonly workspaceItems?: readonly AppShellNavItem[]
}): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const workspaceLabelId = useId()
  return (
    <>
      {/* The same end attempt the banner carries: an impersonation ends from
          wherever the account menu lives. */}
      <ImpersonationEndMenuItem divider onActivate={onClose} />
      <MenuItem onClick={() => onNavigate(NOTIFICATIONS_PATH)} sx={itemSx}>
        <Box component="span" sx={accountMenuItemIconSx}>
          <NotificationsNavIcon size={24} />
        </Box>
        {t('userMenu.messages')}
      </MenuItem>
      <Divider />
      {ACCOUNT_MENU_PANES.map(({ section, key, icon }) => (
        <MenuItem
          key={section}
          onClick={() => onNavigate(ACCOUNT_SECTION_PATHS[section])}
          sx={itemSx}
        >
          <Box component="span" sx={accountMenuItemIconSx}>
            {icon}
          </Box>
          {t(key)}
        </MenuItem>
      ))}
      {workspaceItems !== undefined && workspaceItems.length > 0 ? (
        <>
          <Divider />
          {/* The group's heading names the group for assistive technology and
              is not itself an action: a plain div inside role="group", never
              a menuitem, so the menu's focus cycle skips it. */}
          <Box role="group" aria-labelledby={workspaceLabelId}>
            <Typography
              id={workspaceLabelId}
              component="div"
              variant="caption"
              sx={{
                display: 'block',
                px: 2,
                py: 0.5,
                color: 'text.secondary',
              }}
            >
              {t('nav.workspaceAdmin')}
            </Typography>
            {workspaceItems.map((item) => (
              <MenuItem
                key={item.id}
                component="a"
                href={item.href}
                onClick={(event) => {
                  // route-kit's handler navigates a plain click in place and
                  // leaves a modified one to the browser; the surface then
                  // closes itself either way.
                  item.onClick?.(event)
                  onDismiss?.()
                }}
                sx={itemSx}
              >
                {item.icon === undefined ? null : (
                  <Box component="span" sx={accountMenuItemIconSx}>
                    {item.icon}
                  </Box>
                )}
                {item.label}
              </MenuItem>
            ))}
          </Box>
        </>
      ) : null}
      <Divider />
      <MenuItem onClick={onSignOut} sx={itemSx}>
        <Box component="span" sx={accountMenuItemIconSx}>
          <SignOutIcon />
        </Box>
        {t('userMenu.signOut')}
      </MenuItem>
    </>
  )
}

/**
 * The account entry at the bottom of the navigation column: a divider above a
 * row carrying the signed-in user's avatar, display name and disclosure
 * chevron. The menu opens upward over the navigation column with the
 * notification centre, the four account panes, the workspace admin group and
 * sign-out. `workspaceItems` is the same group the header menu receives, so
 * both surfaces offer the workspace entries when the caller's admission
 * allows them.
 *
 * The row shares the navigation's own icon track in absolute pixels -- a 16px
 * leading inset and a 36px icon box -- so the avatar starts on the navigation
 * icons' left edge and the label starts where a navigation label starts. The
 * divider sits 12px above the row, and the row is 12px above the column's
 * bottom edge. These are fixed pixel values, not theme spacing steps: a preset
 * with a different spacing base must not move the bar.
 *
 * The collapsed rail keeps the avatar alone; the accessible name still carries
 * the account, so the visible initials stay inside it (WCAG 2.5.3). Sign-out is
 * the session's own logout.
 */
export function AccountEntry({
  narrow = false,
  closeDrawer,
  workspaceItems,
}: {
  /** The collapsed rail's icon-only form. */
  readonly narrow?: boolean
  /** Closes a temporary (mobile) drawer before navigating. */
  readonly closeDrawer?: () => void
  /** The workspace admin group's entries, supplied only while the caller's
   * admission allows it; absent leaves the group out. */
  readonly workspaceItems?: readonly AppShellNavItem[]
}): ReactElement {
  const { session } = useAppServices()
  const navigate = useNavigate()
  const { t } = useTranslation(DEMO_NAMESPACE)
  const snapshot = useAuthState()
  const menuId = useId()
  const [anchorEl, setAnchorEl] = useState<HTMLElement | null>(null)
  const menuOpen = anchorEl !== null

  const displayName =
    snapshot.state === 'authenticated'
      ? snapshot.principal?.display_name
      : undefined
  const initials = avatarInitials(displayName)
  const triggerLabel =
    displayName === undefined || displayName.trim() === ''
      ? t('userMenu.accountMenu')
      : initials === ''
        ? displayName
        : `${initials} ${displayName}`

  const closeMenu = (): void => setAnchorEl(null)
  const dismissSurface = (): void => {
    closeMenu()
    closeDrawer?.()
  }
  const goTo = (path: string): void => {
    dismissSurface()
    navigate(path)
  }
  const signOut = (): void => {
    dismissSurface()
    // Logout is terminal either way (auth-core clears the local session and
    // the shell flips to its ended/sign-in branch), so a refused server
    // revocation has nothing left to retry against and needs no banner.
    void session.logout().catch(() => undefined)
  }

  return (
    <Box data-testid="account-entry" sx={{ width: '100%' }}>
      <Divider />
      <Box sx={{ pt: '12px', pb: '12px' }}>
        <Button
          fullWidth
          color="inherit"
          aria-label={triggerLabel}
          aria-haspopup="menu"
          aria-expanded={menuOpen}
          aria-controls={menuOpen ? menuId : undefined}
          data-testid="account-entry-trigger"
          onClick={(event) => setAnchorEl(event.currentTarget)}
          sx={{
            justifyContent: narrow ? 'center' : 'flex-start',
            px: narrow ? 0 : '16px',
            py: '4px',
            minWidth: 0,
            textTransform: 'none',
          }}
        >
          <Box
            component="span"
            sx={{
              display: 'inline-flex',
              alignItems: 'center',
              flexShrink: 0,
              // The navigation row's 36px ListItemIcon advance, in absolute
              // pixels so every preset keeps the same label track.
              width: narrow ? 'auto' : '36px',
            }}
          >
            <Avatar name={displayName} decorative size={narrow ? 28 : 32} />
          </Box>
          {/* The glyph's initials and the display name are two text nodes.
              Without a separator their text coalesces into one word ("TUJane
              Doe"), which is not a word of the trigger's accessible name and
              so fails WCAG 2.5.3's token containment. A whitespace-only text
              node is not a flex item, so neither child moves. */}
          {' '}
          {narrow ? null : (
            <>
              <Box
                component="span"
                sx={{
                  flex: '1 1 auto',
                  minWidth: 0,
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                  textAlign: 'left',
                  typography: 'body2',
                  pr: '8px',
                }}
              >
                {displayName ?? t('userMenu.accountMenu')}
              </Box>
              <Box
                component="svg"
                aria-hidden="true"
                focusable="false"
                viewBox="0 0 24 24"
                width={16}
                height={16}
                fill="currentColor"
                sx={{ flex: '0 0 auto' }}
              >
                <path d="M7.41 15.41 12 10.83l4.59 4.58L18 14l-6-6-6 6z" />
              </Box>
            </>
          )}
        </Button>
      </Box>
      <Menu
        id={menuId}
        anchorEl={anchorEl}
        open={menuOpen}
        onClose={closeMenu}
        anchorOrigin={{ vertical: 'top', horizontal: 'left' }}
        transformOrigin={{ vertical: 'bottom', horizontal: 'left' }}
        // The default threshold keeps the paper 16px off the viewport edge
        // before the paper's own 9px margin is applied, landing its border
        // box 25px in from the column. With no threshold the anchor (the
        // full-width trigger, flush with the column) positions the margin box
        // at the column's edge and the 9px margin is the only inset; the
        // right and vertical clamps still keep the paper inside the viewport.
        marginThreshold={0}
        slotProps={{
          paper: {
            // The paper keeps whatever fill the theme gives it -- glass and
            // the other presets stay translucent -- and takes only the
            // divider-coloured outline and the deeper shadow.
            sx: (theme) => ({
              ...accountMenuPaperSx(theme),
              ml: '9px',
            }),
          },
        }}
      >
        {/* The same entries the header's avatar menu carries, from one list:
            the impersonation-end item, the notification centre, the four
            account panes, the workspace admin group and sign-out. */}
        <AccountMenuItems
          onNavigate={goTo}
          onSignOut={signOut}
          onClose={closeMenu}
          onDismiss={dismissSurface}
          itemSx={accountMenuItemSx}
          workspaceItems={workspaceItems}
        />
      </Menu>
    </Box>
  )
}
