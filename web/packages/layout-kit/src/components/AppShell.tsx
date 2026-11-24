/** AppShell: the responsive app-chrome shell with a fixed head, a navigation drawer and a main content region. */

import { useEffect, useId, useRef, useState } from 'react'
import type { MouseEvent, MouseEventHandler, ReactElement, ReactNode } from 'react'
import type { SxProps, Theme } from '@mui/material/styles'
import { alpha, useTheme } from '@mui/material/styles'
import AppBar from '@mui/material/AppBar'
import Box from '@mui/material/Box'
import Divider from '@mui/material/Divider'
import Drawer from '@mui/material/Drawer'
import IconButton from '@mui/material/IconButton'
import Link from '@mui/material/Link'
import List from '@mui/material/List'
import ListItem from '@mui/material/ListItem'
import ListItemButton from '@mui/material/ListItemButton'
import ListItemIcon from '@mui/material/ListItemIcon'
import ListItemText from '@mui/material/ListItemText'
import Toolbar from '@mui/material/Toolbar'
import Tooltip from '@mui/material/Tooltip'
import useMediaQuery from '@mui/material/useMediaQuery'
import { visuallyHiddenSx } from '@speed/ui-kit'
import { useLayoutKitTranslation } from '../internal/translation.js'
import {
  ChevronLeftIcon,
  ChevronRightIcon,
  CloseIcon,
  ExpandMoreIcon,
  MenuIcon,
} from '../internal/icons.js'

/**
 * One navigation entry. The host owns `selected`; `href` renders a link and/or
 * `onClick` handles activation.
 */
export interface AppShellNavItem {
  /** Stable identity for the React key; not rendered. */
  readonly id: string
  /** The item's visible label; host content, already translated. */
  readonly label: ReactNode
  /** Optional leading icon; host content. */
  readonly icon?: ReactNode
  /** Renders the item as a link to this destination when set. */
  readonly href?: string
  /** Click handler; fires whether or not `href` is also set. */
  readonly onClick?: MouseEventHandler<HTMLElement>
  /** Whether this is the current page/section, computed by the host. */
  readonly selected?: boolean
  /**
   * Second-level nesting: when set, the item becomes an expandable parent whose
   * children render indented, each a real route. The parent's own `href`,
   * `onClick` and `selected` are not rendered.
   */
  readonly children?: readonly AppShellNavItem[]
}

/**
 * One titled, collapsible group: a heading that is itself the disclosure
 * control over the `items` beneath it.
 */
export interface AppShellNavGroup {
  /** Stable identity for the React key and the group's initial state. */
  readonly id: string
  /** The group's visible heading; host content, already translated. */
  readonly label: ReactNode
  /** Optional leading icon; in the collapsed rail it replaces the heading text. */
  readonly icon?: ReactNode
  /** The entries inside the group, in order. */
  readonly items: readonly AppShellNavItem[]
  /** Whether the group starts collapsed; defaults to false. */
  readonly defaultCollapsed?: boolean
}

/** One navigation entry or grouped section, as the host declares it. */
export type AppShellNavEntry = AppShellNavItem | AppShellNavGroup

/** The state a `navHeader` or `navFooter` render function receives. */
export interface AppShellNavSlotState {
  /** Whether the mounted drawer is the collapsed icon-only rail; always false in the temporary drawer. */
  readonly narrow: boolean
  /** Closes a temporary drawer; a no-op for the permanent drawer. */
  readonly closeDrawer: () => void
}

/** A drawer slot: host content, or a render function given the mounted drawer's state. */
export type AppShellNavSlot =
  | ReactNode
  | ((state: AppShellNavSlotState) => ReactNode)

/**
 * The shell's chrome arrangement: `'topbar'` (the default) keeps the brand,
 * header actions and user menu in the fixed app bar; `'sidebar'` moves them
 * into the drawer paper on `md` and up.
 */
export type AppShellShellLayout = 'topbar' | 'sidebar'

