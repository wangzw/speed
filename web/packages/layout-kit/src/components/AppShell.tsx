/**
 * AppShell: the responsive app-chrome shell.
 *
 * Renders a fixed `AppBar` (the `header` landmark), a responsive
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
 * Two shell layouts share that frame. `shellLayout: 'topbar'` -- the
 * default -- is the arrangement above: one fixed app bar carrying
 * `header`, `headerActions` and `userMenu`. `shellLayout: 'sidebar'`
 * renders no app bar at `md` and up: the brand and the desktop collapse
 * toggle move to a row at the top of the drawer paper, and
 * `headerActions`/`userMenu` to a row pinned at its bottom. In both
 * layouts the drawer paper is a fixed flex column whose `nav` takes the
 * remaining height and scrolls itself, with `navFooter` at the paper's
 * bottom edge -- in the topbar layout that edge is the account entry's
 * only home. Below `md` a sidebar shell keeps
 * a minimal top bar carrying only the drawer trigger and `header` (the
 * banner landmark); `headerActions`/`userMenu` stay in the temporary
 * drawer's bottom row, and the drawer renders no top row (the brand is
 * already in the minimal bar, so it appears once per page). A desktop
 * sidebar shell with no banner renders no fixed head at all -- hence no
 * banner landmark -- and no spacers. The sidebar's drawer paper column
 * carries a named complementary landmark around its chrome: the brand
 * row, the host slots and the bottom action row are page content too, and
 * without a landmark around them a page-level axe scan rejects the
 * column (`region`); the `nav` landmark stays nested inside it.
 * `navHeader`/`navFooter` remain siblings of the `nav` landmark in both
 * layouts; the drawer's `nav` is the paper's only scrolling region, and
 * the content region scrolls independently.
 *
 * Slots (`header`, `headerActions`, `userMenu`, `banner`, `navHeader`,
 * `navFooter`, `children`)
 * render exactly the host content passed in and nothing implicit -- an
 * absent slot renders nothing, never a placeholder.
 *
 * `navHeader` and `navFooter` are the drawer paper's own leading and
 * trailing content: each renders immediately before or after the
 * navigation landmark, inside the drawer paper, in both the permanent and
 * the temporary variant. They are siblings of the landmark, not part of
 * it, so neither the `nav` landmark nor its accessible name changes. Each
 * slot is either host content or a render function receiving the mounted
 * drawer's state (`narrow`, `closeDrawer`): `narrow` is true only when the
 * mounted drawer is the collapsed rail, and `closeDrawer` closes a
 * temporary uncontrolled drawer, reports `false` through
 * `onMobileOpenChange` when the drawer is controlled, and is a no-op for
 * the permanent drawer. An absent, `null` or `false` slot renders nothing
 * and adds no height. `headerActions` and `userMenu` take the same state,
 * with `narrow` true for the controls the sidebar layout puts in the
 * collapsed rail's bottom row: a host control too wide for the rail
 * renders its icon form there rather than being clipped by the paper.
 *
 * The `banner` slot is part of the shell's fixed head: the head is one
 * fixed container pinned to the top of the viewport, holding the AppBar
 * first (in the topbar layout, or the minimal top bar of a narrow
 * sidebar shell) and the banner immediately after it in normal flow. A
 * desktop sidebar shell has no app bar, so the banner is the head's only
 * child and sits at the very top. The whole head stays in the viewport while the page scrolls, the
 * banner sits below the header visually and in DOM order, and the
 * spacers inside `main` and the drawer papers reserve the fixed head's
 * measured height, so neither the header nor the navigation covers
 * content. Measuring the one container -- rather than the AppBar and the
 * banner separately and deriving an offset from one of them -- keeps a
 * wrapped header and a tall banner in lockstep with the spacers with no
 * mutually recursive offsets. The slot container carries no padding,
 * border or minimum height: its height is exactly what the host node
 * renders, so an empty host node adds nothing to the head. The fixed
 * head sits above page content and the drawer papers but below modal
 * panels and dialogs. The banner is visible at every breakpoint
 * (wrapping is the host node's own concern) and belongs to the shell
 * rather than to `children`, so a host that swaps the content on a route
 * change never unmounts it. The slot adds no landmark and no live region
 * -- it is not the `banner` landmark, which is the app bar (the minimal
 * top bar in a narrow sidebar shell, and absent entirely on a desktop
 * sidebar shell without a banner) -- so the shell's landmark structure
 * (header where an app bar renders, nav, main) is unchanged. Absent,
 * `null` or `false` renders no slot container and adds no height.
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
 * Because the header row can wrap and the banner has its own height, the
 * fixed head container's rendered height is measured (ResizeObserver,
 * where the browser supports it) and the three spacer placeholders --
 * the one inside the drawer paper and the one inside `main` for each
 * drawer variant -- derive their height from that measurement rather
 * than assuming the theme's single-row toolbar height, so the head can
 * never cover the top of the content it overlays. Where no
 * ResizeObserver exists (jsdom), the spacers keep the plain theme-height
 * Toolbar and the banner contributes no measured height.
 */

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

