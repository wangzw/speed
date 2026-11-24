import { useCallback, useId, useRef, useState, useSyncExternalStore } from 'react'
import type { ReactElement, ReactNode } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Collapse from '@mui/material/Collapse'
import Divider from '@mui/material/Divider'
import IconButton from '@mui/material/IconButton'
import Menu from '@mui/material/Menu'
import MenuItem from '@mui/material/MenuItem'
import type { SxProps, Theme } from '@mui/material/styles'
import Typography from '@mui/material/Typography'
import { evictTenantQueries, useAuthState } from '@speed/auth-core'
import { ImpersonationEndMenuItem } from '@speed/auth-ui'
import { useTranslation } from '@speed/i18n'
import { useNotificationUnread } from '@speed/notification-ui'
import type { NotificationUnread } from '@speed/notification-ui'
import { Avatar, avatarInitials } from '@speed/layout-kit'
import type { AppShellNavItem } from '@speed/layout-kit'
import { useNavigate, useRouteLinkProps } from '@speed/route-kit'
import {
  TenantMenuItems,
  TenantSwitchAnnouncement,
} from '@speed/tenancy-ui'
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
  TransferRequestsNavIcon,
} from './nav-icons.js'
import {
  ACCOUNT_SECTION_PATHS,
  NOTIFICATIONS_PATH,
  TRANSFER_REQUESTS_PATH,
} from './paths.js'
import { DEMO_NAMESPACE } from './resources.js'
import type { SelfTenantKind } from './tenant-self.js'

export interface DemoTenant extends TenantOption {
  readonly displayName?: string
  readonly kind?: SelfTenantKind
}

let currentTenantName: string | null = null

const currentTenantListeners = new Set<() => void>()

export function getCurrentTenant(): string | null {
  return currentTenantName
}

export function setCurrentTenant(name: string | null): void {
  if (currentTenantName === name) {
    return
  }
  currentTenantName = name
  for (const listener of currentTenantListeners) {
    listener()
  }
}

export function subscribeCurrentTenant(listener: () => void): () => void {
  currentTenantListeners.add(listener)
  return () => {
    currentTenantListeners.delete(listener)
  }
}

export function useCurrentTenant(): string | null {
  return useSyncExternalStore(subscribeCurrentTenant, getCurrentTenant)
}

interface RecordedTenant {
  readonly displayName?: string
  readonly kind?: SelfTenantKind
}

let recordedTenants: ReadonlyMap<string, RecordedTenant> = new Map()

export function setTenantNames(
  displayNames?: ReadonlyMap<string, string>,
): void {
  const next = new Map<string, RecordedTenant>()
  for (const [name, displayName] of displayNames ?? []) {
    next.set(name, {
      displayName:
        displayName ?? recordedTenants.get(name)?.displayName ?? '',
      kind: recordedTenants.get(name)?.kind,
    })
  }
  recordedTenants = next
}

export function setTenantName(
  name: string,
  kind?: SelfTenantKind,
  displayName?: string,
): void {
  const next = new Map(recordedTenants)
  next.set(name, {
    displayName: displayName ?? next.get(name)?.displayName ?? '',
    kind: kind ?? next.get(name)?.kind,
  })
  recordedTenants = next
}

export function getRecordedTenantName(name: string): string | null {
  return recordedTenants.has(name) ? name : null
}

export function getRecordedTenantDisplayName(name: string): string | null {
  const displayName = recordedTenants.get(name)?.displayName
  return displayName === undefined || displayName.trim() === ''
    ? null
    : displayName
}

export function getRecordedTenantKind(name: string): SelfTenantKind | null {
  return recordedTenants.get(name)?.kind ?? null
}

export function getCurrentTenantName(): string | null {
  return currentTenantName
}

export function useTenantSelection(
  initialTenantName: string | null,
): readonly [string | null, (tenantName: string | null) => void] {
  const { queryClient, session } = useAppServices()
  const [tenantName, setSelected] = useState<string | null>(initialTenantName)
  const committed = useRef<string | null>(initialTenantName)
  const select = useCallback(
    (nextTenantName: string | null): void => {
      const previous = committed.current
      if (previous !== null && previous !== nextTenantName) {
        session.setPermissionSet('tenant', null)
        evictTenantQueries(queryClient, previous)
      }
      committed.current = nextTenantName
      setCurrentTenant(nextTenantName)
      setSelected(nextTenantName)
    },
    [queryClient, session],
  )
  return [tenantName, select]
}

