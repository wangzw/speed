/**
 * The shell chrome the app's two areas share: the brand, the header action
 * controls and the user menu the tenant frame and the console frame both
 * render in their top bar.
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
 *  - HeaderActions: the app-wide language switch and color-mode toggle,
 *    grouped for the frame's headerActions slot. Both shells render them.
 *  - TenantMenu: tenancy-ui's TenantSwitcher over the caller's own tenant
 *    list (the caller tenant read). product-shell places the switcher in the
 *    userMenu slot; the console banner, which resolves no tenant, does not
 *    render it.
 *  - UserMenu: an avatar trigger opening a menu of profile / account-settings
 *    / sign-out, replacing the bare sign-out button. While an impersonation
 *    is in force, auth-ui's end item sits first: closing the banner must not
 *    strand the operator without a way to end it.
 */

import { useCallback, useId, useRef, useState, useSyncExternalStore } from 'react'
import type { ReactElement } from 'react'
import Box from '@mui/material/Box'
import IconButton from '@mui/material/IconButton'
import Menu from '@mui/material/Menu'
import MenuItem from '@mui/material/MenuItem'
import Typography from '@mui/material/Typography'
import { evictTenantQueries, useAuthState } from '@speed/auth-core'
import { ImpersonationEndMenuItem } from '@speed/auth-ui'
import {
  languageDisplayName,
  readSupportedLanguages,
  switchLanguage,
  useTranslation,
} from '@speed/i18n'
import { Avatar } from '@speed/layout-kit'
import { useNavigate } from '@speed/route-kit'
import { TenantSwitcher } from '@speed/tenancy-ui'
import type { TenantOption } from '@speed/tenancy-ui'
import { themePresets } from '@speed/theme-presets'
import { useAppThemeMode } from '@speed/ui-kit'
import type { AppThemeMode } from '@speed/ui-kit'
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
 * It starts empty: the app settles it once the roster is read
 * (src/app.tsx), which is what keeps a tenant-scoped request from firing
 * before the current tenant's alias is known.
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
 * The app-wide header action controls: the language switch and the
 * color-mode toggle. Both shells -- the tenant frame and the console
 * banner -- render these; they are the same chrome in either area, so a
 * switch is available wherever the viewer is.
 *
 * The two controls are siblings, not a group: every slot container lays
 * its children out in a row, and the collapsed sidebar rail lays them out
 * in a column, so a wrapper around the pair keeps them side by side in a
 * rail too narrow to hold the row.
 */
export function HeaderActions(): ReactElement {
  return (
    <>
      <LanguageSwitcher />
      <ColorModeToggle />
    </>
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
 * host's bilingual 「个人空间 / Personal workspace」 text. The personal row is
 * recognised by the derived identifier compared EXACTLY: another account's
 * personal tenant shares the prefix and must render its alias instead.
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
   * name does not fit; the switch confirmation stays announced but is not
   * painted there. */
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
      announcement={compact ? 'visually-hidden' : 'inline'}
    />
  )
}

/**
 * The language switch: a globe trigger opening one item per language the
 * instance supports (readSupportedLanguages). Choosing one runs the
 * persisting switchLanguage -- a manual choice, so it lands on the device
 * too -- which fires the instance's languageChanged event, so the interface
 * and the document metadata follow. Option labels are the language names in
 * the current UI language.
 */
