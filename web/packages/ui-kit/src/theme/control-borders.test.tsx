/**
 * The control-boundary contract: an outlined input's resting border reads
 * `palette.text.secondary`, and an outlined button's resting border reads
 * `currentColor` -- its own text colour -- at full opacity.
 *
 * MUI's default resting input border is 23% of the foreground, which
 * lands near 2:1 on this theme's surfaces -- below the 3:1 WCAG 1.4.11
 * asks of a control boundary. The input's proof reads the stylesheet a
 * real TextField emits (see @speed/test-utils/emitted-css), because the
 * color is carried by a theme callback rather than by the component's own
 * markup.
 *
 * MUI's outlined button border is `alpha(main, 0.5)`, which measures
 * roughly 2.0-2.7:1 against the card surface on every preset but glass.
 * The override writes `currentColor`, so the boundary follows whichever
 * colour role the button carries (primary, secondary, error, inherit)
 * instead of forcing primary onto every one of them.
 *
 * jsdom does not substitute CSS `var()` in a computed value, and MUI
 * paints the variant colour as `color: var(--variant-outlinedColor)`.
 * The button's computed `color` therefore stays a `var()` expression and
 * its `currentColor` border resolves to transparent. The tests split the
 * proof accordingly: a literal or inherited text colour is checked
 * through `getComputedStyle`, while a palette role is checked through the
 * emitted rule -- the custom property carries that role's text colour and
 * the last top-level `border-color` in the same rule is `currentColor`.
 */

import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { ThemeProvider } from '@mui/material/styles'
import Button from '@mui/material/Button'
import TextField from '@mui/material/TextField'
import type { Theme } from '@mui/material/styles'
import { themePresets } from '@speed/theme-presets'
import { emittedStyleText } from '@speed/test-utils/emitted-css'
import { createAppTheme } from './createAppTheme.js'
import type { AppThemeMode } from './createAppTheme.js'

/** The browser-normalized colour of `value` (hex, named, rgba, ...). */
function normalizedColor(value: string): string {
  const probe = document.createElement('div')
  probe.style.color = value
  document.body.appendChild(probe)
  const color = getComputedStyle(probe).color
  probe.remove()
  return color
}

/** `theme`'s outlined-button override, read through its callback.
 *
 * The slot composes every source that fills it, so the callback returns
 * an array of contributions; the boundary's own contribution is the one
 * declaring `borderColor`.
 */
function outlinedOverride(theme: Theme): { borderColor?: unknown } {
  const styleOverrides = theme.components?.MuiButton?.styleOverrides
  const outlined = styleOverrides?.outlined
  if (typeof outlined !== 'function') {
    throw new Error('MuiButton.styleOverrides.outlined is not a theme callback')
  }
  const resolve = outlined as unknown as (props: {
    theme: Theme
    ownerState: Record<string, unknown>
  }) => { borderColor?: unknown } | ReadonlyArray<{ borderColor?: unknown }>
  const resolved = resolve({ theme, ownerState: {} })
  const contributions = Array.isArray(resolved) ? resolved : [resolved]
  const boundary = contributions.find(
    (contribution) => contribution.borderColor !== undefined,
  )
  if (boundary === undefined) {
    throw new Error('MuiButton.styleOverrides.outlined lost its border color')
  }
  return boundary
}

/**
 * The declaration bodies of the emitted rules whose selector names one of
 * `button`'s emotion classes. Emotion gives each rendered instance its
 * own class, so a body read here belongs to this button.
 */
function ruleBodiesFor(button: Element): string[] {
  const css = emittedStyleText()
  return Array.from(button.classList)
    .filter((className) => className.startsWith('css-'))
    .flatMap((className) => {
      const marker = `.${className}{`
      const bodies: string[] = []
      let cursor = 0
      for (;;) {
        const start = css.indexOf(marker, cursor)
        if (start < 0) return bodies
        const open = start + marker.length - 1
        let depth = 0
        let end = open
        for (; end < css.length; end += 1) {
          if (css[end] === '{') depth += 1
          else if (css[end] === '}') {
            depth -= 1
            if (depth === 0) break
          }
        }
        bodies.push(css.slice(open + 1, end))
        cursor = end + 1
      }
    })
}

/** The top-level `border-color` values in a rule body, in source order. */
function topLevelBorderColors(body: string): string[] {
  return Array.from(body.matchAll(/border-color:([^;{}]+)/g)).map((match) =>
    (match[1] ?? '').trim(),
  )
}