/** Props for AppShell. */
export interface AppShellProps {
  /** The shell's chrome arrangement; `'topbar'` is the default. */
  readonly shellLayout?: AppShellShellLayout
  /** The navigation entries rendered in the drawer, plain entries or titled groups. */
  readonly navItems: readonly AppShellNavEntry[]
  /** Start-of-AppBar content, typically a logo or product name. */
  readonly header?: ReactNode
  /** End-of-AppBar content before `userMenu`; in the sidebar layout it moves to the bottom row. */
  readonly headerActions?: AppShellNavSlot
  /** Far end-of-AppBar content, typically the account menu trigger. */
  readonly userMenu?: AppShellNavSlot
  /**
   * A full-width banner below the app bar, part of the fixed head. The slot
   * adds no padding, border or height of its own, and absent/null/false renders
   * nothing.
   */
  readonly banner?: ReactNode
  /** The navigation landmark's accessible name; defaults to `appShell.navLabel`. */
  readonly navLabel?: string
  /** Host content rendered inside the drawer paper before the navigation landmark. */
  readonly navHeader?: AppShellNavSlot
  /** Host content rendered inside the drawer paper after the navigation landmark. */
  readonly navFooter?: AppShellNavSlot
  /** The content region, rendered inside the `main` landmark. */
  readonly children: ReactNode
  /** Mobile drawer open state; omit with `onMobileOpenChange` for an uncontrolled drawer. */
  readonly mobileOpen?: boolean
  /** Fired when the mobile drawer's open state should change. */
  readonly onMobileOpenChange?: (open: boolean) => void
  /** Desktop sidebar collapse state; omit to keep the sidebar always open. */
  readonly sidebarCollapsed?: boolean
  /** Fired when the sidebar collapse state should change; supplying it renders the toggle. */
  readonly onSidebarCollapsedChange?: (collapsed: boolean) => void
  /** Drawer width in px, both variants. Defaults to 280. */
  readonly sidebarWidth?: number
  /** Extra styling merged onto the root layout box. */
  readonly sx?: SxProps<Theme>
}

const DEFAULT_SIDEBAR_WIDTH = 280

/** The collapsed rail's width in theme spacing units (a 24px icon plus 2 x 16px inset = 56 = 7 units). */
const COLLAPSED_SIDEBAR_SPACING_UNITS = 7

/** The collapsed rail's horizontal row inset in px: the focus ring's reach. */
const RAIL_ROW_INSET_PX = 4

/** The rail's floor: a touch target plus the ring's clearance on both sides plus the paper's divider. */
const COLLAPSED_SIDEBAR_MIN_WIDTH = 44 + RAIL_ROW_INSET_PX * 2 + 1

const railIconItemSx = { justifyContent: 'center', px: 1 } as const

/**
 * A collapsed rail entry without an icon keeps its label visible, clipped to
 * the rail; the button's `overflow: hidden` is only a backstop.
 */
const railTextItemSx = {
  px: 1,
  overflow: 'hidden',
} as const

/**
 * The clipped label must be declared on the text element, not the button:
 * `text-overflow` never reaches a descendant block's text.
 */
const railTextSx = {
  minWidth: 0,
  overflow: 'hidden',
  whiteSpace: 'nowrap',
  textOverflow: 'ellipsis',
} as const

const railIconSx = { minWidth: 0, mr: 0, justifyContent: 'center' } as const

function RailTooltip({
  enabled,
  label,
  children,
}: {
  readonly enabled: boolean
  readonly label: ReactNode
  readonly children: ReactElement
}): ReactElement {
  if (!enabled) {
    return children
  }
  return (
    <Tooltip title={label} placement="right">
      {children}
    </Tooltip>
  )
}

function isNavGroup(entry: AppShellNavEntry): entry is AppShellNavGroup {
  return 'items' in entry
}

function hasSlotContent(value: AppShellNavSlot | undefined): boolean {
  return value !== undefined && value !== null && value !== false
}

function renderNavSlot(
  slot: AppShellNavSlot | undefined,
  state: AppShellNavSlotState,
): ReactNode {
  if (slot === undefined || slot === null || slot === false) {
    return null
  }
  return typeof slot === 'function' ? slot(state) : slot
}

function DisclosureChevron({ expanded }: { readonly expanded: boolean }) {
  return (
    <Box
      component="span"
      sx={{
        display: 'flex',
        alignItems: 'center',
        flexShrink: 0,
        transform: expanded ? 'rotate(180deg)' : 'none',
        transition: 'transform 150ms',
      }}
    >
      <ExpandMoreIcon />
    </Box>
  )
}

