/**
 * The account menu's divider and geometry across every shipped theme preset,
 * in both color modes, in Chromium.
 *
 * jsdom carries the class names and the inline styles but lays nothing out,
 * so the geometry the account bar's contract states -- the menu's inset from
 * the navigation column, its width, the icon and avatar tracks, and the two
 * gaps around the account row -- can only be told apart here. The same is
 * true of the divider colour and the paper surface: a preset restyles them
 * through tokens, and the menu has to follow. Light mode paints the divider
 * token; dark mode paints a translucent wash of `text.primary`, which has to
 * separate from the paper it is drawn on.
 *
 * Every preset/mode combination is its own case so the matrix reports per
 * combination. A case measures every criterion before it fails and lists the
 * ones that missed, so one bad criterion does not hide the rest.
 */

import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { alpha } from '@mui/material/styles'
import axe from 'axe-core'
import { beforeEach, describe, expect, it } from 'vitest'
import { page } from 'vitest/browser'
import { attachSession } from '@speed/auth-core'
import type { AppShellNavItem } from '@speed/layout-kit'
import {
  createQueryClient,
  createSpeedI18n,
  SpeedAppProviders,
} from '@speed/product-shell/bootstrap'
import type { SpeedAppServices } from '@speed/product-shell/bootstrap'
import { RouteProvider } from '@speed/route-kit'
import { themePresets } from '@speed/theme-presets'
import { createAppTheme } from '@speed/ui-kit'
import { AppServicesProvider } from './app-services.js'
import { AccountEntry } from './chrome.js'
import { DEMO_NAMESPACE, demoResources } from './resources.js'
import { createHarnessClient, renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'

/** The account bar's geometry contract, in CSS pixels. */
const EXPECTED = {
  menuLeftInset: 9,
  avatarInset: 16,
  /** The navigation's own icon mark, and so every menu entry's. */
  iconGlyphSize: 24,
  rowBottomGap: 12,
  dividerRowGap: 12,
} as const

/** The tolerance the contract allows on a geometric measurement. */
const TOLERANCE = 1

/**
 * The alpha the theme applies to `text.primary` for a dark-mode rule (see
 * the divider override in `@speed/ui-kit`).
 */
const DARK_RULE_ALPHA = 0.16

/** The minimum paper-relative contrast a dark-mode rule must reach. */
const MIN_DARK_RULE_CONTRAST = 1.5

const COLOR_MODES = ['light', 'dark'] as const

beforeEach(() => {
  window.history.replaceState({}, '', '/')
})

/** The computed CSS color of a token value, normalized by the browser. */
function normalizedColor(value: string): string {
  const probe = document.createElement('div')
  probe.style.color = value
  document.body.appendChild(probe)
  const color = getComputedStyle(probe).color
  probe.remove()
  return color
}

/** The computed CSS background color of a token value, normalized. */
function normalizedBackground(value: string): string {
  const probe = document.createElement('div')
  probe.style.backgroundColor = value
  document.body.appendChild(probe)
  const color = getComputedStyle(probe).backgroundColor
  probe.remove()
  return color
}

/** The computed CSS box-shadow of a token value, normalized. */
function normalizedBoxShadow(value: string): string {
  const probe = document.createElement('div')
  probe.style.boxShadow = value
  document.body.appendChild(probe)
  const shadow = getComputedStyle(probe).boxShadow
  probe.remove()
  return shadow
}

/** A color's alpha channel; 1 when the color is fully opaque. */
function alphaOf(color: string): number {
  const match = /rgba?\(([^)]+)\)/.exec(color)
  if (match === null) {
    return 1
  }
  const parts = (match[1] ?? '').split(',').map((part) => Number(part.trim()))
  return parts.length === 4 ? (parts[3] ?? 1) : 1
}

interface Rgba {
  readonly r: number
  readonly g: number
  readonly b: number
  readonly a: number
}

/** Parse an `rgb()`/`rgba()` string; anything else is a test bug. */
function parseColor(value: string): Rgba {
  const match = /rgba?\(([^)]+)\)/.exec(value)
  if (match === null) {
    throw new Error(`unparsed colour: ${value}`)
  }
  const parts = (match[1] ?? '').split(/[,\s/]+/).filter(Boolean).map(Number)
  const [r = 0, g = 0, b = 0, a = 1] = parts
  return { r, g, b, a }
}

