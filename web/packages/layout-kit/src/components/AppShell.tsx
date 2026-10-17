/**
 * AppShell: the responsive app-chrome shell.
 *
 * Renders a fixed `AppBar` (the `header`/banner landmark), a responsive
 * navigation drawer (a `nav` landmark: permanent at `md`+, temporary and
 * overlaid below `md`), and a `main` content landmark. The desktop/mobile
 * split is a JS boolean from `useMediaQuery(theme.breakpoints.up('md'))`
 * -- no new breakpoint is introduced, and the two Drawer variants are
 * chosen in JS rather than toggled through CSS-only breakpoint sx, so a
 * host that queries "is the desktop drawer showing" gets one deterministic
 * answer instead of two DOM nodes racing on paint.
 *
 * AppShell carries no navigation or auth semantics: `navItems` is fully
 * host-computed (including which item is `selected` -- AppShell never
 * does path matching, since different hosts use different routers) and
 * the mobile drawer's open state is the one interaction-local exception
 * (uncontrolled by default, like ConfirmDialog's double-confirm arm),
 * promotable to fully controlled via `mobileOpen` / `onMobileOpenChange`.
 *
 * Slots (`header`, `headerActions`, `userMenu`, `children`) render
 * exactly the host content passed in and nothing implicit -- an absent
 * slot renders nothing, never a placeholder.
 *
 * The navigation has two optional levels beyond a flat list, both purely
 * additive over the original `AppShellNavItem[]`. A `navItems` element
 * may be an `AppShellNavGroup` -- a titled, collapsible section whose
 * heading is itself the disclosure control (`aria-expanded` /
 * `aria-controls`) and whose entries sit beneath it -- and a plain
 * `AppShellNavItem` may carry `children`, turning it into an expandable
 * parent whose child entries render indented, each a real link or
 * button. Both reuse the leaf activation and the selected/`aria-current`
 * convention unchanged, so a host that passes no groups and no children
 * renders exactly the flat list it did before. A collapsed group or
 * parent keeps its entries in the DOM under the `hidden` attribute, so
 * assistive tech (and Testing Library's role queries) do not reach them
 * while the section is closed.
 *
 * Narrow-viewport protection (additive, CSS-only, no prop changes):
 * the Toolbar row and the `headerActions` group both carry a responsive
 * `flexWrap: 'wrap'` so a host that supplies several header actions
 * wraps onto a second line under real overflow pressure instead of
 * being squeezed or clipped -- AppShell still renders only what the
 * host gives it (no auto-collapsing overflow menu is invented here,
 * consistent with this package's host-injected-everything design). The
 * desktop sidebar's collapse is the opposite kind of change: an
 * explicit, host-controlled pair (`sidebarCollapsed` /
 * `onSidebarCollapsedChange`) that a host opts into, never an implicit
 * responsive behavior -- omitting it renders the always-open permanent
 * drawer unchanged, and the toggle button appears only when the host
 * supplies the change callback. Collapsing narrows the permanent drawer
 * to the icon-only rail the layout design names (a fixed width derived
 * from the theme spacing base, not a zero-width hide): the drawer stays
 * mounted as a real `nav` landmark, each entry keeps its icon with its
 * text clipped (still the entry's accessible name, still readable by a
 * screen reader) and the clipped label offered to a sighted pointer user
 * as a per-entry tooltip; an entry that carries no icon keeps its label
 * visible (clipped to the rail) so the rail is never a blank cell -- see
 * railTextItemSx, and railTextSx for the clip itself, which is declared
 * on the label's text element so the label truncates with an ellipsis
 * instead of being cut mid-glyph by the button. A group heading follows
 * the same rule: with an icon it
 * becomes an icon row (its label clipped but still the heading's
 * accessible name and tooltip), and without one it keeps its clipped
 * label. The rail therefore stays navigable instead of
 * disappearing. The
 * mobile (temporary) Drawer's paper width is capped to
 * `min(sidebarWidth, 85vw)`, and the nav content rendered inside it
 * fills that paper (`width: 100%`) rather than re-declaring
 * `sidebarWidth` itself, so a very narrow viewport (~320px and below)
 * never gets a near-full-screen or overflowing drawer; the desktop
 * (permanent) Drawer's width, and its nav content, are untouched.
 *
 * Two interaction-local states live inside AppShell, both only when
 * the host keeps the drawer uncontrolled. First, the skip link focuses
 * the `main` landmark programmatically instead of navigating to a
 * fragment: fragment navigation rewrites `location.hash`, which in a
 * hash-routed host replaces
 * the app route with the generated target id and loses the route.
 * Second, activating a nav item in the temporary drawer closes it --
 * the drawer owns its open state in uncontrolled mode, so it owns the
 * close-on-navigation transition (MUI's own close machinery clears the
 * scrim and restores focus). A controlled host keeps the existing
 * contract untouched: nav activation reports nothing and closing stays
 * the host's own next render.
 *
 * Because the header row can wrap, its rendered height is measured
 * (ResizeObserver on the banner, where the browser supports it) and
 * the three spacer placeholders -- the one inside the drawer paper and
 * the one inside `main` for each drawer variant -- derive their height
 * from that measurement rather than assuming the theme's single-row
 * toolbar height, so a wrapped header can never cover the top of the
 * content it overlays. Where no ResizeObserver exists (jsdom), the
 * spacers keep the plain theme-height Toolbar.
 */