export function LanguageSwitcher(): ReactElement {
  const { t, i18n } = useTranslation(DEMO_NAMESPACE)
  const menuId = useId()
  const [anchorEl, setAnchorEl] = useState<HTMLElement | null>(null)
  const menuOpen = anchorEl !== null
  const languages = readSupportedLanguages(i18n)
  const current = i18n.language
  return (
    <Box>
      <IconButton
        color="inherit"
        aria-label={t('languageSwitcher.label')}
        aria-haspopup="menu"
        aria-expanded={menuOpen}
        aria-controls={menuOpen ? menuId : undefined}
        onClick={(event) => setAnchorEl(event.currentTarget)}
      >
        <GlobeIcon />
      </IconButton>
      <Menu
        id={menuId}
        anchorEl={anchorEl}
        open={menuOpen}
        onClose={() => setAnchorEl(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
        transformOrigin={{ vertical: 'top', horizontal: 'right' }}
      >
        {languages.map((tag) => (
          <MenuItem
            key={tag}
            selected={tag === current}
            onClick={() => {
              setAnchorEl(null)
              void switchLanguage(i18n, tag)
            }}
          >
            {languageDisplayName(tag, current)}
          </MenuItem>
        ))}
      </Menu>
    </Box>
  )
}

/** The device key the chosen colour mode is remembered under. */
export const COLOR_MODE_STORAGE_KEY = 'demo-web.color-mode'

/** Whether a stored value is one of the two theme modes. */
function isColorMode(value: unknown): value is AppThemeMode {
  return value === 'light' || value === 'dark'
}

/** The colour mode the device remembers, or null when none was stored (or
 * the storage is unavailable). */
export function readStoredColorMode(): AppThemeMode | null {
  try {
    const stored = window.localStorage.getItem(COLOR_MODE_STORAGE_KEY)
    return isColorMode(stored) ? stored : null
  } catch {
    // Storage can be refused (privacy modes, a sandboxed document). The
    // choice then lasts the page instead of failing the render.
    return null
  }
}

/** Records the user's colour-mode choice on the device. */
export function persistColorMode(mode: AppThemeMode): void {
  try {
    window.localStorage.setItem(COLOR_MODE_STORAGE_KEY, mode)
  } catch {
    // See readStoredColorMode: a refused storage is not a failed switch.
  }
}

/**
 * The mode the app starts in: the device's stored choice, else the operating
 * system's preference, else light. Read at boot (src/main.tsx passes it to
 * the assembly as the theme's defaultMode), so a reload with a stored choice
 * renders in it without a flash of the other mode.
 */
export function initialColorMode(): AppThemeMode {
  const stored = readStoredColorMode()
  if (stored !== null) {
    return stored
  }
  try {
    if (
      typeof window.matchMedia === 'function' &&
      window.matchMedia('(prefers-color-scheme: dark)').matches
    ) {
      return 'dark'
    }
  } catch {
    // No media-query support: fall through to the light default.
  }
  return 'light'
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
 * The color-mode toggle: a moon/sun button reading the active mode from
 * ui-kit's AppThemeProvider and flipping it. The button's accessible name
 * describes the action (switch to the other mode). The flip is a manual
 * choice, so it is written to the device: the next boot reads it back as the
 * theme's defaultMode (initialColorMode).
 */
export function ColorModeToggle(): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const { mode, toggleMode } = useAppThemeMode()
  const dark = mode === 'dark'
  const toggle = (): void => {
    persistColorMode(dark ? 'light' : 'dark')
    toggleMode()
  }
  return (
    <IconButton
      color="inherit"
      aria-label={dark ? t('colorMode.toLight') : t('colorMode.toDark')}
      onClick={toggle}
    >
      {dark ? <SunIcon /> : <MoonIcon />}
    </IconButton>
  )
}

/**
 * The account menu: an avatar trigger opening profile / account-settings /
 * sign-out. The trigger shows the signed-in user's initials (the avatar is
 * decorative -- the trigger's accessible name is the display name, or the
 * generic label while anonymous). The account page holds both the profile
 * and the settings surfaces (account-ui's sections span them), so the two
 * entries open the same page; sign-out drives the session's logout, whose
 * quiet terminal flip the shells observe.
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
  const triggerLabel =
    displayName !== undefined && displayName !== ''
      ? displayName
      : t('userMenu.accountMenu')

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
        <MenuItem onClick={goToAccount}>{t('userMenu.profile')}</MenuItem>
        <MenuItem onClick={goToAccount}>
          {t('userMenu.accountSettings')}
        </MenuItem>
        <MenuItem onClick={signOut}>{t('userMenu.signOut')}</MenuItem>
      </Menu>
    </Box>
  )
}

interface IconProps {
  /** Box length in px; stroke geometry is sized for the 24 grid. */
  readonly size?: number
}

/** A hand-drawn globe glyph, for the language switch (decorative). */
function GlobeIcon({ size = 22 }: IconProps): ReactElement {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <circle cx="12" cy="12" r="9" />
      <path d="M3 12h18" />
      <path d="M12 3c2.5 2.5 3.8 5.6 3.8 9s-1.3 6.5-3.8 9c-2.5-2.5-3.8-5.6-3.8-9S9.5 5.5 12 3z" />
    </svg>
  )
}

/** A hand-drawn moon glyph, for the switch to dark mode (decorative). */
function MoonIcon({ size = 22 }: IconProps): ReactElement {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z" />
    </svg>
  )
}

/** A hand-drawn sun glyph, for the switch to light mode (decorative). */
function SunIcon({ size = 22 }: IconProps): ReactElement {
  return (
    <svg
      aria-hidden="true"
      focusable="false"
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <circle cx="12" cy="12" r="4.5" />
      <path d="M12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.5 1.5M17.5 17.5 19 19M19 5l-1.5 1.5M6.5 17.5 5 19" />
    </svg>
  )
}
