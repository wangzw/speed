/**
 * The divider rule at the theme layer: a horizontal Divider paints a solid
 * rule in both color modes, and a vertical one paints that rule on its
 * right edge.
 *
 * In light mode the rule comes from `palette.divider`. In dark mode that
 * token (neutral 700) is one step above the paper (neutral 800) and reads
 * as no line at 1px, so the rule is an ink wash -- `text.primary` at a low
 * alpha -- that stays translucent over a preset's own surfaces.
 *
 * The proof renders a real Divider and reads the computed border. MUI's
 * own root keeps a transparent background and zeroes every edge before
 * raising one with the CSS `thin` keyword; the assertion pins the resolved
 * colour and the width to something greater than zero, so an override that
 * left either at 0 or `transparent` fails. The dark case also composites
 * the rule over the paper and requires a WCAG contrast ratio of at least
 * 1.5, which the near-paper divider token does not reach.
 */

import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { ThemeProvider, alpha } from '@mui/material/styles'
import Divider from '@mui/material/Divider'
import { emittedStyleText } from '@speed/test-utils/emitted-css'
import { createAppTheme } from './createAppTheme.js'

/** The alpha the dark rule applies to `text.primary` (see divider.ts). */
const DARK_RULE_ALPHA = 0.16

/** The minimum paper-relative contrast the dark rule must reach. */
const MIN_DARK_RULE_CONTRAST = 1.5

/** The browser-normalized colour of `value` (hex, named, rgba, ...). */
function normalizedColor(value: string): string {
  const probe = document.createElement('div')
  probe.style.color = value
  document.body.appendChild(probe)
  const color = getComputedStyle(probe).color
  probe.remove()
  return color
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
  const mix = (fg: number, bg: number) => fg * foreground.a + bg * (1 - foreground.a)
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
  const [lighter, darker] = [relativeLuminance(first), relativeLuminance(second)].sort(
    (left, right) => right - left,
  ) as [number, number]
  return (lighter + 0.05) / (darker + 0.05)
}

/**
 * The declarations emotion emitted for the classes on `element`. The
 * divider's rule carries MUI's base declarations and the theme's override
 * in one block, so reading that block tells an explicit width apart from
 * MUI's own `thin` keyword.
 */
function declarationsOf(element: Element): string {
  const names = Array.from(element.classList)
  const bodies: string[] = []
  for (const rule of emittedStyleText().matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const [, selector = '', declarations = ''] = rule
    if (names.some((name) => selector.includes(`.${name}`))) {
      bodies.push(declarations)
    }
  }
  return bodies.join(';')
}

describe('theme divider', () => {
  for (const mode of ['light', 'dark'] as const) {
    it(`paints a visible horizontal rule (${mode})`, () => {
      const { theme } = createAppTheme(undefined, undefined, mode)
      // The rule comes from the theme's own override, not from MUI's
      // default component styles.
      expect(theme.components?.MuiDivider?.styleOverrides?.root).toBeTypeOf(
        'function',
      )
      const expectedColor =
        mode === 'dark'
          ? alpha(theme.palette.text.primary, DARK_RULE_ALPHA)
          : theme.palette.divider
      const { container } = render(
        <ThemeProvider theme={theme}>
          <Divider />
        </ThemeProvider>,
      )
      const rule = container.querySelector('hr') as HTMLElement
      const style = getComputedStyle(rule)
      expect(Number.parseFloat(style.borderBottomWidth)).toBeGreaterThan(0)
      expect(style.borderBottomStyle).toBe('solid')
      expect(style.borderBottomColor).toBe(normalizedColor(expectedColor))
      // The visible edge is the override's own width and colour, not
      // MUI's `thin` keyword or its default border colour.
      const declarations = declarationsOf(rule).replace(/\s+/g, '')
      expect(declarations).toContain('border-bottom-width:1px')
      expect(declarations).toContain(
        `border-color:${expectedColor.replace(/\s+/g, '')}`,
      )
    })
  }

  it('separates the dark rule from the paper perceptibly', () => {
    const { theme } = createAppTheme(undefined, undefined, 'dark')
    const { container } = render(
      <ThemeProvider theme={theme}>
        <Divider />
      </ThemeProvider>,
    )
    const rule = container.querySelector('hr') as HTMLElement
    // The rendered colour is translucent, so it is composited over the
    // paper before the ratio is taken: that is what the eye sees.
    const paper = parseColor(normalizedColor(theme.palette.background.paper))
    const drawn = parseColor(getComputedStyle(rule).borderBottomColor)
    const visible = compositeOver(drawn, paper)
    expect(contrastRatio(visible, paper)).toBeGreaterThanOrEqual(
      MIN_DARK_RULE_CONTRAST,
    )
  })

  it('paints the vertical rule on the right edge and clears the bottom edge', () => {
    const { theme } = createAppTheme()
    const { container } = render(
      <ThemeProvider theme={theme}>
        <Divider orientation="vertical" />
      </ThemeProvider>,
    )
    const rule = container.querySelector('.MuiDivider-root') as HTMLElement
    const style = getComputedStyle(rule)
    expect(Number.parseFloat(style.borderRightWidth)).toBeGreaterThan(0)
    expect(style.borderRightStyle).toBe('solid')
    expect(style.borderRightColor).toBe(
      normalizedColor(theme.palette.divider),
    )
    expect(Number.parseFloat(style.borderBottomWidth)).toBe(0)
    const declarations = declarationsOf(rule).replace(/\s+/g, '')
    expect(declarations).toContain('border-right-width:1px')
    expect(declarations).toContain('border-bottom-width:0')
  })
})