function NavItemRow({
  item,
  onNavigate,
  indented = false,
  narrow = false,
}: {
  readonly item: AppShellNavItem
  readonly onNavigate?: () => void
  readonly indented?: boolean
  readonly narrow?: boolean
}) {
  // Called unconditionally (hook order): only the parent branch uses them.
  const contentId = useId()
  const [expanded, setExpanded] = useState(true)
  const indentSx = indented && !narrow ? { pl: 4 } : undefined
  const hasIcon = item.icon !== undefined
  const contentSx = narrow
    ? hasIcon
      ? railIconItemSx
      : railTextItemSx
    : indentSx
  const iconSx = narrow && hasIcon ? railIconSx : undefined
  const textSx = narrow && hasIcon ? visuallyHiddenSx : undefined
  const textSlotProps =
    narrow && !hasIcon ? { primary: { sx: railTextSx } } : undefined

  const activate = (event: MouseEvent<HTMLElement>): void => {
    item.onClick?.(event)
    onNavigate?.()
  }

  const hasChildren = item.children !== undefined && item.children.length > 0
  if (hasChildren) {
    return (
      <ListItem disablePadding sx={{ display: 'block' }}>
        <RailTooltip enabled={narrow} label={item.label}>
          <ListItemButton
            aria-expanded={expanded}
            aria-controls={contentId}
            onClick={() => {
              setExpanded((previous) => !previous)
            }}
            sx={contentSx}
          >
            {item.icon !== undefined && (
              <ListItemIcon sx={iconSx}>{item.icon}</ListItemIcon>
            )}
            <ListItemText
              primary={item.label}
              sx={textSx}
              slotProps={textSlotProps}
            />
            <DisclosureChevron expanded={expanded} />
          </ListItemButton>
        </RailTooltip>
        <Box id={contentId} hidden={!expanded}>
          <List disablePadding>
            {(item.children ?? []).map((child) => (
              <NavItemRow
                key={child.id}
                item={child}
                onNavigate={onNavigate}
                indented
                narrow={narrow}
              />
            ))}
          </List>
        </Box>
      </ListItem>
    )
  }

  const label = (
    <>
      {item.icon !== undefined && (
        <ListItemIcon sx={iconSx}>{item.icon}</ListItemIcon>
      )}
      <ListItemText
        primary={item.label}
        sx={textSx}
        slotProps={textSlotProps}
      />
    </>
  )
  return (
    <ListItem disablePadding>
      <RailTooltip enabled={narrow} label={item.label}>
        {item.href !== undefined ? (
          <ListItemButton
            component="a"
            href={item.href}
            onClick={activate}
            selected={item.selected ?? false}
            aria-current={item.selected === true ? 'page' : undefined}
            sx={contentSx}
          >
            {label}
          </ListItemButton>
        ) : (
          <ListItemButton
            onClick={activate}
            selected={item.selected ?? false}
            aria-current={item.selected === true ? 'page' : undefined}
            sx={contentSx}
          >
            {label}
          </ListItemButton>
        )}
      </RailTooltip>
    </ListItem>
  )
}

function NavGroupItem({
  group,
  onNavigate,
  narrow = false,
}: {
  readonly group: AppShellNavGroup
  readonly onNavigate?: () => void
  readonly narrow?: boolean
}) {
  const contentId = useId()
  const [expanded, setExpanded] = useState(!(group.defaultCollapsed ?? false))
  const hasIcon = group.icon !== undefined
  return (
    <ListItem disablePadding sx={{ display: 'block' }}>
      <RailTooltip enabled={narrow} label={group.label}>
        <ListItemButton
          aria-expanded={expanded}
          aria-controls={contentId}
          onClick={() => {
            setExpanded((previous) => !previous)
          }}
          // In the rail a group heading with an icon becomes an icon row; without one it keeps the clipped text.
          sx={narrow ? (hasIcon ? railIconItemSx : railTextItemSx) : undefined}
        >
          {group.icon !== undefined && (
            <ListItemIcon sx={narrow && hasIcon ? railIconSx : undefined}>
              {group.icon}
            </ListItemIcon>
          )}
          <ListItemText
            primary={group.label}
            sx={narrow && hasIcon ? visuallyHiddenSx : undefined}
            slotProps={{
              primary: {
                variant: 'subtitle2',
                color: 'text.secondary',
                ...(narrow && !hasIcon ? { sx: railTextSx } : {}),
              },
            }}
          />
          {!narrow && <DisclosureChevron expanded={expanded} />}
        </ListItemButton>
      </RailTooltip>
      <Box id={contentId} hidden={!expanded}>
        <List disablePadding>
          {group.items.map((item) => (
            <NavItemRow
              key={item.id}
              item={item}
              onNavigate={onNavigate}
              narrow={narrow}
            />
          ))}
        </List>
      </Box>
    </ListItem>
  )
}