import { useEffect, useId, useRef, useState } from 'react'
import type { MouseEvent, MouseEventHandler, ReactElement, ReactNode } from 'react'
import type { SxProps, Theme } from '@mui/material/styles'
import { useTheme } from '@mui/material/styles'
import AppBar from '@mui/material/AppBar'
import Box from '@mui/material/Box'
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
 * One navigation entry. The host owns `selected` (computed from
 * whichever router it uses) and either `href` (renders as a link) or
 * `onClick` (renders as a button) -- both may be supplied together, for
 * example to intercept a client-side router link's default navigation.
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
   * Second-level nesting: the item's own child entries, each a real route.
   * When set, the item becomes an expandable parent -- its row is a
   * disclosure toggle (`aria-expanded` / `aria-controls`) whose children
   * render as an indented list of normal navigation entries. The parent's
   * own `href` / `onClick` / `selected` are not rendered: the routes live
   * in the children, so a parent with children has no single destination
   * of its own. Omit it for a plain leaf entry.
   */
  readonly children?: readonly AppShellNavItem[]
}

/**
 * One titled, collapsible group of navigation entries: a heading that is
 * itself the disclosure control (`aria-expanded` / `aria-controls`) over
 * the `items` beneath it. Host content throughout -- the `label` is
 * already translated and the `items` are ordinary navigation entries.
 */
export interface AppShellNavGroup {
  /** Stable identity for the React key and the group's initial state. */
  readonly id: string
  /** The group's visible heading; host content, already translated. */
  readonly label: ReactNode
  /**
   * Optional leading icon; host content. In the collapsed rail it is the
   * group heading's visible content (the label stays as the heading's
   * accessible name and the tooltip), exactly as an entry's icon replaces
   * its label there.
   */
  readonly icon?: ReactNode
  /** The entries inside the group, in order. */
  readonly items: readonly AppShellNavItem[]
  /**
   * Whether the group starts collapsed. Defaults to false (expanded), so
   * a host that never sets it shows the group's entries.
   */
  readonly defaultCollapsed?: boolean
}

/** One navigation entry or grouped section, as the host declares it. */
export type AppShellNavEntry = AppShellNavItem | AppShellNavGroup