export function AppBrand(): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  return (
    <Typography
      noWrap
      variant="h6"
      // Not a heading: the page owns its single h1.
      component="div"
      sx={{ color: 'inherit', fontWeight: 600 }}
    >
      {t('app.title')}
    </Typography>
  )
}

export interface AccountMenuOrganizations {
  readonly tenants: readonly DemoTenant[]
  readonly tenantName: string | null
  readonly onSelect: (tenantName: string) => void
  readonly notice?: ReactNode
  readonly createHref?: string
}

export function tenantDisplayName(
  tenant: DemoTenant,
  personalLabel: string,
): string {
  if (tenant.kind === 'personal') {
    return personalLabel
  }
  const displayName = tenant.displayName?.trim() ?? ''
  return displayName === '' ? tenant.id : displayName
}

export function usePersonalOrganizationName(): string {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const snapshot = useAuthState()
  const displayName =
    snapshot.state === 'authenticated'
      ? snapshot.principal?.display_name.trim()
      : undefined
  return displayName === undefined || displayName === ''
    ? t('organization.personal')
    : displayName
}

export interface AccountMenuDestination {
  readonly id: string
  readonly label: string
  readonly target: string
  readonly icon?: ReactNode
}

export function TenantRosterFailure({
  onRetry,
  compact = false,
}: {
  readonly onRetry: () => void
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

export const COLOR_MODE_STORAGE_KEY = 'demo-web.color-mode'

function isColorModePreference(
  value: unknown,
): value is AppThemeModePreference {
  return value === 'light' || value === 'dark' || value === 'system'
}

export function readStoredColorModePreference(): AppThemeModePreference | null {
  try {
    const stored = window.localStorage.getItem(COLOR_MODE_STORAGE_KEY)
    return isColorModePreference(stored) ? stored : null
  } catch {
    return null
  }
}

export function persistColorModePreference(
  preference: AppThemeModePreference,
): void {
  try {
    window.localStorage.setItem(COLOR_MODE_STORAGE_KEY, preference)
  } catch {
  }
}

export function initialColorModePreference(): AppThemeModePreference {
  return readStoredColorModePreference() ?? 'system'
}

export const THEME_PRESET_STORAGE_KEY = 'demo-web.theme-preset'

function isThemePresetId(value: unknown): value is string {
  return (
    typeof value === 'string' &&
    themePresets.some((preset) => preset.id === value)
  )
}

export function readStoredThemePreset(): string | null {
  try {
    const stored = window.localStorage.getItem(THEME_PRESET_STORAGE_KEY)
    return isThemePresetId(stored) ? stored : null
  } catch {
    return null
  }
}

export function persistThemePreset(preset: string): void {
  try {
    window.localStorage.setItem(THEME_PRESET_STORAGE_KEY, preset)
  } catch {
  }
}

export function initialThemePreset(deploymentPreset?: string): string {
  const stored = readStoredThemePreset()
  if (stored !== null) {
    return stored
  }
  return isThemePresetId(deploymentPreset) ? deploymentPreset : 'default'
}

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
  organizationItems,
  organizations,
  destination,
  notifications = false,
}: {
  readonly narrow?: boolean
  readonly closeDrawer?: () => void
  readonly organizationItems?: readonly AppShellNavItem[]
  readonly organizations?: AccountMenuOrganizations
  readonly destination?: AccountMenuDestination
  readonly notifications?: boolean
} = {}): ReactElement {
  const { session } = useAppServices()
  const navigate = useNavigate()
  const { t } = useTranslation(DEMO_NAMESPACE)
  const snapshot = useAuthState()
  const menuId = useId()
  const [anchorEl, setAnchorEl] = useState<HTMLElement | null>(null)
  const menuOpen = anchorEl !== null
  const [switchedTenant, setSwitchedTenant] = useState<string | null>(null)
  const personalOrganizationName = usePersonalOrganizationName()
  const selectTenant = (tenantName: string): void => {
    if (organizations === undefined) {
      return
    }
    const picked = organizations.tenants.find(
      (tenant) => tenant.id === tenantName,
    )
    organizations.onSelect(tenantName)
    setSwitchedTenant(
      picked === undefined
        ? null
        : tenantDisplayName(picked, personalOrganizationName),
    )
  }

  const displayName =
    snapshot.state === 'authenticated'
      ? snapshot.principal?.display_name
      : undefined
  const initials = avatarInitials(displayName)
  const accountName =
    displayName === undefined || displayName.trim() === ''
      ? t('userMenu.accountMenu')
      : initials === ''
        ? displayName
        : `${initials} ${displayName}`
  const unread = useNotificationUnread(notifications)
  const triggerLabel =
    unread.phrase === null
      ? accountName
      : t('userMenu.triggerWithNotifications', {
          account: accountName,
          notifications: unread.phrase,
        })

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
    void session.logout().catch(() => undefined)
  }

  return (
    <Box sx={{ position: 'relative', display: 'inline-flex' }}>
      <IconButton
        color="inherit"
        size={narrow ? 'small' : 'medium'}
        aria-label={triggerLabel}
        aria-haspopup="menu"
        aria-expanded={menuOpen}
        aria-controls={menuOpen ? menuId : undefined}
        data-testid="user-menu-trigger"
        onClick={(event) => setAnchorEl(event.currentTarget)}
      >
        <Avatar name={displayName} decorative size={narrow ? 28 : 32} />
      </IconButton>
      <AccountUnreadOverlay
        text={unread.badgeText}
        placement={narrow ? 'header-narrow' : 'header'}
      />
      <TenantSwitchAnnouncement tenantName={switchedTenant} />
      <Menu
        id={menuId}
        anchorEl={anchorEl}
        open={menuOpen}
        onClose={closeMenu}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
        transformOrigin={{ vertical: 'top', horizontal: 'right' }}
        slotProps={{ paper: { sx: accountMenuPaperSx } }}
      >
        <AccountMenuItems
          onNavigate={goTo}
          onSignOut={signOut}
          onClose={closeMenu}
          onDismiss={dismissSurface}
          organizationItems={organizationItems}
          organizations={
            organizations === undefined
              ? undefined
              : { ...organizations, onSelect: selectTenant }
          }
          destination={destination}
          unread={unread}
        />
      </Menu>
    </Box>
  )
}

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