/** `foreground` composited over an opaque `background`. */
function compositeOver(foreground: Rgba, background: Rgba): Rgba {
  const mix = (fg: number, bg: number) =>
    fg * foreground.a + bg * (1 - foreground.a)
  return {
    r: mix(foreground.r, background.r),
    g: mix(foreground.g, background.g),
    b: mix(foreground.b, background.b),
    a: 1,
  }
}

/** The WCAG linear channel value of an 8-bit sRGB channel. */
function linearChannel(channel: number): number {
  const value = channel / 255
  return value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
}

/** The WCAG relative luminance of an opaque colour. */
function relativeLuminance(color: Rgba): number {
  return (
    0.2126 * linearChannel(color.r) +
    0.7152 * linearChannel(color.g) +
    0.0722 * linearChannel(color.b)
  )
}

/** The WCAG contrast ratio between two opaque colours. */
function contrastRatio(first: Rgba, second: Rgba): number {
  const [lighter, darker] = [
    relativeLuminance(first),
    relativeLuminance(second),
  ].sort((left, right) => right - left) as [number, number]
  return (lighter + 0.05) / (darker + 0.05)
}

/**
 * The opaque colour a rule is drawn on: the nearest non-transparent ancestor
 * backgrounds, layered in paint order over the page's own fill.
 */
function paperBehind(element: Element, pageFill: string): Rgba {
  const layers: string[] = []
  let node: Element | null = element
  while (node !== null) {
    const background = getComputedStyle(node).backgroundColor
    if (alphaOf(background) > 0) {
      layers.push(background)
      if (alphaOf(background) >= 1) {
        break
      }
    }
    node = node.parentElement
  }
  let paper = parseColor(pageFill)
  for (const layer of layers.reverse()) {
    paper = compositeOver(parseColor(layer), paper)
  }
  return paper
}

/** Rounds a measured pixel value for a readable failure message. */
function px(value: number): number {
  return Math.round(value * 100) / 100
}

/**
 * Records a geometric miss: the measurement must sit on the expected value
 * within the contract's tolerance.
 */
function checkClose(
  issues: string[],
  label: string,
  actual: number,
  expected: number,
): void {
  if (Math.abs(actual - expected) > TOLERANCE) {
    issues.push(
      `${label}: expected ${expected}px ±${TOLERANCE}px, measured ${px(actual)}px`,
    )
  }
}

/**
 * The left edge of an entry's own text, read from the direct text node that
 * follows its icon cell. Measuring the text rather than deriving the offset
 * from the cell's width keeps the comparison honest: it is where the label
 * actually starts, not where the box arithmetic says it should.
 */
function menuItemLabelLeft(item: HTMLElement): number | undefined {
  for (const node of Array.from(item.childNodes)) {
    if (
      node.nodeType === Node.TEXT_NODE &&
      (node.textContent ?? '').trim() !== ''
    ) {
      const range = document.createRange()
      range.selectNodeContents(node)
      return range.getBoundingClientRect().left
    }
  }
  return undefined
}

/**
 * Records a divider that is not the visible 1px solid rule in the theme's
 * divider colour. A zero width, a non-solid style or a transparent/other
 * colour all mean the rule would not read. In dark mode the rule is a
 * translucent wash, so its drawn colour is also composited over the paper
 * it sits on and required to separate from that paper.
 */
function checkDivider(
  issues: string[],
  divider: Element,
  label: string,
  dividerColor: string,
  pageFill: string,
  minimumContrast?: number,
): void {
  const style = getComputedStyle(divider)
  if (style.borderBottomStyle !== 'solid') {
    issues.push(`${label}: border-bottom-style is ${style.borderBottomStyle}`)
  }
  if (style.borderBottomWidth !== '1px') {
    issues.push(`${label}: border-bottom-width is ${style.borderBottomWidth}`)
  }
  if (style.borderBottomColor !== dividerColor) {
    issues.push(
      `${label}: border-bottom-color is ${style.borderBottomColor}, theme divider is ${dividerColor}`,
    )
  }
  if (minimumContrast !== undefined) {
    const paper = paperBehind(divider, pageFill)
    const drawn = compositeOver(parseColor(style.borderBottomColor), paper)
    const ratio = contrastRatio(drawn, paper)
    if (ratio < minimumContrast) {
      issues.push(
        `${label}: contrast over paper is ${px(ratio)}:1, expected at least ${minimumContrast}:1`,
      )
    }
  }
}