function NavList({
  entries,
  onNavigate,
  narrow = false,
  flushTop = false,
}: {
  readonly entries: readonly AppShellNavEntry[]
  readonly onNavigate?: () => void
  readonly narrow?: boolean
  readonly flushTop?: boolean
}) {
  return (
    <List sx={flushTop ? { pt: 0 } : undefined}>
      {entries.map((entry) =>
        isNavGroup(entry) ? (
          <NavGroupItem
            key={entry.id}
            group={entry}
            onNavigate={onNavigate}
            narrow={narrow}
          />
        ) : (
          <NavItemRow
            key={entry.id}
            item={entry}
            onNavigate={onNavigate}
            narrow={narrow}
          />
        ),
      )}
    </List>
  )
}

function TopbarUserMenuSlot({
  slot,
  state,
  stacked,
}: {
  readonly slot: AppShellNavSlot | undefined
  readonly state: AppShellNavSlotState
  readonly stacked: boolean
}): ReactElement | null {
  const content = renderNavSlot(slot, state)
  if (!hasSlotContent(content)) {
    return null
  }
  return (
    <Box
      sx={{
        display: 'flex',
        alignItems: 'center',
        ...(stacked ? { flexBasis: '100%' } : {}),
      }}
    >
      {content}
    </Box>
  )
}