const accountMenuItemIconSx = {
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  flexShrink: 0,
  width: (theme: Theme) => theme.spacing(4.5),
  color: 'text.secondary',
} as const

const accountMenuItemSx = { pl: '6px' } as const

const nonInteractiveRowSx = {
  display: 'flex',
  alignItems: 'center',
  py: 0.75,
  pr: 2,
  pl: 2,
  typography: 'body1',
} as const

function OrganizationTile({ name }: { readonly name: string }): ReactElement {
  const initial = (Array.from(name.trim())[0] ?? '').toUpperCase()
  return (
    <Box
      component="span"
      aria-hidden="true"
      sx={(theme) => ({
        display: 'inline-flex',
        alignItems: 'center',
        justifyContent: 'center',
        width: theme.spacing(3),
        height: theme.spacing(3),
        borderRadius: 0.75,
        bgcolor: 'action.hover',
        color: 'text.secondary',
        typography: 'caption',
        fontWeight: 600,
        lineHeight: 1,
      })}
    >
      {initial}
    </Box>
  )
}

function AccountUnreadOverlay({
  text,
  placement,
}: {
  readonly text: string | null
  readonly placement: 'header' | 'header-narrow' | 'row' | 'row-narrow'
}): ReactElement | null {
  if (text === null) {
    return null
  }
  const anchor =
    placement === 'header' || placement === 'header-narrow'
      ?
        { top: '0px', right: '0px' }
      : placement === 'row'
        ? { top: '7px', left: '41px' }
        : { top: '7px', left: '50%', transform: 'translate(5px, 0)' }
  return (
    <Box
      component="span"
      aria-hidden="true"
      sx={(theme) => ({
        ...anchor,
        position: 'absolute',
        pointerEvents: 'none',
        minWidth: theme.spacing(2.25),
        px: theme.spacing(0.5),
        borderRadius: 999,
        bgcolor: 'error.light',
        color: 'grey.900',
        typography: 'caption',
        fontWeight: 700,
        lineHeight: 1.5,
        textAlign: 'center',
      })}
    >
      {text}
    </Box>
  )
}

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