describe('the account menu across theme presets', () => {
  for (const preset of themePresets) {
    for (const mode of COLOR_MODES) {
      it(`places its dividers and geometry under ${preset.id} / ${mode}`, async () => {
        await page.viewport(1280, 900)
        const label = `${preset.id} / ${mode}`
        const theme = createAppTheme(preset.tokens, undefined, mode).theme
        // Light mode paints the divider token; dark mode washes
        // `text.primary` at a low alpha so the rule clears the dark paper.
        const dividerColor = normalizedColor(
          mode === 'dark'
            ? alpha(theme.palette.text.primary, DARK_RULE_ALPHA)
            : theme.palette.divider,
        )
        const minimumContrast =
          mode === 'dark' ? MIN_DARK_RULE_CONTRAST : undefined
        const pageFill = normalizedBackground(theme.palette.background.default)
        const paperBorderColor = normalizedColor(theme.palette.divider)
        const issues: string[] = []

        renderDemoApp({
          session: createTestSession(authenticatedSnapshot()),
          theme: {
            presets: themePresets,
            defaultPreset: preset.id,
            defaultMode: mode,
            projectTokens: {},
          },
        })

        const trigger = await screen.findByTestId('account-entry-trigger')
        const column = trigger.closest('.MuiDrawer-paper') as HTMLElement | null
        if (column === null) {
          throw new Error(`${label}: the account entry is not in the drawer`)
        }
        const entry = screen.getByTestId('account-entry')
        const rowDivider = entry.firstElementChild as HTMLElement
        const avatar = trigger.querySelector('.MuiAvatar-root') as HTMLElement

        const columnRect = column.getBoundingClientRect()
        const columnContentLeft =
          columnRect.left +
          Number.parseFloat(getComputedStyle(column).borderLeftWidth || '0')
        const triggerRect = trigger.getBoundingClientRect()
        const rowDividerRect = rowDivider.getBoundingClientRect()
        const avatarRect = avatar.getBoundingClientRect()

        // The rule above the account row is visible in the theme's divider
        // colour, not a zero-width or transparent edge.
        checkDivider(
          issues,
          rowDivider,
          `${label}: row divider`,
          dividerColor,
          pageFill,
          minimumContrast,
        )

        // The row shares the navigation's icon track: the avatar starts 16px
        // in, the divider sits 12px above the row and the row 12px above the
        // column's bottom edge.
        checkClose(
          issues,
          `${label}: avatar inset`,
          avatarRect.left - columnContentLeft,
          EXPECTED.avatarInset,
        )
        checkClose(
          issues,
          `${label}: row bottom gap`,
          columnRect.bottom - triggerRect.bottom,
          EXPECTED.rowBottomGap,
        )
        checkClose(
          issues,
          `${label}: divider-to-row gap`,
          triggerRect.top - rowDividerRect.bottom,
          EXPECTED.dividerRowGap,
        )

        // The sidebar's own icon track is the reference, captured in this
        // same render before the menu opens: a modal menu marks the rest of
        // the page aria-hidden, so the navigation landmark can no longer be
        // queried by role once it is open.
        const navigation = screen.getByRole('navigation')
        const navLink = navigation.querySelector('a')
        const navIconCell = navLink?.querySelector('.MuiListItemIcon-root')
        const navLabel = navLink?.querySelector('.MuiListItemText-root')
        const navIconCellRect =
          navIconCell instanceof HTMLElement
            ? navIconCell.getBoundingClientRect()
            : undefined
        const navLabelLeft =
          navLabel instanceof HTMLElement
            ? navLabel.getBoundingClientRect().left
            : undefined
        if (navIconCellRect === undefined || navLabelLeft === undefined) {
          issues.push(
            `${label}: the navigation entry has no icon cell or label`,
          )
        }
        if (navIconCell instanceof HTMLElement) {
          const navGlyph = navIconCell.querySelector('svg')
          if (navGlyph !== null) {
            checkClose(
              issues,
              `${label}: navigation glyph`,
              navGlyph.getBoundingClientRect().width,
              EXPECTED.iconGlyphSize,
            )
          }
        }

        await userEvent.setup().click(trigger)
        const menu = await screen.findByRole('menu', undefined, {
          timeout: 5000,
        })
        const paper = menu.parentElement as HTMLElement
        // The Grow transition scales the paper while it opens; measure the
        // settled box.
        await waitFor(() => {
          const transform = getComputedStyle(paper).transform
          const settled =
            transform === 'none' ||
            /^matrix\(1, 0, 0, 1, 0, 0\)$/.test(transform)
          if (!settled) {
            throw new Error(`menu still transforming: ${transform}`)
          }
        })

        const paperRect = paper.getBoundingClientRect()
        const paperStyle = getComputedStyle(paper)

        // The menu opens from the column's left edge (its paper's own 9px
        // margin is part of the contract) and keeps the theme's width.
        const menuLeftInset = paperRect.left - columnRect.left
        if (Math.abs(menuLeftInset - EXPECTED.menuLeftInset) > TOLERANCE) {
          issues.push(
            `${label}: menu left edge: expected ${EXPECTED.menuLeftInset}px ±${TOLERANCE}px, measured ${px(menuLeftInset)}px (computed margin-left ${paperStyle.marginLeft})`,
          )
        }
        // The expected width is the theme's own spacing(30), not a fixed
        // pixel count: the paper settles at that minimum unless its content
        // is wider, so the content's own width is measured too.
        const themeMinWidth = Number.parseFloat(theme.spacing(30))
        const inlineWidth = paper.style.width
        const inlineMinWidth = paper.style.minWidth
        paper.style.width = 'max-content'
        paper.style.minWidth = '0px'
        const contentWidth = paper.getBoundingClientRect().width
        paper.style.width = inlineWidth
        paper.style.minWidth = inlineMinWidth
        checkClose(
          issues,
          `${label}: menu width`,
          paperRect.width,
          Math.max(themeMinWidth, contentWidth),
        )

        // The sidebar's own icon track is the reference: every entry shares
        // it -- its icon cell starts where the navigation's does and is as
        // wide, its glyph is the navigation's 24px mark, and its label starts
        // exactly on the navigation label's left edge.
        if (navIconCellRect !== undefined && navLabelLeft !== undefined) {
          const items = within(menu).getAllByRole('menuitem')
          for (const item of items) {
            const text = item.textContent ?? ''
            const iconBox = item.firstElementChild
            const glyph = iconBox?.querySelector('svg')
            if (!(iconBox instanceof HTMLElement) || glyph == null) {
              issues.push(
                `${label}: menu entry has no icon box or glyph (${text})`,
              )
              continue
            }
            const iconBoxRect = iconBox.getBoundingClientRect()
            checkClose(
              issues,
              `${label}: item icon cell left (${text})`,
              iconBoxRect.left,
              navIconCellRect.left,
            )
            checkClose(
              issues,
              `${label}: item icon cell width (${text})`,
              iconBoxRect.width,
              navIconCellRect.width,
            )
            checkClose(
              issues,
              `${label}: item glyph (${text})`,
              glyph.getBoundingClientRect().width,
              EXPECTED.iconGlyphSize,
            )
            const labelLeft = menuItemLabelLeft(item)
            if (labelLeft === undefined) {
              issues.push(`${label}: menu entry has no label (${text})`)
              continue
            }
            checkClose(
              issues,
              `${label}: item label left (${text})`,
              labelLeft,
              navLabelLeft,
            )
          }
        }

        // The menu's own dividers are visible rules too.
        const dividers = within(paper).getAllByRole('separator')
        if (dividers.length !== 2) {
          issues.push(
            `${label}: expected 2 menu dividers, found ${dividers.length}`,
          )
        }
        dividers.forEach((divider, index) => {
          checkDivider(
            issues,
            divider,
            `${label}: menu divider ${index + 1}`,
            dividerColor,
            pageFill,
            minimumContrast,
          )
        })

        // The paper keeps the divider-coloured outline and the theme's own
        // elevation-12 shadow, read literally: a preset that sets that slot to
        // `none` keeps it (the ascii preset does), so this is not a "has a
        // shadow" check.
        if (paperStyle.borderTopStyle !== 'solid') {
          issues.push(
            `${label}: menu border style is ${paperStyle.borderTopStyle}`,
          )
        }
        if (paperStyle.borderTopWidth !== '1px') {
          issues.push(
            `${label}: menu border width is ${paperStyle.borderTopWidth}`,
          )
        }
        if (paperStyle.borderTopColor !== paperBorderColor) {
          issues.push(
            `${label}: menu border colour is ${paperStyle.borderTopColor}, theme divider is ${paperBorderColor}`,
          )
        }
        const themeShadow = normalizedBoxShadow(theme.shadows[12])
        if (paperStyle.boxShadow !== themeShadow) {
          issues.push(
            `${label}: menu box-shadow is ${paperStyle.boxShadow}, theme.shadows[12] is ${themeShadow}`,
          )
        }

        // The account bar must leave the theme's paper surface in place. For
        // every preset the computed fill is the theme's overlay fill; liquid
        // glass is the discriminating case, where that fill is translucent
        // and carries the backdrop filter rather than the plain paper colour.
        const overlayFill = normalizedBackground(theme.surface.overlay.fill)
        if (paperStyle.backgroundColor !== overlayFill) {
          issues.push(
            `${label}: menu background is ${paperStyle.backgroundColor}, theme overlay fill is ${overlayFill}`,
          )
        }
        if (preset.id === 'glass') {
          const plainPaper = normalizedBackground(theme.palette.background.paper)
          if (alphaOf(paperStyle.backgroundColor) >= 1) {
            issues.push(
              `${label}: glass menu background is opaque (${paperStyle.backgroundColor})`,
            )
          }
          if (paperStyle.backgroundColor === plainPaper) {
            issues.push(
              `${label}: glass menu background collapsed to the plain paper colour (${plainPaper})`,
            )
          }
          if (paperStyle.backdropFilter === 'none') {
            issues.push(`${label}: glass menu has no backdrop filter`)
          }
        }

        expect(issues, `${label}: account menu contract`).toEqual([])
      })
    }
  }
})