/** The responsive app-chrome shell: fixed head, navigation drawer and main content region. */
export function AppShell({
  shellLayout = 'topbar',
  navItems,
  header,
  headerActions,
  userMenu,
  banner,
  navLabel,
  navHeader,
  navFooter,
  children,
  mobileOpen: mobileOpenProp,
  onMobileOpenChange,
  sidebarCollapsed = false,
  onSidebarCollapsedChange,
  sidebarWidth = DEFAULT_SIDEBAR_WIDTH,
  sx,
}: AppShellProps) {
  const { t } = useLayoutKitTranslation()
  const theme = useTheme()
  const isDesktop = useMediaQuery(theme.breakpoints.up('md'))
  const mainContentId = useId()

  const sidebarLayout = shellLayout === 'sidebar'

  // CSS max() keeps the theme's own spacing unit; parsing to a number would pin every theme to px.
  const collapsedSidebarWidth = `max(${theme.spacing(
    COLLAPSED_SIDEBAR_SPACING_UNITS,
  )}, ${COLLAPSED_SIDEBAR_MIN_WIDTH}px)`

  const resolvedNavLabel = navLabel ?? t('appShell.navLabel')

  const collapseToggleEnabled = onSidebarCollapsedChange !== undefined
  const desktopCollapsed = isDesktop && sidebarCollapsed

  const [uncontrolledOpen, setUncontrolledOpen] = useState(false)
  const isControlled = mobileOpenProp !== undefined
  const mobileOpen = isControlled ? mobileOpenProp : uncontrolledOpen

  const setMobileOpen = (next: boolean) => {
    if (!isControlled) {
      setUncontrolledOpen(next)
    }
    onMobileOpenChange?.(next)
  }

  const permanentSlotState: AppShellNavSlotState = {
    narrow: sidebarCollapsed,
    closeDrawer: () => undefined,
  }
  const temporarySlotState: AppShellNavSlotState = {
    narrow: false,
    closeDrawer: () => {
      if (isControlled) {
        onMobileOpenChange?.(false)
      } else {
        setUncontrolledOpen(false)
      }
    },
  }

  // Leaving the temporary variant resets the mobile open state; a controlled host is told through onMobileOpenChange.
  useEffect(() => {
    if (isDesktop && mobileOpen) {
      if (isControlled) {
        onMobileOpenChange?.(false)
      } else {
        setUncontrolledOpen(false)
      }
    }
  }, [isDesktop, isControlled, mobileOpen, onMobileOpenChange])

  const hasBanner = hasSlotContent(banner)

  // A desktop sidebar shell with no banner has no fixed head, hence no spacers (a theme-height hole otherwise).
  const hasFixedHead = !sidebarLayout || hasBanner || !isDesktop

  // One ResizeObserver on the fixed head keeps every spacer at its rendered height.
  const [headHeight, setHeadHeight] = useState<number | null>(null)
  const headRef = useRef<HTMLDivElement | null>(null)
  useEffect(() => {
    setHeadHeight(null)
  }, [shellLayout, isDesktop])
  useEffect(() => {
    if (typeof ResizeObserver === 'undefined') {
      return undefined
    }
    const head = headRef.current
    if (head === null) {
      return undefined
    }
    const observer = new ResizeObserver((entries) => {
      for (const entry of entries) {
        if (entry.target === head) {
          setHeadHeight(Math.round(entry.contentRect.height))
        }
      }
    })
    observer.observe(head)
    return () => observer.disconnect()
  }, [hasFixedHead, shellLayout, isDesktop])

  const mainContentRef = useRef<HTMLDivElement | null>(null)

  const focusMainContent = (): void => {
    mainContentRef.current?.focus()
  }

  const navContent = (
    onNavigate?: () => void,
    narrow = false,
    flushTop = false,
  ) => (
    <Box
      component="nav"
      aria-label={resolvedNavLabel}
      sx={{
        width: '100%',
        boxSizing: 'border-box',
        flex: '1 1 auto',
        minHeight: 0,
        overflowY: 'auto',
        // Rows tint with the primary accent rather than MUI's neutral wash, which turns grey on the shell surface.
        '& .MuiListItemButton-root:hover': {
          backgroundColor: (theme) => alpha(theme.palette.primary.main, 0.08),
        },
        '& .MuiListItemButton-root.Mui-selected': {
          backgroundColor: (theme) => alpha(theme.palette.primary.main, 0.14),
        },
        '& .MuiListItemButton-root.Mui-selected:hover': {
          backgroundColor: (theme) => alpha(theme.palette.primary.main, 0.18),
        },
      }}
    >
      <NavList
        entries={navItems}
        onNavigate={onNavigate}
        narrow={narrow}
        flushTop={flushTop}
      />
    </Box>
  )

  // The measured height is an inline style, never sx: the theme toolbar mixin's breakpoint min-height would override an sx height.
  const spacerMinHeight = headHeight
  const spacer = hasFixedHead ? (
    <Toolbar
      style={
        spacerMinHeight !== null ? { minHeight: spacerMinHeight } : undefined
      }
    />
  ) : null

  const mobileNavToggle = !isDesktop ? (
    <IconButton
      color="inherit"
      edge="start"
      aria-label={mobileOpen ? t('appShell.closeNav') : t('appShell.openNav')}
      aria-expanded={mobileOpen}
      onClick={() => setMobileOpen(!mobileOpen)}
      sx={{ marginRight: sidebarLayout ? 2 : 1 }}
    >
      {mobileOpen ? <CloseIcon /> : <MenuIcon />}
    </IconButton>
  ) : null

  const collapseToggleLabel = sidebarCollapsed
    ? t('appShell.expandNav')
    : t('appShell.collapseNav')
  const toggleCollapse = () => {
    onSidebarCollapsedChange?.(!sidebarCollapsed)
  }

  const sidebarTopRow =
    hasSlotContent(header) || collapseToggleEnabled ? (
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: sidebarCollapsed ? 0 : 1,
          flexShrink: 0,
          ...(sidebarCollapsed
            ?
              { px: `${RAIL_ROW_INSET_PX}px`, justifyContent: 'center' }
            : { pl: 2.5, pr: 2 }),
          minHeight: 64,
          py: 1,
        }}
      >
        <Box
          sx={{
            ...(sidebarCollapsed
              ? { flexGrow: 0, flexShrink: 0, width: 0 }
              : { flexGrow: 1 }),
            display: 'flex',
            alignItems: 'center',
            minWidth: 0,
            overflow: 'hidden',
          }}
        >
          {header}
        </Box>
        {collapseToggleEnabled && (
          <IconButton
            color="inherit"
            aria-label={collapseToggleLabel}
            aria-expanded={!sidebarCollapsed}
            onClick={toggleCollapse}
            sx={{ flexShrink: 0 }}
          >
            {sidebarCollapsed ? <ChevronRightIcon /> : <ChevronLeftIcon />}
          </IconButton>
        )}
      </Box>
    ) : null

  const hasSidebarBottomRow =
    hasSlotContent(headerActions) || hasSlotContent(userMenu)
  const sidebarBottomRow = (state: AppShellNavSlotState) =>
    hasSidebarBottomRow ? (
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          flexWrap: 'wrap',
          gap: 1,
          flexShrink: 0,
          px: state.narrow ? `${RAIL_ROW_INSET_PX}px` : 2,
          py: 1,
          ...(state.narrow
            ? { flexDirection: 'column', justifyContent: 'center' }
            : {}),
        }}
      >
        {renderNavSlot(headerActions, state)}
        {renderNavSlot(userMenu, state)}
      </Box>
    ) : null

  // The drawer paper is one flex column with a named complementary landmark; the nav landmark stays nested.
  const drawerColumn = (contents: ReactNode): ReactNode => (
    <Box
      component="aside"
      aria-label={t('appShell.sidebarRegionLabel')}
      sx={{
        display: 'flex',
        flexDirection: 'column',
        width: '100%',
        height: '100%',
        minHeight: 0,
      }}
    >
      {contents}
    </Box>
  )

  return (
    <Box
      sx={[
        { display: 'flex', minHeight: '100vh' },
        ...(Array.isArray(sx) ? sx : sx ? [sx] : []),
      ]}
    >
      <Link
        href={`#${mainContentId}`}
        onClick={(event) => {
          event.preventDefault()
          focusMainContent()
        }}
        sx={{
          position: 'absolute',
          width: 1,
          height: 1,
          padding: 0,
          margin: -1,
          overflow: 'hidden',
          whiteSpace: 'nowrap',
          clipPath: 'inset(50%)',
          color: 'inherit',
          '&:focus': {
            position: 'fixed',
            top: 8,
            left: 8,
            width: 'auto',
            height: 'auto',
            margin: 0,
            padding: 1,
            overflow: 'visible',
            whiteSpace: 'normal',
            clipPath: 'none',
            zIndex: theme.zIndex.tooltip,
            backgroundColor: 'background.paper',
            color: 'text.primary',
          },
        }}
      >
        {t('appShell.skipToContent')}
      </Link>
      {hasFixedHead && (
        <Box
          ref={headRef}
          sx={{
            position: 'fixed',
            top: 0,
            left: 0,
            right: 0,
            zIndex: theme.zIndex.drawer + 1,
            display: 'flex',
            flexDirection: 'column',
          }}
        >
          {!sidebarLayout && (
            // The theme's MuiAppBar mapping paints the fill and divider, so a surface token override reaches the bar.
            <AppBar
              position="static"
              color="inherit"
              elevation={0}
              sx={{ color: 'text.primary' }}
            >
              <Toolbar
                sx={{
                  flexWrap: 'wrap',
                  rowGap: 1,
                  ...(isDesktop ? {} : { px: 1 }),
                }}
              >
                {mobileNavToggle}
                {isDesktop && collapseToggleEnabled && (
                  <IconButton
                    color="inherit"
                    edge="start"
                    aria-label={collapseToggleLabel}
                    aria-expanded={!sidebarCollapsed}
                    onClick={toggleCollapse}
                    sx={{ marginRight: 2 }}
                  >
                    {sidebarCollapsed ? (
                      <ChevronRightIcon />
                    ) : (
                      <ChevronLeftIcon />
                    )}
                  </IconButton>
                )}
                <Box
                  sx={{
                    ...(isDesktop
                      ? { flexGrow: 1 }
                      : { flex: '1 1 0', overflow: 'hidden' }),
                    display: 'flex',
                    alignItems: 'center',
                    minWidth: 0,
                  }}
                >
                  {header}
                </Box>
                {headerActions !== undefined && (
                  <Box
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      flexWrap: 'wrap',
                      gap: isDesktop ? 1 : 0.5,
                      marginRight:
                        userMenu !== undefined
                          ? isDesktop
                            ? 2
                            : 0.5
                          : 0,
                    }}
                  >
                    {renderNavSlot(headerActions, temporarySlotState)}
                  </Box>
                )}
                <TopbarUserMenuSlot
                  slot={userMenu}
                  state={temporarySlotState}
                  stacked={!isDesktop}
                />
              </Toolbar>
            </AppBar>
          )}
          {sidebarLayout && !isDesktop && (
            <AppBar
              position="static"
              color="inherit"
              elevation={0}
              sx={{ color: 'text.primary' }}
            >
              <Toolbar sx={{ flexWrap: 'wrap', rowGap: 1 }}>
                {mobileNavToggle}
                <Box
                  sx={{
                    flexGrow: 1,
                    display: 'flex',
                    alignItems: 'center',
                    minWidth: 0,
                  }}
                >
                  {header}
                </Box>
              </Toolbar>
            </AppBar>
          )}
          {hasBanner && <Box sx={{ width: '100%' }}>{banner}</Box>}
        </Box>
      )}
      <Box
        sx={{
          width: {
            md: sidebarCollapsed ? collapsedSidebarWidth : sidebarWidth,
          },
          flexShrink: { md: 0 },
        }}
      >
        {isDesktop ? (
          <Drawer
            variant="permanent"
            open
            sx={{
              '& .MuiDrawer-paper': {
                boxSizing: 'border-box',
                width: sidebarCollapsed ? collapsedSidebarWidth : sidebarWidth,
                overflow: 'hidden',
                ...(sidebarCollapsed ? { overflowX: 'hidden' } : {}),
              },
            }}
          >
            {drawerColumn(
              <>
                {spacer}
                {sidebarLayout && sidebarTopRow}
                {sidebarLayout && sidebarTopRow !== null ? <Divider /> : null}
                {renderNavSlot(navHeader, permanentSlotState)}
                {navContent(undefined, sidebarCollapsed, sidebarLayout)}
                {renderNavSlot(navFooter, permanentSlotState)}
                {sidebarLayout && sidebarBottomRow(permanentSlotState)}
              </>,
            )}
          </Drawer>
        ) : (
          <Drawer
            variant="temporary"
            open={mobileOpen}
            onClose={() => setMobileOpen(false)}
            ModalProps={{ keepMounted: true }}
            // The open temporary drawer is a modal dialog, so its paper needs a name; it reuses the nav label.
            slotProps={{ paper: { 'aria-label': resolvedNavLabel } }}
            sx={{
              '& .MuiDrawer-paper': {
                boxSizing: 'border-box',
                // Capped so a very narrow viewport never gets a near-full-screen drawer.
                width: `min(${sidebarWidth}px, 85vw)`,
                overflow: 'hidden',
              },
            }}
          >
            {drawerColumn(
              <>
                {spacer}
                {renderNavSlot(navHeader, temporarySlotState)}
                {navContent(() => {
                  // Only an uncontrolled temporary drawer closes itself on navigation.
                  if (!isControlled) {
                    setUncontrolledOpen(false)
                  }
                })}
                {renderNavSlot(navFooter, temporarySlotState)}
                {sidebarLayout && sidebarBottomRow(temporarySlotState)}
              </>,
            )}
          </Drawer>
        )}
      </Box>
      <Box
        component="main"
        id={mainContentId}
        ref={mainContentRef}
        // tabIndex={-1} makes main a valid focus target without adding a Tab stop.
        tabIndex={-1}
        sx={{
          flexGrow: 1,
          minWidth: 0,
          width: {
            md: desktopCollapsed
              ? `calc(100% - ${collapsedSidebarWidth})`
              : `calc(100% - ${sidebarWidth}px)`,
          },
        }}
      >
        {spacer}
        {children}
      </Box>
    </Box>
  )
}