export interface AppShellProps {
  /**
   * The navigation entries rendered in the drawer, in order. Each element
   * is either a plain entry (`AppShellNavItem`) or a titled collapsible
   * group (`AppShellNavGroup`); the union widens the previous
   * `AppShellNavItem[]` shape, so an existing host that passes only plain
   * entries is unchanged.
   */
  readonly navItems: readonly AppShellNavEntry[]
  /** Start-of-AppBar content: typically a logo or product name. */
  readonly header?: ReactNode
  /** End-of-AppBar content, before `userMenu`: search, notifications, and the like. */
  readonly headerActions?: ReactNode
  /** Far end-of-AppBar content: typically an account/user menu trigger. */
  readonly userMenu?: ReactNode
  /**
   * The navigation landmark's accessible name -- and the name of the
   * temporary drawer, which opens as a modal `nav` labelled by it.
   * Already-translated host text; defaults to this package's own
   * `appShell.navLabel`. A host that mounts a shell beside another
   * shell's own navigation supplies a distinguishing name so neither
   * landmark reads as the other in the accessibility tree.
   */
  readonly navLabel?: string
  /** The content region, rendered inside the `main` landmark. */
  readonly children: ReactNode
  /**
   * Mobile drawer open state. Omit both this and `onMobileOpenChange` to
   * let AppShell manage the toggle internally (uncontrolled); pass both
   * for a fully host-controlled drawer.
   */
  readonly mobileOpen?: boolean
  /**
   * Fired whenever the mobile drawer's open state should change: the
   * toggle and the drawer's own close report here, and so does the
   * narrow-to-wide breakpoint crossing -- at md+ the drawer is
   * permanent and the mobile open state should be false.
   */
  readonly onMobileOpenChange?: (open: boolean) => void
  /**
   * Desktop sidebar collapse state (the permanent drawer at md and up).
   * Omit to keep the sidebar always open -- the previous behavior, and
   * the one every host that never opts in is byte-for-byte unaffected
   * by. Pass it (optionally with onSidebarCollapsedChange) to collapse
   * the sidebar to the icon-only rail that reclaims most of the content
   * width: the drawer stays mounted and named, every entry keeps its
   * icon with the label shown as a tooltip, and the mobile (temporary)
   * drawer is unaffected. False renders the full icon+text sidebar.
   */
  readonly sidebarCollapsed?: boolean
  /**
   * Fired when the sidebar collapse state should change. Supplying it
   * renders the desktop collapse/expand toggle button in the AppBar;
   * omitting it renders no toggle (a host that collapses its sidebar
   * programmatically reports nothing).
   */
  readonly onSidebarCollapsedChange?: (collapsed: boolean) => void
  /** Drawer width in px, both variants. Defaults to 280. */
  readonly sidebarWidth?: number
  /** Extra styling merged onto the root layout box. */
  readonly sx?: SxProps<Theme>
}

const DEFAULT_SIDEBAR_WIDTH = 280

/**
 * The collapsed sidebar rail's width, in theme spacing units. The layout
 * design keeps a collapsed sidebar a fixed-width icon rail rather than
 * hiding it, and derives that width from the spacing base the token tree
 * owns (the theme's spacing base IS the spacing token unit): a standard
 * 24px list icon with the theme's default 16px (spacing 2) inset on each
 * side is 24 + 2 x 16 = 56 = 7 x unit.
 */
const COLLAPSED_SIDEBAR_SPACING_UNITS = 7

/**
 * A collapsed rail entry that carries an icon: the icon is centered and
 * the label clipped (see RailTooltip). The horizontal padding is the
 * theme's single spacing step, leaving the icon horizontally centered in
 * the rail's remaining width.
 */
const railIconItemSx = { justifyContent: 'center', px: 1 } as const

/**
 * A collapsed rail entry without an icon: rather than a blank cell, the
 * entry's label stays visible, clipped to the rail's width. The layout
 * design's rail is icon-bearing by intent, but a host that collapses a
 * navigation whose entries carry no `icon` still gets identifiable,
 * operable rows -- tooltipped and, for assistive tech, named by the
 * label either way. The button's own `overflow: hidden` is only a
 * backstop; the one-line clip and its ellipsis live on the label's text
 * element -- see railTextSx.
 */
const railTextItemSx = {
  px: 1,
  overflow: 'hidden',
} as const

