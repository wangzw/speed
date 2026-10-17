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
 *  - TenantMenu: tenancy-ui's TenantSwitcher over the host's own placeholder
 *    tenant list. product-shell places the switcher in the userMenu slot;
 *    the console banner, which resolves no tenant, does not render it.
 *  - UserMenu: an avatar trigger opening a menu of profile / account-settings
 *    / sign-out, replacing the bare sign-out button.
 */

import { useId, useState } from 'react'
import type { ReactElement } from 'react'
import Box from '@mui/material/Box'
import IconButton from '@mui/material/IconButton'
import Menu from '@mui/material/Menu'
import MenuItem from '@mui/material/MenuItem'
import Typography from '@mui/material/Typography'
import { useAuthState } from '@speed/auth-core'
import { readSupportedLanguages, switchLanguage, useTranslation } from '@speed/i18n'
import { Avatar } from '@speed/layout-kit'
import { useNavigate } from '@speed/route-kit'
import { TenantSwitcher } from '@speed/tenancy-ui'
import type { TenantOption } from '@speed/tenancy-ui'
import { useAppThemeMode } from '@speed/ui-kit'
import { useAppServices } from './app-services.js'
import { ACCOUNT_PATH } from './paths.js'
import { DEMO_NAMESPACE } from './resources.js'

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
 * The tenant list the switcher offers. The skeleton carries no tenant
 * roster -- there is no membership source -- so the app supplies a
 * placeholder list; a product replaces it with the tenants its own
 * generated operations return (each row's id and alias are what the roster
 * holds, so the two may differ after a rename). The placeholder must not
 * name a tenant the deployment does not serve: the seeded roster in
 * starters/demo/server/testdata registers `acme`, so that is the whole list
 * here. The current tenant is a routing fact the server resolves per
 * request, held here as host state.
 */
export const DEMO_TENANTS: readonly DemoTenant[] = [
  { id: 'acme', alias: 'acme', name: 'Acme Inc.' },
]

/**
 * The tenant the app falls back to when the roster cannot be read: the
 * placeholder list's first.
 */
export const DEFAULT_TENANT_ID: string | null = DEMO_TENANTS[0]?.id ?? null

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

/** The tenant the switcher treats as current (its TenantOption.id). */
export function getCurrentTenantId(): string | null {
  return currentTenantId
}

/** Records the host's current tenant; the switcher's pick (a tenant id) lands here. */
export function setCurrentTenantId(tenantId: string | null): void {
  currentTenantId = tenantId
}

/**
 * The alias -- the request-surface name -- of one tenant, in the given list
 * (the placeholder by default); null when the id is null or not on the list.
 * Kept separate from the tenant id so a rename (which moves the alias and not
 * the id) cannot leak the id into the field the server resolves against.
 */
export function tenantAlias(
  tenantId: string | null,
  tenants: readonly DemoTenant[] = DEMO_TENANTS,
): string | null {
  if (tenantId === null) {
    return null
  }
  return tenants.find((tenant) => tenant.id === tenantId)?.alias ?? null
}

/**
 * The aliases the roster reported for known tenant ids, layered over the
 * placeholder's own. The placeholder's list is what the switcher offers (the
 * demo holds no roster of its own), but a name the placeholder fixed can go
 * stale: a rename moves the alias and leaves the id, and a request that kept
 * announcing the old alias would be refused 404. So the app reads the roster
 * (its own generated operation) once a viewer is signed in and records each
 * tenant's current alias here (src/app.tsx), and the announced value prefers
 * it. The read needs tenant.read in the platform domain, so it answers for the
 * demo's operator but not for an ordinary member (403); a viewer without it
 * keeps the placeholder's aliases, which answer only while the roster's alias
 * still equals them -- after a rename such a viewer announces a stale name and
 * is refused 404, because no member-facing endpoint carries the current alias.
 */
let rosterAliases: ReadonlyMap<string, string> = new Map()

/** Records the roster's current aliases, keyed by tenant id. */
export function setTenantAliases(aliases: ReadonlyMap<string, string>): void {
  rosterAliases = aliases
}

/**
 * The value the client announces on every request as X-Tenant: the current
 * tenant's ALIAS, the name the request surface carries -- the roster's current
 * alias when the app has read it, the placeholder's otherwise. This is the
 * tenantProvider the assembly reads per attempt (src/main.tsx); it is not
 * getCurrentTenantId, whose value the switcher keys on.
 */
export function getCurrentTenantAlias(): string | null {
  if (currentTenantId === null) {
    return null
  }
  return rosterAliases.get(currentTenantId) ?? tenantAlias(currentTenantId)
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
 */
export function HeaderActions(): ReactElement {
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
      <LanguageSwitcher />
      <ColorModeToggle />
    </Box>
  )
}

/**
 * The tenant switcher over the host's placeholder tenant list. The
 * current tenant is a host fact (a route change, not a session op), and
 * product-shell places the switcher in the userMenu slot; the console
 * banner, which resolves no tenant, does not render it.
 */
export function TenantMenu({
  tenantId,
  onTenantSelect,
}: {
  /** The id of the tenant the host currently resolves to, or null. */
  readonly tenantId: string | null
  /** Reports the tenant the user picked; the host switches its route/state. */
  readonly onTenantSelect: (tenantId: string) => void
}): ReactElement {
  return (
    <TenantSwitcher
      tenants={DEMO_TENANTS}
      currentTenantId={tenantId}
      onSelect={onTenantSelect}
    />
  )
}

/** The language name for a tag, in the current UI language; the raw tag is the fallback. */
function languageName(tag: string, uiLanguage: string): string {
  try {
    return new Intl.DisplayNames([uiLanguage], { type: 'language' }).of(tag) ?? tag
  } catch {
    return tag
  }
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
            {languageName(tag, current)}
          </MenuItem>
        ))}
      </Menu>
    </Box>
  )
}

/**
 * The color-mode toggle: a moon/sun button reading the active mode from
 * ui-kit's AppThemeProvider and flipping it. The button's accessible name
 * describes the action (switch to the other mode).
 */
export function ColorModeToggle(): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const { mode, toggleMode } = useAppThemeMode()
  const dark = mode === 'dark'
  return (
    <IconButton
      color="inherit"
      aria-label={dark ? t('colorMode.toLight') : t('colorMode.toDark')}
      onClick={toggleMode}
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
export function UserMenu(): ReactElement {
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
        aria-label={triggerLabel}
        aria-haspopup="menu"
        aria-expanded={menuOpen}
        aria-controls={menuOpen ? menuId : undefined}
        onClick={(event) => setAnchorEl(event.currentTarget)}
      >
        <Avatar name={displayName} decorative size={32} />
      </IconButton>
      <Menu
        id={menuId}
        anchorEl={anchorEl}
        open={menuOpen}
        onClose={closeMenu}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
        transformOrigin={{ vertical: 'top', horizontal: 'right' }}
      >
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