/**
 * The trigger's Label in Name (WCAG 2.5.3), settled in Chromium because the
 * rule needs the canvas text-measurement API: jsdom answers
 * `getContext('2d')` with null, so the rule stays indeterminate there. The
 * trigger renders the avatar's initials and the display name as two text
 * nodes; without whitespace between them their text coalesces into one word
 * that the accessible name does not contain.
 */
describe("the account trigger's Label in Name", () => {
  it('keeps the visible initials and name inside its accessible name', async () => {
    await page.viewport(1280, 900)
    renderDemoApp({ session: createTestSession(authenticatedSnapshot()) })
    const trigger = await screen.findByTestId('account-entry-trigger')
    await document.fonts.ready

    const result = await axe.run(trigger, {
      runOnly: { type: 'rule', values: ['label-content-name-mismatch'] },
    })
    const findings = [...result.violations, ...result.incomplete].flatMap(
      (rule) => rule.nodes.map((node) => `${rule.id}: ${node.html}`),
    )
    expect(findings).toEqual([])
    // The rule must have judged the trigger, not skipped it as inapplicable.
    expect(
      result.passes.reduce((total, rule) => total + rule.nodes.length, 0),
    ).toBeGreaterThan(0)
  })
})

/** The organization entry this case's menu carries; the group renders one
 * entry, so its bottom edge is the group's. */