/**
 * The clipped label in the collapsed rail. It must be declared on the
 * text element (`ListItemText`'s primary slot), not on the button: the
 * label sits in a block below the button, and `text-overflow` applies
 * only to an element's own inline content, never to a descendant
 * block's text. Declared on the button, the ellipsis never engages and
 * the button's `overflow: hidden` cuts the label mid-glyph instead of
 * producing a readable truncation. `minWidth: 0` lets the text box
 * shrink to its column instead of holding its nowrap content width.
 */
const railTextSx = {
  minWidth: 0,
  overflow: 'hidden',
  whiteSpace: 'nowrap',
  textOverflow: 'ellipsis',
} as const

/** The rail entry's icon box: no reserved label width, itself centered. */
const railIconSx = { minWidth: 0, mr: 0, justifyContent: 'center' } as const

/**
 * The sidebar's icon-only label tooltip. In the collapsed rail each
 * entry's text is clipped (kept in the DOM, so it remains the entry's
 * accessible name), so a sighted pointer user reads the label on hover or
 * focus; everywhere else the entry's text is visible and no tooltip is
 * added, so an expanded entry is byte-for-byte what it was before the
 * rail existed.
 */
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

/** Narrows a declared entry to a grouped section. */
function isNavGroup(entry: AppShellNavEntry): entry is AppShellNavGroup {
  return 'items' in entry
}

/**
 * The chevron shown by a disclosure control, its direction reflecting the
 * expanded state. Decorative: the control's own `aria-expanded` reports
 * the state to assistive tech.
 */
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

/**
 * One navigation entry. A leaf renders as a link (`href`) or a button
 * (`onClick`); an entry carrying `children` renders as an expandable
 * parent whose child entries nest below it. `indented` shifts a nested
 * entry to the right so its level below the parent reads visually.
 */
