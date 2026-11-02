/**
 * The account menu's divider and geometry across every shipped theme preset,
 * in both color modes, in Chromium.
 *
 * jsdom carries the class names and the inline styles but lays nothing out,
 * so the geometry the account bar's contract states -- the menu's inset from
 * the navigation column, its width, the icon and avatar tracks, and the two
 * gaps around the account row -- can only be told apart here. The same is
 * true of the divider colour and the paper surface: a preset restyles them
 * through tokens, and the menu has to follow.
 *
 * Every preset/mode combination is its own case so the matrix reports per
 * combination. A case measures every criterion before it fails and lists the
 * ones that missed, so one bad criterion does not hide the rest.
 */

import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { page } from 'vitest/browser'
import { themePresets } from '@speed/theme-presets'
import { createAppTheme } from '@speed/ui-kit'
import { renderDemoApp } from './test-utils/render.js'
import {
  authenticatedSnapshot,
  createTestSession,
} from './test-utils/session.js'

/** The account bar's geometry contract, in CSS pixels. */
const EXPECTED = {
  menuLeftInset: 9,
  menuWidth: 238,
  avatarInset: 16,
  /** The navigation icon box's left edge, from the column's edge. */
  itemIconInset: 16,
  rowBottomGap: 12,
  dividerRowGap: 12,
} as const

/** The tolerance the contract allows on a geometric measurement. */
const TOLERANCE = 1

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
 * Records a divider that is not the visible 1px solid rule in the theme's
 * divider colour. A zero width, a non-solid style or a transparent/other
 * colour all mean the rule would not read.
 */
function checkDivider(
  issues: string[],
  divider: Element,
  label: string,
  dividerColor: string,
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
}

describe('the account menu across theme presets', () => {
  for (const preset of themePresets) {
    for (const mode of COLOR_MODES) {
      it(`places its dividers and geometry under ${preset.id} / ${mode}`, async () => {
        await page.viewport(1280, 900)
        const label = `${preset.id} / ${mode}`
        const theme = createAppTheme(preset.tokens, undefined, mode).theme
        const dividerColor = normalizedColor(theme.palette.divider)
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
        checkDivider(issues, rowDivider, `${label}: row divider`, dividerColor)

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
        // margin is part of the contract) and keeps the fixed width.
        const menuLeftInset = paperRect.left - columnRect.left
        if (Math.abs(menuLeftInset - EXPECTED.menuLeftInset) > TOLERANCE) {
          issues.push(
            `${label}: menu left edge: expected ${EXPECTED.menuLeftInset}px ±${TOLERANCE}px, measured ${px(menuLeftInset)}px (computed margin-left ${paperStyle.marginLeft})`,
          )
        }
        checkClose(
          issues,
          `${label}: menu width`,
          paperRect.width,
          EXPECTED.menuWidth,
        )

        // Every entry's glyph box starts on the navigation's icon track: a
        // 16px inset from the column's edge, measured on the box that holds
        // the glyph (not the glyph's own centred edge).
        const items = within(menu).getAllByRole('menuitem')
        for (const item of items) {
          const iconBox = item.firstElementChild
          if (!(iconBox instanceof HTMLElement)) {
            issues.push(`${label}: menu entry has no icon box`)
            continue
          }
          checkClose(
            issues,
            `${label}: item icon inset (${item.textContent ?? ''})`,
            iconBox.getBoundingClientRect().left - columnContentLeft,
            EXPECTED.itemIconInset,
          )
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
        if (paperStyle.borderTopColor !== dividerColor) {
          issues.push(
            `${label}: menu border colour is ${paperStyle.borderTopColor}, theme divider is ${dividerColor}`,
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
