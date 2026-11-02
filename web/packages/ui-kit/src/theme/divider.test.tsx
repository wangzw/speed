/**
 * The divider rule at the theme layer: a horizontal Divider paints a solid
 * rule from `palette.divider` in both color modes, and a vertical one
 * paints that rule on its right edge.
 *
 * The proof renders a real Divider and reads the computed border. MUI's
 * own root keeps a transparent background and zeroes every edge before
 * raising one with the CSS `thin` keyword; the assertion pins the resolved
 * colour to `theme.palette.divider` and the width to something greater
 * than zero, so an override that left either at 0 or `transparent` fails.
 */

import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { ThemeProvider } from '@mui/material/styles'
import Divider from '@mui/material/Divider'
import { emittedStyleText } from '@speed/test-utils/emitted-css'
import { createAppTheme } from './createAppTheme.js'

/** The browser-normalized colour of `value` (hex, named, rgba, ...). */
function normalizedColor(value: string): string {
  const probe = document.createElement('div')
  probe.style.color = value
  document.body.appendChild(probe)
  const color = getComputedStyle(probe).color
  probe.remove()
  return color
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
    it(`paints a visible horizontal rule from palette.divider (${mode})`, () => {
      const { theme } = createAppTheme(undefined, undefined, mode)
      // The rule comes from the theme's own override, not from MUI's
      // default component styles.
      expect(theme.components?.MuiDivider?.styleOverrides?.root).toBeTypeOf(
        'function',
      )
      const { container } = render(
        <ThemeProvider theme={theme}>
          <Divider />
        </ThemeProvider>,
      )
      const rule = container.querySelector('hr') as HTMLElement
      const style = getComputedStyle(rule)
      expect(Number.parseFloat(style.borderBottomWidth)).toBeGreaterThan(0)
      expect(style.borderBottomStyle).toBe('solid')
      expect(style.borderBottomColor).toBe(
        normalizedColor(theme.palette.divider),
      )
      // The visible edge is the override's own width and colour, not
      // MUI's `thin` keyword or its default border colour.
      const declarations = declarationsOf(rule).replace(/\s+/g, '')
      expect(declarations).toContain('border-bottom-width:1px')
      expect(declarations).toContain(`border-color:${theme.palette.divider}`)
    })
  }

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