const ORGANIZATION_ITEM: AppShellNavItem = {
  id: '/organization/admin/invitations',
  label: 'Invitations',
}

/**
 * Mounts the sidebar account entry alone, with the organization group present,
 * under the production provider stack and the theme the case renders. The
 * group is what the divider before sign-out follows, so this is the shape that
 * exercises the rhythm MUI's sibling rule cannot reach.
 */
function renderAccountEntry(): void {
  const i18n = createSpeedI18n({
    supportedLanguages: ['zh-CN', 'en-US'],
    defaultLanguage: 'en-US',
    storage: null,
    urlParameterName: null,
    navigatorLanguages: [],
    namespaces: [{ namespace: DEMO_NAMESPACE, resources: demoResources }],
  })
  const queryClient = createQueryClient()
  const session = createTestSession(authenticatedSnapshot())
  attachSession(session)
  const services: SpeedAppServices = {
    session,
    api: createHarnessClient(),
    queryClient,
  }
  render(
    <SpeedAppProviders
      i18n={i18n}
      queryClient={queryClient}
      theme={{
        presets: themePresets,
        defaultPreset: 'default',
        defaultMode: 'light',
        projectTokens: {},
      }}
    >
      <AppServicesProvider services={services}>
        <RouteProvider table={[]} fallback={() => null}>
          <AccountEntry organizationItems={[ORGANIZATION_ITEM]} />
        </RouteProvider>
      </AppServicesProvider>
    </SpeedAppProviders>,
  )
}