describe('theme control boundaries', () => {
  for (const mode of ['light', 'dark'] as const) {
    it(`paints the outlined input's resting border in text.secondary (${mode})`, () => {
      const { theme } = createAppTheme(undefined, undefined, mode as AppThemeMode)
      render(
        <ThemeProvider theme={theme}>
          <TextField label="field" />
        </ThemeProvider>,
      )
      expect(emittedStyleText()).toContain(theme.palette.text.secondary)
    })
  }

  for (const mode of ['light', 'dark'] as const) {
    it(`paints a primary outlined button's border in its own text colour (${mode})`, () => {
      const { theme } = createAppTheme(undefined, undefined, mode as AppThemeMode)
      const primaryText = theme.palette.primary.main

      // Pin the text colour to a literal so jsdom can resolve currentColor;
      // the border itself still comes from the theme's outlined rule.
      const { getByRole } = render(
        <ThemeProvider theme={theme}>
          <Button variant="outlined" sx={{ color: primaryText }}>
            action
          </Button>
        </ThemeProvider>,
      )
      const button = getByRole('button', { name: 'action' })
      const computed = getComputedStyle(button)
      expect(normalizedColor(computed.borderTopColor)).toBe(
        normalizedColor(computed.color),
      )
      expect(normalizedColor(computed.borderTopColor)).toBe(
        normalizedColor(primaryText),
      )
      expect(outlinedOverride(theme).borderColor).toBe('currentColor')
    })
  }

  for (const mode of ['light', 'dark'] as const) {
    it(`resolves an inherit outlined button's border to its inherited text colour (${mode})`, () => {
      const { theme } = createAppTheme(undefined, undefined, mode as AppThemeMode)
      const inheritedText = 'rgb(12, 34, 56)'

      const { getByRole } = render(
        <ThemeProvider theme={theme}>
          <div style={{ color: inheritedText }}>
            <Button variant="outlined" color="inherit">
              action
            </Button>
          </div>
        </ThemeProvider>,
      )
      const button = getByRole('button', { name: 'action' })
      const computed = getComputedStyle(button)
      expect(normalizedColor(computed.borderTopColor)).toBe(
        normalizedColor(computed.color),
      )
      expect(normalizedColor(computed.borderTopColor)).toBe(
        normalizedColor(inheritedText),
      )
      expect(normalizedColor(computed.borderTopColor)).not.toBe(
        normalizedColor(theme.palette.primary.main),
      )
      expect(outlinedOverride(theme).borderColor).toBe('currentColor')
    })
  }

  for (const mode of ['light', 'dark'] as const) {
    it(`keeps a secondary outlined button's border on secondary, not primary (${mode})`, () => {
      const { theme } = createAppTheme(undefined, undefined, mode as AppThemeMode)

      const { getByRole } = render(
        <ThemeProvider theme={theme}>
          <Button variant="outlined" color="secondary">
            action
          </Button>
        </ThemeProvider>,
      )
      const button = getByRole('button', { name: 'action' })
      // jsdom exposes the cascaded custom property but does not substitute
      // it into computed `color`; this is the text colour MUI paints.
      expect(
        getComputedStyle(button).getPropertyValue('--variant-outlinedColor'),
      ).toBe(theme.palette.secondary.main)
      expect(theme.palette.secondary.main).not.toBe(theme.palette.primary.main)

      const variantBody = ruleBodiesFor(button).find((body) =>
        body.includes('--variant-outlinedColor'),
      )
      expect(variantBody).toBeDefined()
      const borderColors = topLevelBorderColors(variantBody ?? '')
      expect(borderColors).toContain(
        'var(--variant-outlinedBorder, currentColor)',
      )
      expect(borderColors[borderColors.length - 1]).toBe('currentColor')
      expect(outlinedOverride(theme).borderColor).toBe('currentColor')
    })
  }

  for (const preset of themePresets) {
    for (const mode of ['light', 'dark'] as const) {
      it(`keeps the outlined button border on currentColor at full opacity on the ${preset.id} preset (${mode})`, () => {
        const { theme } = createAppTheme(preset.tokens, undefined, mode)

        const { getByRole } = render(
          <ThemeProvider theme={theme}>
            <Button variant="outlined">action</Button>
          </ThemeProvider>,
        )
        const button = getByRole('button', { name: 'action' })
        expect(
          getComputedStyle(button).getPropertyValue('--variant-outlinedColor'),
        ).toBe(theme.palette.primary.main)

        const variantBody = ruleBodiesFor(button).find((body) =>
          body.includes('--variant-outlinedColor'),
        )
        expect(variantBody).toBeDefined()
        const borderColors = topLevelBorderColors(variantBody ?? '')
        expect(borderColors).toContain(
          'var(--variant-outlinedBorder, currentColor)',
        )
        expect(borderColors[borderColors.length - 1]).toBe('currentColor')
        expect(outlinedOverride(theme).borderColor).toBe('currentColor')
      })
    }
  }
})