function AccountMenuItems({
  onNavigate,
  onSignOut,
  onClose,
  onDismiss,
  itemSx,
  organizationItems,
  organizations,
  destination,
  unread,
}: {
  readonly onNavigate: (path: string) => void
  readonly onSignOut: () => void
  readonly onClose: () => void
  readonly onDismiss?: () => void
  readonly itemSx?: typeof accountMenuItemSx
  readonly organizationItems?: readonly AppShellNavItem[]
  readonly organizations?: AccountMenuOrganizations
  readonly destination?: AccountMenuDestination
  readonly unread?: NotificationUnread
}): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const notificationsShown =
    unread !== undefined && unread.badgeText !== null
  return (
    <>
      <ImpersonationEndMenuItem divider onActivate={onClose} />
      {organizations === undefined ? null : (
        <>
          <OrganizationMenuEntry
            organizations={organizations}
            onClose={onClose}
            itemSx={itemSx}
          />
          <Divider sx={{ my: 1 }} />
        </>
      )}
      <MenuItem
        onClick={() => onNavigate(NOTIFICATIONS_PATH)}
        sx={itemSx}
        aria-label={
          notificationsShown
            // Keep a separator between label and count: joined without one the accessible name reads as one word.
            ? t('userMenu.messagesWithCount', { n: unread?.badgeText })
            : undefined
        }
      >
        <Box component="span" sx={accountMenuItemIconSx}>
          <NotificationsNavIcon size={24} />
        </Box>
        <Box component="span" sx={{ flex: '1 1 auto', minWidth: 0 }}>
          {t('userMenu.messages')}
        </Box>
        {notificationsShown ? ' ' : null}
        {notificationsShown ? (
          <Box
            component="span"
            aria-hidden="true"
            sx={(theme) => ({
              flex: '0 0 auto',
              ml: 2,
              minWidth: theme.spacing(2.5),
              px: theme.spacing(0.75),
              py: theme.spacing(0.125),
              borderRadius: 999,
              bgcolor: 'error.light',
              color: 'grey.900',
              typography: 'caption',
              fontWeight: 700,
              lineHeight: 1.5,
              textAlign: 'center',
            })}
          >
            {unread?.badgeText}
          </Box>
        ) : null}
      </MenuItem>
      <MenuItem
        onClick={() => onNavigate(TRANSFER_REQUESTS_PATH)}
        sx={itemSx}
      >
        <Box component="span" sx={accountMenuItemIconSx}>
          <TransferRequestsNavIcon size={24} />
        </Box>
        <Box component="span" sx={{ flex: '1 1 auto', minWidth: 0 }}>
          {t('userMenu.transferRequests')}
        </Box>
      </MenuItem>
      <Divider sx={{ my: 1 }} />
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
      {organizationItems !== undefined && organizationItems.length > 0 ? (
        <>
          <Divider sx={{ my: 1 }} />
          <Box role="group" aria-label={t('nav.organizationAdmin')}>
            {organizationItems.map((item) => (
              <MenuItem
                key={item.id}
                component="a"
                href={item.href}
                onClick={(event) => {
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
      {destination === undefined ? null : (
        <>
          <Divider sx={{ my: 1 }} />
          <AccountMenuDestinationItem
            destination={destination}
            onDismiss={onDismiss}
            itemSx={itemSx}
          />
        </>
      )}
      <Divider sx={{ my: 1 }} />
      <MenuItem onClick={onSignOut} sx={itemSx}>
        <Box component="span" sx={accountMenuItemIconSx}>
          <SignOutIcon />
        </Box>
        {t('userMenu.signOut')}
      </MenuItem>
    </>
  )
}

function CreateOrganizationMenuItem({
  href,
  onDismiss,
  itemSx,
}: {
  readonly href: string
  readonly onDismiss: () => void
  readonly itemSx?: typeof accountMenuItemSx
}): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const link = useRouteLinkProps(href)
  return (
    <MenuItem
      component="a"
      href={link.href}
      onClick={(event) => {
        link.onClick(event)
        onDismiss()
      }}
      sx={itemSx}
    >
      <Box component="span" sx={accountMenuItemIconSx} aria-hidden="true">
        <Box
          component="svg"
          viewBox="0 0 24 24"
          width={24}
          height={24}
          fill="none"
          stroke="currentColor"
          strokeWidth={2}
          strokeLinecap="round"
        >
          <path d="M12 5v14M5 12h14" />
        </Box>
      </Box>
      {t('organization.create')}
    </MenuItem>
  )
}

export function OrganizationMenuEntry({
  organizations,
  onClose,
  itemSx,
}: {
  readonly organizations: AccountMenuOrganizations
  readonly onClose: () => void
  readonly itemSx?: typeof accountMenuItemSx
}): ReactElement {
  const { t } = useTranslation(DEMO_NAMESPACE)
  const [expanded, setExpanded] = useState(false)
  const personalOrganizationName = usePersonalOrganizationName()
  const options: readonly TenantOption[] = organizations.tenants.map(
    (tenant) => ({
      id: tenant.id,
      name: tenantDisplayName(tenant, personalOrganizationName),
    }),
  )
  const current =
    options.find((tenant) => tenant.id === organizations.tenantName) ?? null
  const others = options.filter(
    (tenant) => tenant.id !== organizations.tenantName,
  )
  const rowContent = (
    <>
      <Box component="span" sx={accountMenuItemIconSx}>
        <OrganizationTile name={current?.name ?? ''} />
      </Box>
      <Box
        component="span"
        sx={{
          flex: '1 1 auto',
          minWidth: 0,
          overflow: 'hidden',
          textOverflow: 'ellipsis',
          whiteSpace: 'nowrap',
          fontWeight: 600,
        }}
      >
        {current === null ? t('userMenu.organizations') : current.name}
      </Box>
    </>
  )
  if (others.length === 0) {
    return (
      <>
        <Box
          role="group"
          aria-label={current?.name ?? t('userMenu.organizations')}
          sx={[nonInteractiveRowSx, itemSx ?? {}] as SxProps<Theme>}
        >
          {rowContent}
        </Box>
        {organizations.notice}
      </>
    )
  }
  return (
    <>
      <MenuItem
        aria-expanded={expanded}
        onClick={() => setExpanded((open) => !open)}
        sx={itemSx}
      >
        {rowContent}
        <Box
          component="svg"
          aria-hidden="true"
          focusable="false"
          viewBox="0 0 24 24"
          width={18}
          height={18}
          fill="currentColor"
          sx={(theme) => ({
            flex: '0 0 auto',
            color: 'text.secondary',
            transform: expanded ? 'rotate(90deg)' : 'none',
            transition: theme.transitions.create('transform'),
          })}
        >
          <path d="M9 6.5 14.5 12 9 17.5z" />
        </Box>
      </MenuItem>
      <Collapse in={expanded} timeout="auto" unmountOnExit>
        <Box role="group" aria-label={t('userMenu.organizations')}>
          <TenantMenuItems
            tenants={others}
            currentTenantId={organizations.tenantName}
            onSelect={organizations.onSelect}
            onDismiss={() => {
              setExpanded(false)
              onClose()
            }}
            itemSx={itemSx}
            leading={(tenant) => (
              <Box component="span" sx={accountMenuItemIconSx}>
                <OrganizationTile name={tenant.name} />
              </Box>
            )}
          />
          {organizations.createHref === undefined ? null : (
            <CreateOrganizationMenuItem
              href={organizations.createHref}
              onDismiss={() => {
                setExpanded(false)
                onClose()
              }}
              itemSx={itemSx}
            />
          )}
        </Box>
      </Collapse>
      {organizations.notice}
    </>
  )
}

function AccountMenuDestinationItem({
  destination,
  onDismiss,
  itemSx,
}: {
  readonly destination: AccountMenuDestination
  readonly onDismiss?: () => void
  readonly itemSx?: typeof accountMenuItemSx
}): ReactElement {
  const { href, onClick } = useRouteLinkProps(destination.target)
  return (
    <MenuItem
      component="a"
      href={href}
      onClick={(event) => {
        onClick(event)
        onDismiss?.()
      }}
      sx={itemSx}
    >
      {destination.icon === undefined ? null : (
        <Box component="span" sx={accountMenuItemIconSx}>
          {destination.icon}
        </Box>
      )}
      {destination.label}
    </MenuItem>
  )
}

export function AccountEntry({
  narrow = false,
  closeDrawer,
  organizationItems,
  organizations,
  destination,
  notifications = false,
}: {
  readonly narrow?: boolean
  readonly closeDrawer?: () => void
  readonly organizationItems?: readonly AppShellNavItem[]
  readonly organizations?: AccountMenuOrganizations
  readonly destination?: AccountMenuDestination
  readonly notifications?: boolean
}): ReactElement {
  const { session } = useAppServices()
  const navigate = useNavigate()
  const { t } = useTranslation(DEMO_NAMESPACE)
  const snapshot = useAuthState()
  const menuId = useId()
  const [anchorEl, setAnchorEl] = useState<HTMLElement | null>(null)
  const menuOpen = anchorEl !== null
  const [switchedTenant, setSwitchedTenant] = useState<string | null>(null)
  const personalOrganizationName = usePersonalOrganizationName()
  const selectTenant = (tenantName: string): void => {
    if (organizations === undefined) {
      return
    }
    const picked = organizations.tenants.find(
      (tenant) => tenant.id === tenantName,
    )
    organizations.onSelect(tenantName)
    setSwitchedTenant(
      picked === undefined
        ? null
        : tenantDisplayName(picked, personalOrganizationName),
    )
  }

  const displayName =
    snapshot.state === 'authenticated'
      ? snapshot.principal?.display_name
      : undefined
  const initials = avatarInitials(displayName)
  const accountName =
    displayName === undefined || displayName.trim() === ''
      ? t('userMenu.accountMenu')
      : initials === ''
        ? displayName
        : `${initials} ${displayName}`
  const unread = useNotificationUnread(notifications)
  const triggerLabel =
    unread.phrase === null
      ? accountName
      : t('userMenu.triggerWithNotifications', {
          account: accountName,
          notifications: unread.phrase,
        })

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
    void session.logout().catch(() => undefined)
  }

  return (
    <Box data-testid="account-entry" sx={{ width: '100%' }}>
      <Divider />
      <TenantSwitchAnnouncement tenantName={switchedTenant} />
      <Box sx={{ pt: '12px', pb: '12px', position: 'relative' }}>
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
              width: narrow ? 'auto' : '36px',
            }}
          >
            <Avatar name={displayName} decorative size={narrow ? 28 : 32} />
          </Box>
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
                  pl: '8px',
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
        <AccountUnreadOverlay
          text={unread.badgeText}
          placement={narrow ? 'row-narrow' : 'row'}
        />
      </Box>
      <Menu
        id={menuId}
        anchorEl={anchorEl}
        open={menuOpen}
        onClose={closeMenu}
        anchorOrigin={{ vertical: 'top', horizontal: 'left' }}
        transformOrigin={{ vertical: 'bottom', horizontal: 'left' }}
        marginThreshold={0}
        slotProps={{
          paper: {
            sx: (theme) => ({
              ...accountMenuPaperSx(theme),
              ml: '9px',
            }),
          },
        }}
      >
        <AccountMenuItems
          onNavigate={goTo}
          onSignOut={signOut}
          onClose={closeMenu}
          onDismiss={dismissSurface}
          itemSx={accountMenuItemSx}
          organizationItems={organizationItems}
          organizations={
            organizations === undefined
              ? undefined
              : { ...organizations, onSelect: selectTenant }
          }
          destination={destination}
          unread={unread}
        />
      </Menu>
    </Box>
  )
}
