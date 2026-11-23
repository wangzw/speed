/**
 * The page baseline contract: the body paints the page surface and
 * carries the two optional page values (typography glow and canvas
 * gradient) for the active mode, alongside the other component layers.
 */

import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import CssBaseline from '@mui/material/CssBaseline'
import { createTheme, ThemeProvider } from '@mui/material/styles'
import type { CSSInterpolation, Theme } from '@mui/material/styles'
import { enUS } from '@mui/material/locale'
import { emittedStyleText } from '@speed/test-utils/emitted-css'
import { createAppTheme } from './createAppTheme.js'
import { POINTER_COARSE_QUERY, TOUCH_TARGET_MIN_PX } from '../internal/pointer.js'

type BodyStyle = Record<string, unknown>

/** The body declarations the baseline emits, read through its callback. */
function bodyStyle(theme: Theme): BodyStyle {
  const overrides = theme.components?.MuiCssBaseline?.styleOverrides
  if (typeof overrides !== 'function') {
    throw new Error('MuiCssBaseline.styleOverrides is not a theme callback')
  }
  const styles = (overrides as (theme: Theme) => CSSInterpolation)(theme) as {
    body?: BodyStyle
  }
  if (styles.body === undefined) {
    throw new Error('MuiCssBaseline body slot is missing')
  }
  return styles.body
}

describe('page baseline', () => {
  it('paints the page surface on the body with no optional declarations by default', () => {
    const { theme } = createAppTheme()
    const body = bodyStyle(theme)
    expect(body.backgroundColor).toBe(theme.surface.page.fill)
    expect(body.backgroundColor).toBe(theme.palette.background.default)
    expect('textShadow' in body).toBe(false)
    expect('backgroundImage' in body).toBe(false)
    expect('backgroundAttachment' in body).toBe(false)
  })

  it('paints a translucent page fill resolved through the role opacity', () => {
    const { theme } = createAppTheme({
      surface: { page: { fill: '#112233', opacity: 0.4 } },
    })
    expect(bodyStyle(theme).backgroundColor).toBe('rgba(17, 34, 51, 0.4)')
  })

  it('maps the typography glow onto the body text shadow', () => {
    const glow = '0 0 6px rgba(74, 222, 128, 0.8)'
    const { theme } = createAppTheme({ typography: { textGlow: glow } })
    expect(bodyStyle(theme).textShadow).toBe(glow)
  })

  it('paints the active mode canvas stops as a fixed gradient', () => {
    const canvas = {
      light: { from: '#F8FAFC', to: '#DBEAFE' },
      dark: { from: '#020617', to: '#0F172A' },
    }
    const light = createAppTheme({
      color: { background: { canvas } },
    }).theme
    expect(bodyStyle(light).backgroundImage).toBe(
      'linear-gradient(160deg, #F8FAFC, #DBEAFE)',
    )
    expect(bodyStyle(light).backgroundAttachment).toBe('fixed')

    const dark = createAppTheme(
      { color: { background: { canvas } } },
      undefined,
      'dark',
    ).theme
    expect(bodyStyle(dark).backgroundImage).toBe(
      'linear-gradient(160deg, #020617, #0F172A)',
    )
    expect(bodyStyle(dark).backgroundAttachment).toBe('fixed')
  })

  it('emits the body declarations through the rendered baseline', () => {
    const glow = '0 0 6px #4ADE80'
    const canvas = {
      light: { from: '#F8FAFC', to: '#DBEAFE' },
      dark: { from: '#020617', to: '#0F172A' },
    }
    const { theme } = createAppTheme({
      surface: { page: { fill: '#112233', opacity: 0.4 } },
      typography: { textGlow: glow },
      color: { background: { canvas } },
    })
    render(
      <ThemeProvider theme={theme}>
        <CssBaseline />
      </ThemeProvider>,
    )
    const css = emittedStyleText().replace(/\s+/g, '')
    expect(css).toContain('background-color:rgba(17,34,51,0.4)')
    expect(css).toContain(`text-shadow:${glow.replace(/\s+/g, '')}`)
    expect(css).toContain(
      `background-image:linear-gradient(160deg,${canvas.light.from},${canvas.light.to})`,
    )
    expect(css).toContain('background-attachment:fixed')
  })

  it('survives the locale merge AppThemeProvider performs', () => {
    const glow = '0 0 2px #4ADE80'
    const { theme } = createAppTheme({ typography: { textGlow: glow } })
    const localized = createTheme(theme, enUS)
    expect(bodyStyle(localized).textShadow).toBe(glow)
    expect(bodyStyle(localized).backgroundColor).toBe(
      localized.surface.page.fill,
    )
  })

  it('coexists with the touch floor, focus ring, surface and reduced-motion layers', () => {
    const { theme } = createAppTheme({
      typography: { textGlow: '0 0 2px #4ADE80' },
    })
    // The touch floor is static on the slot and the unavailable appearance
    // is a callback; the merge composes both into one callback.
    const buttonRoot = theme.components?.MuiButton?.styleOverrides?.root as
      | ((props: { theme: Theme }) => unknown[])
      | undefined
    expect(typeof buttonRoot).toBe('function')
    const contributions = buttonRoot?.({ theme }) ?? []
    expect(contributions[0]).toMatchObject({
      [POINTER_COARSE_QUERY]: {
        minWidth: TOUCH_TARGET_MIN_PX,
        minHeight: TOUCH_TARGET_MIN_PX,
      },
    })
    // The focus ring composes with the static touch floor into one
    // callback on the shared slot.
    expect(theme.components?.MuiButtonBase?.styleOverrides?.root).toBeTypeOf(
      'function',
    )
    expect(theme.components?.MuiCard).toBeDefined()
    expect(theme.components?.MuiSkeleton).toBeDefined()
    expect(theme.components?.MuiCssBaseline).toBeDefined()
  })
})
