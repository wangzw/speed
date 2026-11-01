/**
 * The shell chrome the app's two areas share: the brand and the user menu the
 * tenant frame and the console frame both render in their top bar.
 *
 * The tenant area (product-shell's frame) and the console area (console-ui's
 * frame) are two different shells -- the console carries no tenant switcher --
 * but their top bars are the same chrome, so both render the brand and the
 * account controls from here. One definition keeps the two bars from drifting
 * into two products.
 *
 * The pieces:
 *
 *  - AppBrand: the demo namespace's product title.
 *  - TenantMenu: tenancy-ui's TenantSwitcher over the caller's own tenant
 *    list (the caller tenant read). product-shell places the switcher in the
 *    userMenu slot; the console banner, which resolves no tenant, does not
 *    render it. Its switch confirmation is announced but never painted.
 *  - UserMenu: an avatar trigger opening a menu of profile / account-settings
 *    / sign-out, replacing the bare sign-out button. While an impersonation
 *    is in force, auth-ui's end item sits first: closing the banner must not
 *    strand the operator without a way to end it.
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
import IconButton from '@mui/material/IconButton'
import Menu from '@mui/material/Menu'
import MenuItem from '@mui/material/MenuItem'
import Typography from '@mui/material/Typography'
import { evictTenantQueries, useAuthState } from '@speed/auth-core'
import { ImpersonationEndMenuItem } from '@speed/auth-ui'
import { useTranslation } from '@speed/i18n'
import { Avatar, avatarInitials } from '@speed/layout-kit'
import { useNavigate } from '@speed/route-kit'
import { TenantSwitcher } from '@speed/tenancy-ui'
import type { TenantOption } from '@speed/tenancy-ui'
import { themePresets } from '@speed/theme-presets'
import type { AppThemeModePreference } from '@speed/ui-kit'
import { useAppServices } from './app-services.js'
import { ACCOUNT_PATH } from './paths.js'
import { DEMO_NAMESPACE } from './resources.js'
import { selfTenantAlias } from './tenant-self.js'

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
 * The aliases the roster reported for known tenant ids, and the ones the host
 * wrote itself. The switcher's rows come from the caller tenant list; this
 * cache is the name source for a tenant that list does not hold -- a platform
 * staff member entering a tenant they are not a member of -- and the place
 * the host records an alias it learned from an operation's answer (the
 * personal tenant's, for one). A tenant the cache does not hold and the
 * caller list does not list has no alias, and the request surface announces
 * none rather than a stale default.
 */
let rosterAliases: ReadonlyMap<string, string> = new Map()

/** Records the roster's current aliases, keyed by tenant id. */
export function setTenantAliases(aliases: ReadonlyMap<string, string>): void {
  rosterAliases = aliases
}

/**
 * Records one tenant's current alias, leaving the rest as they were. The
 * personal tenant's alias is written here before the frame announces it as
 * the current tenant: no roster read carries it, so without this record the
 * request surface could not name the tenant it is about to act in.
 */
export function setTenantAlias(tenantId: string, alias: string): void {
  const next = new Map(rosterAliases)
  next.set(tenantId, alias)
  rosterAliases = next
}

/**
 * One tenant's alias as the roster read recorded it, or null when the cache
 * does not hold that id. The tenant list the switcher renders adds the current
 * tenant from here when the caller tenant read does not list it -- a platform
 * staff member entering a tenant they are not a member of -- so the trigger
 * can name it and leave it.
 */
export function getRecordedTenantAlias(tenantId: string): string | null {
  return rosterAliases.get(tenantId) ?? null
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
    rosterAliases.get(currentTenantId) ??
    selfTenantAlias(currentTenantId) ??
    null
  )
}

/**
 * The current tenant as React state over the module cell above, with a
 * switch that evicts the departing tenant's cached rows.
 *
 * A switch is a host route change, not a session operation, and every cached
 * row a tenant-scoped read produced is keyed under the tenant it was read
 * in. The setter therefore hands the departing id to auth-core's
 * evictTenantQueries before announcing the new one -- read *before* the
 * write, because the new tenant's id names nothing to remove. A
 * null-to-something transition (the first tenant the frame settles) evicts
 * nothing; there is no departing tenant to clean up after.
 */