function NavItemRow({
  item,
  onNavigate,
  indented = false,
  narrow = false,
}: {
  readonly item: AppShellNavItem
  /**
   * Fired after the entry's own onClick, when it has one, on every
   * activation. The drawer variants decide what this means: the temporary
   * drawer closes itself on it (uncontrolled), the permanent drawer
   * passes none. An expandable parent fires this only from its children --
   * expanding the parent is not navigating.
   */
  readonly onNavigate?: () => void
  /** Whether this entry sits one level below its parent. */
  readonly indented?: boolean
  /** Whether this entry renders in the collapsed icon-only rail. */
  readonly narrow?: boolean
}) {
  // Called unconditionally (hook order): only the parent branch uses them.
  const contentId = useId()
  const [expanded, setExpanded] = useState(true)
  // The indent narrows a nested entry one level, but never in the rail:
  // the rail centers every entry, so an indent would break that (and the
  // rail's own width cannot afford it).
  const indentSx = indented && !narrow ? { pl: 4 } : undefined
  const hasIcon = item.icon !== undefined
  const contentSx = narrow
    ? hasIcon
      ? railIconItemSx
      : railTextItemSx
    : indentSx
  const iconSx = narrow && hasIcon ? railIconSx : undefined
  // In the rail an entry WITH an icon hides its label (the icon speaks,
  // the tooltip names it); an entry WITHOUT one keeps the label visible,
  // so the rail is never a blank cell -- see railTextItemSx, and see
  // railTextSx for why the clipping declarations go on the text element
  // rather than on the button.
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

/**
 * One titled collapsible group: a heading that is itself the disclosure
 * control over the group's entries. The entries stay ordinary navigation
 * entries (each reusing the same link/button activation and
 * selected/`aria-current` convention); `defaultCollapsed` seeds the
 * initial state, and every later toggle is this control's own.
 */
function NavGroupItem({
  group,
  onNavigate,
  narrow = false,
}: {
  readonly group: AppShellNavGroup
  readonly onNavigate?: () => void
  /** Whether the group renders in the collapsed icon-only rail. */
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
          // In the rail a group heading with an icon becomes an icon row,
          // like an entry's; without one it keeps the text fallback (its
          // heading stays visible, clipped) rather than rendering a blank
          // cell.
          sx={narrow ? (hasIcon ? railIconItemSx : railTextItemSx) : undefined}
        >
          {group.icon !== undefined && (
            <ListItemIcon sx={narrow && hasIcon ? railIconSx : undefined}>
              {group.icon}
            </ListItemIcon>
          )}
          <ListItemText
            primary={group.label}
            // With an icon the rail hides the label visually but keeps it
            // as the heading's accessible name, matching an entry; without
            // one the heading stays visible and takes the same text-element
            // clip an icon-less entry does (see railTextSx).
            sx={narrow && hasIcon ? visuallyHiddenSx : undefined}
            slotProps={{
              primary: {
                variant: 'subtitle2',
                color: 'text.secondary',
                ...(narrow && !hasIcon ? { sx: railTextSx } : {}),
              },
            }}
          />
          {/* The rail has no room for the chevron beside the icon; the
              button's aria-expanded still reports the disclosure state. */}
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
}: {
  readonly entries: readonly AppShellNavEntry[]
  /**
   * Fired after a leaf entry's own onClick, when it has one, on every
   * activation. The drawer variants decide what this means: the temporary
   * drawer closes itself on it (uncontrolled), the permanent drawer
   * passes none.
   */
  readonly onNavigate?: () => void
  /** Whether the list renders in the collapsed icon-only rail. */
  readonly narrow?: boolean
}) {
  return (
    <List>
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

/**
 * The responsive app-chrome shell: fixed header, responsive nav drawer,
 * `main` content region. See the module doc comment for the full
 * contract.
 */
export function AppShell({
  navItems,
  header,
  headerActions,
  userMenu,
  navLabel,
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

  // The collapsed rail's fixed width, derived from the theme spacing base
  // (the spacing token unit) -- see COLLAPSED_SIDEBAR_SPACING_UNITS.
  const collapsedSidebarWidth = theme.spacing(COLLAPSED_SIDEBAR_SPACING_UNITS)

  // The navigation landmark's name is host-suppliable (navLabel) so a
  // host mounting this frame beside a second shell's navigation can keep
  // the two landmarks distinguishable; absent one, the package's own
  // bilingual label names it, the behavior every host that never sets
  // navLabel already gets.
  const resolvedNavLabel = navLabel ?? t('appShell.navLabel')

  // The desktop sidebar collapse is entirely host-controlled: the state
  // is the `sidebarCollapsed` prop, and the toggle reports the flipped
  // value through onSidebarCollapsedChange. Supplying that callback is
  // what opts a host into the affordance (and renders the toggle);
  // omitting it renders exactly the always-open permanent drawer every
  // existing host already gets. The collapse is honored only at md and
  // up -- the mobile (temporary) drawer keeps its own open state.
  const collapseToggleEnabled = onSidebarCollapsedChange !== undefined
  const desktopCollapsed = isDesktop && sidebarCollapsed

  // Interaction-only state, the same carve-out ConfirmDialog's double-
  // confirm arm relies on: promoted to fully controlled the moment the
  // host supplies mobileOpen, otherwise AppShell tracks it itself.
  const [uncontrolledOpen, setUncontrolledOpen] = useState(false)
  const isControlled = mobileOpenProp !== undefined
  const mobileOpen = isControlled ? mobileOpenProp : uncontrolledOpen

  const setMobileOpen = (next: boolean) => {
    if (!isControlled) {
      setUncontrolledOpen(next)
    }
    onMobileOpenChange?.(next)
  }

  // The mobile drawer's open state belongs to the temporary variant: a
  // portrait-open drawer that grows into the permanent (always-open)
  // variant at md+ must not come back open when the viewport narrows
  // again, so leaving temporary resets it. The uncontrolled half clears
  // its own state; a controlled host owns the state but not the
  // breakpoint -- the desktop/mobile split is AppShell's own
  // useMediaQuery knowledge -- so the crossing is reported through the
  // same onMobileOpenChange channel the host already reads: when the
  // variant leaves temporary, the mobile drawer's open state should be
  // false. Only an open drawer is reset, so a host already closed is
  // never handed a meaningless callback.
  useEffect(() => {
    if (isDesktop && mobileOpen) {
      if (isControlled) {
        onMobileOpenChange?.(false)
      } else {
        setUncontrolledOpen(false)
      }
    }
  }, [isDesktop, isControlled, mobileOpen, onMobileOpenChange])

  // The header row wraps under real overflow pressure, so its rendered
  // height is measured and every spacer placeholder derives its height
  // from the measurement (see the "spacer" note at the placeholders).
  // Without a ResizeObserver (jsdom) the measurement never arrives and
  // the spacers keep the theme toolbar height, exactly as before.
  const [headerHeight, setHeaderHeight] = useState<number | null>(null)
  const appBarRef = useRef<HTMLDivElement | null>(null)
  useEffect(() => {
    const appBar = appBarRef.current
    if (appBar === null || typeof ResizeObserver === 'undefined') {
      return undefined
    }
    const observer = new ResizeObserver((entries) => {
      const entry = entries[entries.length - 1]
      if (entry !== undefined) {
        setHeaderHeight(Math.round(entry.contentRect.height))
      }
    })
    observer.observe(appBar)
    return () => observer.disconnect()
  }, [])

  // The skip link's activation target. Kept in a ref rather than
  // resolved by id so the handler never touches the document: the
  // element the link labels is the element it focuses.
  const mainContentRef = useRef<HTMLDivElement | null>(null)

  const focusMainContent = (): void => {
    mainContentRef.current?.focus()
  }

  // The nav content mounts into either Drawer paper. It fills whichever
  // paper it lands in (`width: 100%`) rather than re-declaring
  // sidebarWidth itself -- the permanent (desktop) paper is exactly
  // sidebarWidth, but the temporary (mobile) paper is capped to
  // `min(sidebarWidth, 85vw)` below, and a second, independent
  // sidebarWidth here would silently outgrow that cap and bleed past
  // the paper's right edge (Drawer's paper is `position: fixed` with no
  // overflow-x, so the overflow is not clipped, just visible). The
  // permanent variant passes no onNavigate (nothing to close); the
  // temporary variant passes the close transition -- see the module doc
  // comment for who owns it in which mode.
  const navContent = (onNavigate?: () => void, narrow = false) => (
    <Box component="nav" aria-label={resolvedNavLabel} sx={{ width: '100%', boxSizing: 'border-box' }}>
      <NavList entries={navItems} onNavigate={onNavigate} narrow={narrow} />
    </Box>
  )

  // The three offset placeholders that keep content clear of the fixed
  // AppBar: one inside the drawer paper and one inside `main`, for
  // whichever drawer variant is mounted. When the header height has
  // been measured, each spacer carries that exact height; before any
  // measurement exists it is a plain theme-height Toolbar (the
  // single-row case, which is also the whole jsdom case).
  const spacer = (
    <Toolbar sx={headerHeight !== null ? { minHeight: headerHeight } : undefined} />
  )

  return (
    <Box
      sx={[
        { display: 'flex', minHeight: '100vh' },
        ...(Array.isArray(sx) ? sx : sx ? [sx] : []),
      ]}
    >
      <AppBar position="fixed" ref={appBarRef} sx={{ zIndex: theme.zIndex.drawer + 1 }}>
        {/* Visually hidden until focused; the first focusable element in
            the shell, so it must live before the nav toggle in DOM order.
            It stays inside the header/banner landmark so it never counts
            as page content outside a landmark. Activating it focuses the
            main landmark directly instead of navigating to the fragment:
            fragment navigation rewrites location.hash, and in a
            hash-routed host the fragment IS the route -- a skip link
            that navigates would
            replace the app route with the generated target id. The
            keyboard path is unchanged: Tab reaches the link, Enter
            activates it, and the focus visibly moves into main (browsers
            scroll a focused element into view, matching what fragment
            navigation would have scrolled to). */}
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
        <Toolbar sx={{ flexWrap: 'wrap', rowGap: 1 }}>
          {!isDesktop && (
            <IconButton
              color="inherit"
              edge="start"
              aria-label={mobileOpen ? t('appShell.closeNav') : t('appShell.openNav')}
              aria-expanded={mobileOpen}
              onClick={() => setMobileOpen(!mobileOpen)}
              sx={{ marginRight: 2 }}
            >
              {mobileOpen ? <CloseIcon /> : <MenuIcon />}
            </IconButton>
          )}
          {isDesktop && collapseToggleEnabled && (
            <IconButton
              color="inherit"
              edge="start"
              aria-label={
                sidebarCollapsed
                  ? t('appShell.expandNav')
                  : t('appShell.collapseNav')
              }
              aria-expanded={!sidebarCollapsed}
              onClick={() =>
                onSidebarCollapsedChange?.(!sidebarCollapsed)
              }
              sx={{ marginRight: 2 }}
            >
              {sidebarCollapsed ? <ChevronRightIcon /> : <ChevronLeftIcon />}
            </IconButton>
          )}
          <Box sx={{ flexGrow: 1, display: 'flex', alignItems: 'center', minWidth: 0 }}>
            {header}
          </Box>
          {headerActions !== undefined && (
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                flexWrap: 'wrap',
                gap: 1,
                marginRight: userMenu !== undefined ? 2 : 0,
              }}
            >
              {headerActions}
            </Box>
          )}
          {userMenu}
        </Toolbar>
      </AppBar>
      <Box
        sx={{
          width: {
            md: sidebarCollapsed ? collapsedSidebarWidth : sidebarWidth,
          },
          flexShrink: { md: 0 },
        }}
      >
        {isDesktop ? (
          // The permanent drawer stays mounted whether expanded (icon +
          // text) or collapsed (the icon-only rail): collapsing narrows
          // its paper to the rail width and clips each entry's label, it
          // never unmounts the drawer -- the `nav` landmark and every
          // entry's accessible name survive the collapse.
          <Drawer
            variant="permanent"
            open
            sx={{
              '& .MuiDrawer-paper': {
                boxSizing: 'border-box',
                width: sidebarCollapsed ? collapsedSidebarWidth : sidebarWidth,
                ...(sidebarCollapsed ? { overflowX: 'hidden' } : {}),
              },
            }}
          >
            {spacer}
            {navContent(undefined, sidebarCollapsed)}
          </Drawer>
        ) : (
          <Drawer
            variant="temporary"
            open={mobileOpen}
            onClose={() => setMobileOpen(false)}
            ModalProps={{ keepMounted: true }}
            // The open temporary drawer is a real modal: MUI gives its
            // paper role="dialog" with aria-modal, and axe's
            // aria-dialog-name rule fails an unnamed dialog -- measured
            // on the OPEN state by the suite's open-drawer axe scan,
            // which only a named drawer can pass. The drawer IS the
            // navigation, so its dialog name reuses the nav landmark's
            // own label (host-supplied or the package default) rather
            // than inventing a fourth appShell string.
            slotProps={{ paper: { 'aria-label': resolvedNavLabel } }}
            sx={{
              '& .MuiDrawer-paper': {
                boxSizing: 'border-box',
                // Capped to the viewport so a very narrow screen (~320px
                // and below) never gets a near-full-screen or
                // overflowing drawer; the permanent (desktop) Drawer
                // above is untouched and keeps the plain sidebarWidth.
                width: `min(${sidebarWidth}px, 85vw)`,
              },
            }}
          >
            {spacer}
            {navContent(() => {
              // The temporary drawer owns the close-on-navigation
              // transition when it owns the open state. In controlled
              // mode this fires nothing and closes nothing: the host
              // observes the navigation itself and closes through its
              // own next render, the unchanged contract.
              if (!isControlled) {
                setUncontrolledOpen(false)
              }
            })}
          </Drawer>
        )}
      </Box>
      <Box
        component="main"
        id={mainContentId}
        ref={mainContentRef}
        // A bare `main` has no tabindex, so it is not natively focusable
        // and neither native fragment navigation nor the skip link's
        // programmatic focus could land on it. tabIndex={-1} makes it a
        // valid, non-tab-order focus target without adding a Tab stop.
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