/**
 * The state a `navHeader` or `navFooter` render function receives: the
 * mounted drawer's shape and its close channel.
 */
export interface AppShellNavSlotState {
  /**
   * Whether the mounted drawer is the collapsed icon-only rail. Always
   * false in the temporary (mobile) drawer, which never collapses.
   */
  readonly narrow: boolean
  /**
   * Closes a temporary (mobile) drawer: it closes itself in uncontrolled
   * mode and reports `false` through `onMobileOpenChange` in controlled
   * mode. A no-op for the permanent (desktop) drawer, which is always
   * open.
   */
  readonly closeDrawer: () => void
}

/**
 * A drawer slot: host content, or a render function given the mounted
 * drawer's state. A render function lets a host compose a compact header
 * in the collapsed rail and a footer action that closes the mobile
 * drawer.
 */
export type AppShellNavSlot =
  | ReactNode
  | ((state: AppShellNavSlotState) => ReactNode)

/**
 * The shell's chrome arrangement. `'topbar'` keeps every piece of chrome
 * in the fixed app bar; `'sidebar'` moves the brand, the header actions
 * and the user menu into the drawer paper, leaving the content region the
 * full width under a minimal top bar on narrow viewports.
 */
export type AppShellShellLayout = 'topbar' | 'sidebar'