export function useTenantSelection(
  initialTenantId: string | null,
): readonly [string | null, (tenantId: string | null) => void] {
  const { queryClient } = useAppServices()
  const [tenantId, setTenantId] = useState<string | null>(initialTenantId)
  // The committed id, kept beside the state so the setter reads the tenant
  // actually left rather than a value captured when the callback was built.
  const committed = useRef<string | null>(initialTenantId)
  const select = useCallback(
    (nextTenantId: string | null): void => {
      const previous = committed.current
      if (previous !== null && previous !== nextTenantId) {
        evictTenantQueries(queryClient, previous)
      }
      committed.current = nextTenantId
      setCurrentTenantId(nextTenantId)
      setTenantId(nextTenantId)
    },
    [queryClient],
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
 * switcher in the userMenu slot; the console banner, which resolves no tenant,
 * does not render it.
 *
 * Every row the host offers is rendered. A tenant record carries no display
 * name, so a row renders its alias -- the request-surface name, never
 * translated -- except the account's own personal tenant, which renders the
 * host's bilingual 「工作区 / Workspace」 text. The personal row is recognised
 * by the derived identifier compared EXACTLY: another account's personal
 * tenant shares the prefix and must render its alias instead.
 */
export function TenantMenu({
  tenants: offered,
  tenantId,
  personalTenantId,
  onTenantSelect,
  compact = false,
}: {
  /** The tenant list the host offers: the caller tenant read's rows plus the
   * current tenant when the list does not hold it. Required -- the host has no
   * default roster to fall back on. */
  readonly tenants: readonly DemoTenant[]
  /** The id of the tenant the host currently resolves to, or null. */
  readonly tenantId: string | null
  /** The identifier the host derives for the caller's personal tenant
   * (personalTenantId(accountId)), or null while no account is known. Only the
   * row whose id equals it exactly carries the personal-workspace copy. */
  readonly personalTenantId: string | null
  /** Reports the tenant the user picked; the host switches its route/state. */
  readonly onTenantSelect: (tenantId: string) => void
  /** Icon-only trigger for the collapsed sidebar rail, where the tenant
   * name does not fit. The switch confirmation is announced but never
   * painted, in this form and the full one alike. */
  readonly compact?: boolean
}): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const tenants: readonly DemoTenant[] = offered.map((tenant) =>
    personalTenantId !== null && tenant.id === personalTenantId
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
 * The account menu: an avatar trigger opening account-settings / sign-out.
 * The trigger shows the signed-in user's initials (the avatar is
 * decorative) and its accessible name carries both the initials and the
 * display name -- the initials are visible text, so WCAG 2.5.3 requires them
 * inside the name; the generic label is used while anonymous. The account
 * page holds the settings surface (account-ui's sections span it), and the
 * avatar menu is the tenant navigation's single entry for it; sign-out
 * drives the session's logout, whose quiet terminal flip the shells observe.
 */
export function UserMenu({
  narrow = false,
}: {
  /** Tightens the trigger for the collapsed sidebar rail, where the stock
   * padding would push it past the paper's edge. */
  readonly narrow?: boolean
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
  const goToAccount = (): void => {
    closeMenu()
    navigate(ACCOUNT_PATH)
  }
  const signOut = (): void => {
    closeMenu()
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
      >
        {/* Renders only inside an impersonation (and inside auth-ui's notice
            provider); it starts the same end attempt the banner carries, so
            the two are disabled and busy together. */}
        <ImpersonationEndMenuItem divider onActivate={closeMenu} />
        <MenuItem onClick={goToAccount}>
          {t('userMenu.accountSettings')}
        </MenuItem>
        <MenuItem onClick={signOut}>{t('userMenu.signOut')}</MenuItem>
      </Menu>
    </Box>
  )
}