/**
 * The menu's section rhythm belongs to the shared list, not to MUI's sibling
 * rule: every divider carries theme.spacing(1) above and below, including the
 * one that follows the organization group's container rather than a menu item.
 * Measured on the sidebar account entry's menu, where that group renders.
 */
describe('the account menu section rhythm', () => {
  it('spaces every divider, including the one before sign-out', async () => {
    await page.viewport(1280, 900)
    const preset =
      themePresets.find((candidate) => candidate.id === 'default') ??
      themePresets[0]
    if (preset === undefined) {
      throw new Error('the theme shelf is empty')
    }
    const theme = createAppTheme(preset.tokens, undefined, 'light').theme
    const rhythm = Number.parseFloat(theme.spacing(1))
    expect(rhythm).toBeGreaterThan(0)

    renderAccountEntry()

    const trigger = await screen.findByTestId('account-entry-trigger')
    await userEvent.setup().click(trigger)
    const menu = await screen.findByRole('menu', undefined, { timeout: 5000 })
    const paper = menu.parentElement as HTMLElement
    // The Grow transition scales the paper while it opens; measure the
    // settled box.
    await waitFor(() => {
      const transform = getComputedStyle(paper).transform
      const settled =
        transform === 'none' ||
        /^matrix\(1, 0, 0, 1, 0, 0\)$/.test(transform)
      if (!settled) {
        throw new Error(`menu still transforming: ${transform}`)
      }
    })

    const dividers = Array.from(menu.children).filter(
      (child): child is HTMLElement => child.tagName === 'HR',
    )
    // One after the notification centre, one before the organization group,
    // one before sign-out.
    expect(dividers).toHaveLength(3)
    dividers.forEach((divider, index) => {
      const style = getComputedStyle(divider)
      expect(style.marginTop, `divider ${index + 1} margin-top`).toBe(
        `${rhythm}px`,
      )
      expect(style.marginBottom, `divider ${index + 1} margin-bottom`).toBe(
        `${rhythm}px`,
      )
    })

    const group = menu.querySelector('[role="group"]')
    const lastDivider = dividers[dividers.length - 1]
    if (!(group instanceof HTMLElement) || lastDivider === undefined) {
      throw new Error('the menu has no organization group or last divider')
    }
    const signOut = within(menu).getAllByRole('menuitem').at(-1)
    if (signOut === undefined) {
      throw new Error('the menu has no sign-out item')
    }

    const dividerRect = lastDivider.getBoundingClientRect()
    const groupToDivider =
      dividerRect.top - group.getBoundingClientRect().bottom
    const dividerToSignOut =
      signOut.getBoundingClientRect().top - dividerRect.bottom
    expect(
      Math.abs(groupToDivider - rhythm),
      `organization group to last divider gap: expected ${rhythm}px, measured ${px(groupToDivider)}px`,
    ).toBeLessThanOrEqual(TOLERANCE)
    expect(
      Math.abs(dividerToSignOut - rhythm),
      `last divider to sign-out gap: expected ${rhythm}px, measured ${px(dividerToSignOut)}px`,
    ).toBeLessThanOrEqual(TOLERANCE)
  })
})