export interface AppShellProps {
  /**
   * The shell's chrome arrangement, chosen at assembly time. `'topbar'`
   * (the default) is the arrangement every host had before this prop
   * existed. `'sidebar'` is the region mapping described in the module
   * doc comment: at `md` and up the brand and the desktop collapse
   * toggle sit in a row at the top of the drawer paper and
   * `headerActions`/`userMenu` in a row pinned at its bottom; below `md`
   * a minimal top bar carries only the drawer trigger and `header`.
   */
  readonly shellLayout?: AppShellShellLayout
  /**
   * The navigation entries rendered in the drawer, in order. Each element
   * is either a plain entry (`AppShellNavItem`) or a titled collapsible
   * group (`AppShellNavGroup`); the union widens the previous
   * `AppShellNavItem[]` shape, so an existing host that passes only plain
   * entries is unchanged.
   */
  readonly navItems: readonly AppShellNavEntry[]
  /**
   * Start-of-AppBar content: typically a logo or product name. The
   * sidebar layout moves it to the drawer paper's top row (and, below
   * md, to the minimal top bar).
   */
  readonly header?: ReactNode
  /**
   * End-of-AppBar content, before `userMenu`: search, notifications, and
   * the like. The sidebar layout moves it to the drawer paper's bottom
   * row. Takes the same render-function state as `navHeader`, so a host
   * control that cannot fit the collapsed rail's width renders an icon
   * form there instead of being clipped.
   */
  readonly headerActions?: AppShellNavSlot
  /**
   * Far end-of-AppBar content: typically an account/user menu trigger.
   * The sidebar layout moves it to the drawer paper's bottom row, after
   * `headerActions`. Takes the same render-function state as
   * `headerActions`.
   */
  readonly userMenu?: AppShellNavSlot
  /**
   * A full-width banner below the app bar, host content and already
   * translated. It is part of the fixed shell head: the head is one
   * fixed container holding the app bar first (where one renders) and
   * the banner after it in normal flow, so the banner sits below the
   * header in DOM order and on screen, and the content spacers reserve
   * the head's measured height at every breakpoint. A desktop sidebar
   * shell renders no app bar, so there the banner is the head's only
   * child and sits at the very top. The slot adds no padding, border or
   * minimum height -- its height is exactly what the host node renders,
   * and an empty node adds none. The slot belongs to the shell, so a
   * route change never unmounts it; it adds no landmark and no live
   * region -- the host node carries whatever roles it needs, and the
   * banner landmark remains the app bar (there is none on a desktop
   * sidebar shell). Absent, `null` or `false` renders no slot container
   * and adds no height.
   */
  readonly banner?: ReactNode
  /**
   * The navigation landmark's accessible name -- and the name of the
   * temporary drawer, which opens as a modal `nav` labelled by it.
   * Already-translated host text; defaults to this package's own
   * `appShell.navLabel`. A host that mounts a shell beside another
   * shell's own navigation supplies a distinguishing name so neither
   * landmark reads as the other in the accessibility tree.
   */
  readonly navLabel?: string
  /**
   * Host content rendered inside the drawer paper immediately before the
   * navigation landmark, in both the permanent and the temporary drawer.
   * A render function receives the mounted drawer's state
   * (`AppShellNavSlotState`) so the host can compose a compact header in
   * the collapsed rail. The slot is a sibling of the landmark, so the
   * `nav` landmark and its accessible name are unchanged. Absent, `null`
   * or `false` renders nothing and adds no height.
   */
  readonly navHeader?: AppShellNavSlot
  /**
   * Host content rendered inside the drawer paper immediately after the
   * navigation landmark, in both the permanent and the temporary drawer.
   * The same render-function state as `navHeader` applies; `closeDrawer`
   * is the mobile drawer's close channel. The slot is a sibling of the
   * landmark, so the `nav` landmark and its accessible name are
   * unchanged. Absent, `null` or `false` renders nothing and adds no
   * height.
   */
  readonly navFooter?: AppShellNavSlot
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
   * renders the desktop collapse/expand toggle button -- in the AppBar,
   * or at the end of the drawer paper's top row in the sidebar layout;
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
 * The horizontal inset of the collapsed rail's rows, in px. It is the
 * focus ring's reach -- a 2px outline offset by 2px -- so a control inside
 * the rail keeps its ring clear of the paper's edge. A spacing step would
 * shrink with a dense preset's base and cut the ring instead.
 */
const RAIL_ROW_INSET_PX = 4

/**
 * The rail's floor, in px. The derived width is a multiple of the
 * spacing base, so a theme that shrinks the base (a dense preset, say)
 * pushes the rail below what it has to hold: a control at the layout
 * design's touch-target minimum, the ring's clearance on both sides of
 * it, and the paper's own divider. The rail therefore never renders
 * narrower than that; at the default base the derived width is already
 * above it, so the default rendering is unchanged.
 */
const COLLAPSED_SIDEBAR_MIN_WIDTH = 44 + RAIL_ROW_INSET_PX * 2 + 1

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

/** Whether a declared host slot renders anything: absent, null, false do not. */
function hasSlotContent(value: AppShellNavSlot | undefined): boolean {
  return value !== undefined && value !== null && value !== false
}

/**
 * Renders a slot: host content as given, or a render function resolved
 * against the shell state at the place it renders. An absent or explicitly
 * empty slot renders nothing.
 */
function renderNavSlot(
  slot: AppShellNavSlot | undefined,
  state: AppShellNavSlotState,
): ReactNode {
  if (slot === undefined || slot === null || slot === false) {
    return null
  }
  return typeof slot === 'function' ? slot(state) : slot
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
  flushTop = false,
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
  /** Drops the list's own top inset, so the first row starts on the line
   * below the chrome above it -- the sidebar shape's brand row is separated
   * from the entries by a rule, and a second inset under that rule reads as
   * a mis-centred brand row. */
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

/**
 * The topbar layout's user-menu end of the header. Below md it is the
 * header's second band, whatever the slot renders: a full-width basis
 * takes it off the first band instead of letting a narrow user menu cram
 * itself beside the actions and squeeze the brand to nothing. At md and up
 * it stays the far end of the single row. A slot that resolves to nothing
 * renders nothing -- neither the box nor a second flex line.
 */
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

/**
 * The responsive app-chrome shell: fixed header, responsive nav drawer,
 * `main` content region. See the module doc comment for the full
 * contract.
 */
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

  // The chrome arrangement. `sidebarLayout` decides where the brand,
  // actions and menu render (see the module doc comment); the content
  // region, landmarks below the app bar and drawer variants do not change.
  const sidebarLayout = shellLayout === 'sidebar'

  // The collapsed rail's fixed width, derived from the theme spacing base
  // (the spacing token unit) -- see COLLAPSED_SIDEBAR_SPACING_UNITS --
  // and floored so a theme with a smaller spacing base cannot push the
  // rail below the touch-target minimum (COLLAPSED_SIDEBAR_MIN_WIDTH).
  // CSS max() keeps the theme's own unit: a function-form spacing option
  // (for example rem) reaches the rail unchanged, and the floor still wins
  // when the spacing base is small. Parsing the value to a number would
  // silently drop the unit and pin every theme to px.
  const collapsedSidebarWidth = `max(${theme.spacing(
    COLLAPSED_SIDEBAR_SPACING_UNITS,
  )}, ${COLLAPSED_SIDEBAR_MIN_WIDTH}px)`

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

  // The drawer slots' state. The permanent drawer is always open, so its
  // closeDrawer is a deliberate no-op; the temporary drawer gets the
  // close transition the navigation rows use -- the shell closes its own
  // uncontrolled drawer, and a controlled host is told to close through
  // the channel it already reads. Only one variant is mounted at a time,
  // so each slot state is resolved against the variant it renders in.
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

  // Whether the host supplied a banner at all. An absent, null or false
  // slot renders no container; a truthy node -- including one that
  // renders as a zero-height live region -- keeps the slot in the head.
  const hasBanner = hasSlotContent(banner)

  // Whether the shell renders a fixed head at all. The topbar layout
  // always does (its app bar is the head); a sidebar shell does when it
  // has a banner or is below md (where the minimal top bar stands in for
  // the app bar). A desktop sidebar shell with no banner has no fixed
  // head -- and therefore no banner landmark -- so it renders no head
  // container and its spacers are absent rather than a zero-height or
  // theme-height hole: the jsdom fallback (no measurement) would
  // otherwise leave the theme toolbar's 56/64px as a permanent gap.
  const hasFixedHead = !sidebarLayout || hasBanner || !isDesktop

  // The fixed head's rendered height is the number every spacer clears.
  // The header row wraps under real overflow pressure and the banner has
  // its own height, so only the rendered container knows the total; one
  // ResizeObserver on that container keeps the spacers in lockstep with
  // it without deriving an offset from either child. Without a
  // ResizeObserver (jsdom) the measurement never arrives and the
  // spacers keep the theme toolbar height, exactly as before.
  //
  // The measurement belongs to the mounted head: leaving a sidebar shell
  // without a head (or changing layouts/breakpoints, which changes the
  // head's contents) resets it and re-observes, so a height measured for
  // one arrangement never survives into another. `observe()` reports the
  // container's size on attach in a real browser, so a re-observe also
  // re-measures a head whose height happened not to change.
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
  //
  // The paper is a fixed column in both layouts: the nav is its one
  // growing, scrolling region, so navFooter sits at the paper's bottom
  // edge. In the topbar layout that edge is the account entry's only
  // home -- the fixed app bar carries the rest of the chrome.
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
        // The navigation is the drawer column's one growing, scrolling
        // region in both layouts, so the footer slot after it sits at the
        // paper's bottom edge.
        flex: '1 1 auto',
        minHeight: 0,
        overflowY: 'auto',
        // The rows' pointer and selection states are tinted with the
        // primary color instead of MUI's neutral overlay: a neutral wash
        // turns grey on the shell's own tinted surface (and on a glass
        // sidebar's gradient), while the accent keeps a row reading as
        // part of the chrome. One rule on the landmark reaches every row
        // -- leaves, expandable parents and group headers alike.
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

  // The three offset placeholders that keep content clear of the fixed
  // shell head: one inside the drawer paper and one inside `main`, for
  // whichever drawer variant is mounted. When the fixed head's height
  // has been measured, each spacer carries exactly that height; before
  // any measurement exists it is a plain theme-height Toolbar (the
  // single-row case, which is also the whole jsdom case).
  //
  // The measured height is written as an inline style, never through
  // `sx`: the theme toolbar mixin contributes its own `min-height` --
  // 56 at the base and 64 under the `sm` breakpoint -- and that
  // breakpoint rule lands after an equal-specificity `sx` class, so an
  // `sx` height is overridden to the theme's single-row height from
  // 600px up, where a wrapped header plus the banner then cover the
  // top of content again. An inline style outranks every class rule,
  // so the measured height holds at every viewport width.
  const spacerMinHeight = headHeight
  const spacer = hasFixedHead ? (
    <Toolbar
      style={
        spacerMinHeight !== null ? { minHeight: spacerMinHeight } : undefined
      }
    />
  ) : null

  // The mobile drawer trigger: the shell's only navigation affordance
  // below md in either layout. The topbar layout places it at the start
  // of the app bar's toolbar, the sidebar layout in the minimal top bar
  // -- the same control, the same strings, two positions.
  const mobileNavToggle = !isDesktop ? (
    <IconButton
      color="inherit"
      edge="start"
      aria-label={mobileOpen ? t('appShell.closeNav') : t('appShell.openNav')}
      aria-expanded={mobileOpen}
      onClick={() => setMobileOpen(!mobileOpen)}
      // In the topbar layout one spacing step, not two: at the narrowest
      // supported viewports the toggle, the brand and the header actions
      // share one band, and the extra step is what pushes the actions off
      // it. The sidebar layout's minimal top bar holds only the toggle and
      // the brand, so it keeps the theme's two-step gutter.
      sx={{ marginRight: sidebarLayout ? 2 : 1 }}
    >
      {mobileOpen ? <CloseIcon /> : <MenuIcon />}
    </IconButton>
  ) : null

  // The desktop collapse toggle's state-derived label and activation,
  // shared by its two positions (the app bar's start and the drawer
  // paper's top row).
  const collapseToggleLabel = sidebarCollapsed
    ? t('appShell.expandNav')
    : t('appShell.collapseNav')
  const toggleCollapse = () => {
    onSidebarCollapsedChange?.(!sidebarCollapsed)
  }

  // The sidebar layout's drawer-paper chrome. The top row carries the
  // brand and the desktop collapse toggle; the brand's box is the flex
  // item that shrinks (minWidth: 0, overflow: hidden), so the collapsed
  // rail clips it instead of growing the paper, while the label stays in
  // the accessibility tree. The bottom row carries the header actions and
  // the user menu, pinned by `flexShrink: 0` while the nav above it
  // scrolls, and stacks vertically and centered in the rail. Both rows
  // render only when they have content: an absent slot adds no row and
  // no height.
  const sidebarTopRow =
    hasSlotContent(header) || collapseToggleEnabled ? (
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: sidebarCollapsed ? 0 : 1,
          flexShrink: 0,
          // The rail holds a control at the touch-target minimum, cleared
          // on each side by the focus ring's reach; the rail's own floor
          // is that sum, so these insets are the ring's, not a spacing
          // step's. An expanded sidebar starts the brand on the navigation
          // icons' drawn edge: those glyphs are drawn inside a 24px box with
          // a few pixels of inset of their own, so the box's left edge (16px)
          // would leave the word sitting left of every icon below it.
          ...(sidebarCollapsed
            ? // The rail holds the collapse toggle alone: centred in the
              // paper, not pinned to its leading edge.
              { px: `${RAIL_ROW_INSET_PX}px`, justifyContent: 'center' }
            : { pl: 2.5, pr: 2 }),
          // The bar's own height, not a spacing step's: this row is the
          // sidebar's copy of the top bar's chrome, so the two shapes start
          // their content on the same line (64px, the toolbar's minimum, plus
          // the rule below).
          minHeight: 64,
          py: 1,
        }}
      >
        <Box
          sx={{
            // The rail keeps the brand in the accessibility tree but has no
            // room to paint any of it: a box that merely shrinks past the
            // text leaves a clipped half-glyph at the paper's edge, so the
            // collapsed rail takes the box to zero width instead.
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

  // The sidebar layout's drawer paper is one chrome column, but only its
  // centre is a navigation landmark. The brand row, the host slots and the
  // bottom action row would otherwise sit outside every landmark -- a
  // page-level axe scan rejects that (`region`), and the landmark list a
  // screen reader offers cannot reach them. One named complementary
  // landmark around the column gives that chrome a landmark home while
  // leaving the `nav` landmark and the slots' sibling contract untouched.
  // The topbar layout's paper renders exactly as it did before.
  // The drawer paper is one flex column in both layouts: brand rows and
  // header slots first, the navigation growing in the middle, the footer
  // slot last at the paper's bottom edge. The aside landmark names the
  // column; the nav landmark stays nested inside it.
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
      {/* Visually hidden until focused; the first focusable element in
          the shell, so it renders before the fixed head (the AppBar and
          the banner below it) in DOM order -- every control inside the
          head, including the banner's own end and close controls,
          therefore follows it in the tab order instead of preceding it.
          It sits outside the shell's landmarks (the skip target is
          `main`), which is allowed for a link whose fragment target
          resolves. Activating it focuses the main landmark directly
          instead of navigating to the fragment: fragment navigation
          rewrites location.hash, and in a hash-routed host the fragment
          IS the route -- a skip link that navigates would replace the
          app route with the generated target id. The keyboard path is
          unchanged: Tab reaches the link, Enter activates it, and the
          focus visibly moves into main (browsers scroll a focused
          element into view, matching what fragment navigation would
          have scrolled to). */}
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
      {/* The fixed shell head: one pinned container holding the app bar
          first and the banner immediately after it, in normal flow. Its
          measured height is what every spacer reserves; keeping both
          children in one container means neither child's position
          derives from the other's measurement. The container carries the
          layer the app bar used to carry (drawer + 1): above page
          content and the drawer papers, below modal panels and dialogs.
          The banner slot adds no padding, border or minimum height --
          its height is exactly what the host node renders -- and no
          landmark (an app bar is the banner landmark) and no live
          region.

          A desktop sidebar shell renders no app bar, so if it has a
          banner the head holds the banner alone at the very top; without
          one it renders no head at all (see hasFixedHead). */}
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
            // The header is the container surface, not the primary role:
            // the shell's brand color belongs to controls, and a
            // saturated bar would also read differently from the dark
            // mode, where MUI's primary fill is dropped and the surface
            // shows through. The theme's MuiAppBar mapping paints the
            // fill and the bottom divider that separates the bar from
            // the page, so the shell never re-declares them and a
            // surface token override -- a glass fill, say -- reaches the
            // bar with no change here.
            <AppBar
              position="static"
              color="inherit"
              elevation={0}
              sx={{ color: 'text.primary' }}
            >
              {/* Below md the header is arranged in at most two bands:
                  the drawer trigger, the brand and the header actions on
                  the first, the user menu on the second. The tighter
                  gutter is what leaves the brand enough width to stay on
                  the first band beside the actions at 375px; at md and up
                  the toolbar keeps its theme gutter. */}
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
                    // The brand is the one item that concedes width below
                    // md: a zero flex basis keeps it out of the line
                    // breaking, so it never pushes the header actions onto
                    // a band of their own, and the remaining space is its
                    // share (the host's own noWrap label ellipsizes inside
                    // it rather than spilling over the controls). At md and
                    // up it grows exactly as before.
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
                      // Below md the actions share the first band with the
                      // brand and the toggle, so their own spacing steps
                      // shrink: half a step between controls and a half
                      // step before the user menu. At md and up the bar has
                      // the width for the full steps.
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
            // The narrow sidebar shell's minimal top bar: the drawer
            // trigger and the brand only. The header actions and the
            // user menu live in the temporary drawer's bottom row, and
            // the brand appears here exactly once -- the drawer has no
            // top row below md. This bar is the banner landmark; a
            // desktop sidebar shell has none.
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
                // The paper is a fixed column in both layouts: the nav
                // between the chrome rows is the one scrolling region, so
                // the paper itself stops scrolling (the rail also clips its
                // horizontal overflow).
                overflow: 'hidden',
                ...(sidebarCollapsed ? { overflowX: 'hidden' } : {}),
              },
            }}
          >
            {drawerColumn(
              <>
                {spacer}
                {sidebarLayout && sidebarTopRow}
                {/* The brand row is chrome above the navigation, as the app
                    bar is above the content in the topbar shape. The rule is
                    the theme's own Divider -- the same component and recipe
                    the account row uses at the paper's bottom. */}
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
                // The same fixed-column treatment as the permanent paper:
                // the nav scrolls, the bottom slot stays pinned.
                overflow: 'hidden',
              },
            }}
          >
            {drawerColumn(
              <>
                {spacer}
                {renderNavSlot(navHeader, temporarySlotState)}
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
                {renderNavSlot(navFooter, temporarySlotState)}
                {/* Below md the brand already renders in the minimal top
                    bar, so the temporary drawer has no top row -- the brand
                    appears once per page. The bottom row renders as usual. */}
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
