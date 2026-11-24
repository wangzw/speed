
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { createTheme, ThemeProvider } from '@mui/material/styles'
import { enUS } from '@mui/material/locale'
import AppBar from '@mui/material/AppBar'
import Card from '@mui/material/Card'
import Dialog, { dialogClasses } from '@mui/material/Dialog'
import Drawer, { drawerClasses } from '@mui/material/Drawer'
import { defaultTokens } from '@speed/tokens'
import type { TokensOverride } from '@speed/tokens'
import { emittedStyleText } from '@speed/test-utils/emitted-css'
import { createAppTheme } from './createAppTheme.js'
import { POINTER_COARSE_QUERY, TOUCH_TARGET_MIN_PX } from '../internal/pointer.js'
import { touchFloorComponents } from './touch-floor.js'
import { mergeThemeComponents, surfaceComponents } from './surface.js'

const UNIQUE_FILL = '#123456'

describe('theme.surface resolution', () => {
  it('resolves every light surface through the palette slots', () => {
    const { theme } = createAppTheme()
    expect(theme.surface.page.fill).toBe(theme.palette.background.default)
    expect(theme.surface.container.fill).toBe(theme.palette.background.paper)
    expect(theme.surface.overlay.fill).toBe(theme.palette.background.paper)
    expect(theme.surface.container.borderColor).toBe(theme.palette.divider)
    expect(theme.surface.page.borderColor).toBe(theme.palette.divider)
    expect(theme.surface.overlay.borderColor).toBe(theme.palette.divider)
    expect(theme.surface.header.borderColor).toBe(theme.palette.divider)
    expect(theme.surface.container.borderWidth).toBe(
      defaultTokens.surface.container.borderWidth,
    )
    expect(theme.surface.overlay.borderWidth).toBe(0)
    expect(theme.surface.page.borderWidth).toBe(0)
    expect(theme.surface.header.borderWidth).toBe(0)
    expect(theme.surface.header.fill).toBe(defaultTokens.color.neutral[50])
  })

  it('resolves every dark surface from the dark end of the ramp', () => {
    const { theme } = createAppTheme(undefined, undefined, 'dark')
    expect(theme.surface.page.fill).toBe(theme.palette.background.default)
    expect(theme.surface.container.fill).toBe(theme.palette.background.paper)
    expect(theme.surface.overlay.fill).toBe(theme.palette.background.paper)
    expect(theme.surface.container.borderColor).toBe(theme.palette.divider)
    expect(theme.surface.header.fill).toBe(defaultTokens.color.neutral[900])
  })

  it('follows an overridden background.paper with no surface override', () => {
    const { theme } = createAppTheme({
      color: { background: { paper: UNIQUE_FILL } },
    })
    expect(theme.surface.container.fill).toBe(UNIQUE_FILL)
    expect(theme.surface.overlay.fill).toBe(UNIQUE_FILL)
  })

  it('carries declared token values onto theme.surface', () => {
    const { theme } = createAppTheme({
      surface: {
        container: {
          fill: UNIQUE_FILL,
          borderColor: '#654321',
          borderWidth: 2,
          elevation: 4,
        },
        overlay: {
          fill: 'rgba(1, 2, 3, 0.5)',
          backdropFilter: 'blur(8px)',
        },
      },
    })
    expect(theme.surface.container).toMatchObject({
      fill: UNIQUE_FILL,
      borderColor: '#654321',
      borderWidth: 2,
      elevation: 4,
    })
    expect(theme.surface.overlay.fill).toBe('rgba(1, 2, 3, 0.5)')
    expect(theme.surface.overlay.backdropFilter).toBe('blur(8px)')
    expect(theme.surface.overlay.elevation).toBeUndefined()
    expect(theme.surface.container.backdropFilter).toBeUndefined()
  })

  it('takes the dark border from borderColorDark when one is given', () => {
    const tokens = {
      surface: {
        container: { borderColor: '#111111', borderColorDark: '#EEEEEE' },
      },
    }
    const light = createAppTheme(tokens)
    const dark = createAppTheme(tokens, undefined, 'dark')
    expect(light.theme.surface.container.borderColor).toBe('#111111')
    expect(dark.theme.surface.container.borderColor).toBe('#EEEEEE')
  })

  it('serves both modes from borderColor when no dark border is given', () => {
    const tokens = { surface: { container: { borderColor: '#222222' } } }
    expect(createAppTheme(tokens).theme.surface.container.borderColor).toBe('#222222')
    expect(
      createAppTheme(tokens, undefined, 'dark').theme.surface.container.borderColor,
    ).toBe('#222222')
  })

  it('survives the locale merge AppThemeProvider performs', () => {
    const { theme } = createAppTheme()
    const localized = createTheme(theme, enUS)
    expect(localized.surface).toEqual(theme.surface)
  })
})

describe('surface fill opacity', () => {
  it('applies the token alpha to the resolved fill in both color modes', () => {
    const override: TokensOverride = {
      surface: { container: { fill: '#123456', opacity: 0.5 } },
    }
    expect(createAppTheme(override).theme.surface.container.fill).toBe(
      'rgba(18, 52, 86, 0.5)',
    )
    expect(
      createAppTheme(override, undefined, 'dark').theme.surface.container.fill,
    ).toBe('rgba(18, 52, 86, 0.5)')
  })

  it('applies the alpha to an inherited fill as well', () => {
    const override: TokensOverride = {
      surface: { container: { opacity: 0.5 } },
    }
    expect(createAppTheme(override).theme.surface.container.fill).toBe(
      'rgba(255, 255, 255, 0.5)',
    )
    expect(
      createAppTheme(override, undefined, 'dark').theme.surface.container.fill,
    ).toBe('rgba(30, 41, 59, 0.5)')
  })

  it('leaves an opaque alpha and non-hex fills untouched', () => {
    const opaque = createAppTheme({ surface: { page: { opacity: 1 } } }).theme
    expect(opaque.surface.page.fill).toBe(opaque.palette.background.default)
    const nonHex = createAppTheme({
      surface: { overlay: { fill: 'rgba(1, 2, 3, 0.5)', opacity: 0.4 } },
    }).theme
    expect(nonHex.surface.overlay.fill).toBe('rgba(1, 2, 3, 0.5)')
  })

  it('paints the alpha fill on the component override', () => {
    const card = renderCard({
      surface: { container: { fill: '#123456', opacity: 0.5 } },
    })
    const carriers = classesDeclaringBackground(
      emittedStyleText(),
      'rgba(18, 52, 86, 0.5)',
    )
    expect(carriers.some((name) => card.classList.contains(name))).toBe(true)
  })
})

function renderCard(projectTokens?: TokensOverride): Element {
  const { theme } = createAppTheme(projectTokens)
  render(
    <ThemeProvider theme={theme}>
      <Card variant="outlined">card</Card>
    </ThemeProvider>,
  )
  return screen.getByText('card')
}

function classesDeclaringBackground(css: string, fill: string): string[] {
  const names: string[] = []
  const normalizedFill = fill.replace(/\s+/g, '')
  for (const rule of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const [, selector = '', declarations = ''] = rule
    if (!declarations.replace(/\s+/g, '').includes(`background-color:${normalizedFill}`)) {
      continue
    }
    for (const name of selector.matchAll(/\.([A-Za-z0-9_-]+)/g)) {
      const className = name[1]
      if (className !== undefined) names.push(className)
    }
  }
  return names
}

describe('surface component overrides', () => {
  it('paints the resolved container fill on an outlined Card', () => {
    const card = renderCard({ surface: { container: { fill: UNIQUE_FILL } } })
    const carriers = classesDeclaringBackground(emittedStyleText(), UNIQUE_FILL)
    expect(carriers.some((name) => card.classList.contains(name))).toBe(true)
  })

  it('does not paint the container surface on an elevated Card', () => {
    const { theme } = createAppTheme({
      surface: { container: { fill: UNIQUE_FILL } },
    })
    render(
      <ThemeProvider theme={theme}>
        <Card variant="elevation">elevated card</Card>
      </ThemeProvider>,
    )
    const card = screen.getByText('elevated card')
    const carriers = classesDeclaringBackground(emittedStyleText(), UNIQUE_FILL)
    expect(carriers.some((name) => card.classList.contains(name))).toBe(false)
  })

  it('gives the app bar the container fill and a bottom divider', () => {
    const containerFill = '#56789a'
    const { theme } = createAppTheme({
      surface: { container: { fill: containerFill } },
    })
    render(
      <ThemeProvider theme={theme}>
        <AppBar position="static" color="inherit">
          app bar
        </AppBar>
      </ThemeProvider>,
    )
    const bar = screen.getByRole('banner')
    const carriers = classesDeclaringBackground(
      emittedStyleText(),
      containerFill,
    )
    expect(carriers.some((name) => bar.classList.contains(name))).toBe(true)
    expect(bar).toHaveStyle({
      borderBottomStyle: 'solid',
      borderBottomColor: theme.surface.container.borderColor,
      borderBottomWidth: `${theme.surface.container.borderWidth}px`,
    })
  })

  it('does not redefine MuiPaper.root', () => {
    const { theme } = createAppTheme()
    expect(theme.components?.MuiPaper).toBeUndefined()
  })

  it('leaves TableContainer without a surface', () => {
    const { theme } = createAppTheme()
    expect(surfaceComponents?.MuiTableContainer).toBeUndefined()
    expect(theme.components?.MuiTableContainer).toBeUndefined()
  })

  it('paints the overlay surface on a Dialog paper', () => {
    const overlayFill = '#234567'
    const { theme } = createAppTheme({
      surface: { overlay: { fill: overlayFill } },
    })
    render(
      <ThemeProvider theme={theme}>
        <Dialog open>dialog body</Dialog>
      </ThemeProvider>,
    )
    const paper = document.querySelector(`.${dialogClasses.paper}`)
    expect(paper).not.toBeNull()
    const carriers = classesDeclaringBackground(
      emittedStyleText(),
      overlayFill,
    )
    expect(
      carriers.some((name) => (paper as Element).classList.contains(name)),
    ).toBe(true)
  })

  it('gives a permanent drawer the container surface and a temporary one the overlay surface', () => {
    const containerFill = '#345678'
    const overlayFill = '#456789'
    const { theme } = createAppTheme({
      surface: {
        container: { fill: containerFill },
        overlay: { fill: overlayFill },
      },
    })
    const { unmount } = render(
      <ThemeProvider theme={theme}>
        <Drawer variant="permanent" open>
          permanent drawer
        </Drawer>
      </ThemeProvider>,
    )
    const permanentPaper = document.querySelector(`.${drawerClasses.paper}`) as Element
    const css = emittedStyleText()
    expect(
      classesDeclaringBackground(css, containerFill).some((name) =>
        permanentPaper.classList.contains(name),
      ),
    ).toBe(true)
    expect(
      classesDeclaringBackground(css, overlayFill).some((name) =>
        permanentPaper.classList.contains(name),
      ),
    ).toBe(false)
    unmount()

    render(
      <ThemeProvider theme={theme}>
        <Drawer variant="temporary" open>
          temporary drawer
        </Drawer>
      </ThemeProvider>,
    )
    const temporaryPaper = document.querySelector(`.${drawerClasses.paper}`) as Element
    const temporaryCss = emittedStyleText()
    expect(
      classesDeclaringBackground(temporaryCss, overlayFill).some((name) =>
        temporaryPaper.classList.contains(name),
      ),
    ).toBe(true)
    expect(
      classesDeclaringBackground(temporaryCss, containerFill).some((name) =>
        temporaryPaper.classList.contains(name),
      ),
    ).toBe(false)
  })

  it('runs a standing drawer edge to edge with one divider, not a card rim', () => {
    const { theme } = createAppTheme({
      surface: {
        container: { fill: '#345678', borderColor: '#abcdef', borderWidth: 2 },
        overlay: { fill: '#456789', borderWidth: 1 },
      },
    })
    const { unmount } = render(
      <ThemeProvider theme={theme}>
        <Drawer variant="permanent" open>
          permanent drawer
        </Drawer>
      </ThemeProvider>,
    )
    const permanent = getComputedStyle(
      document.querySelector(`.${drawerClasses.paper}`) as Element,
    )
    expect(permanent.borderRadius).toBe('0px')
    expect(permanent.borderRightWidth).toBe('2px')
    expect(permanent.borderTopWidth).toBe('0px')
    expect(permanent.borderLeftWidth).toBe('0px')
    expect(permanent.borderBottomWidth).toBe('0px')
    unmount()

    render(
      <ThemeProvider theme={theme}>
        <Drawer variant="temporary" open>
          temporary drawer
        </Drawer>
      </ThemeProvider>,
    )
    const temporary = getComputedStyle(
      document.querySelector(`.${drawerClasses.paper}`) as Element,
    )
    expect(temporary.borderTopWidth).toBe('1px')
    expect(temporary.borderRightWidth).toBe('1px')
  })
})

describe('component map merge', () => {
  it('keeps every touch-floor override alongside the surface overrides', () => {
    const merged = mergeThemeComponents(touchFloorComponents, surfaceComponents)
    const buttonRoot = merged?.MuiButton?.styleOverrides?.root as
      | Record<string, unknown>
      | undefined
    expect(buttonRoot?.[POINTER_COARSE_QUERY]).toMatchObject({
      minWidth: TOUCH_TARGET_MIN_PX,
      minHeight: TOUCH_TARGET_MIN_PX,
    })
    expect(merged?.MuiCard).toEqual(surfaceComponents?.MuiCard)
    expect(merged?.MuiTableCell).toEqual(surfaceComponents?.MuiTableCell)
  })

  it('merges two sources at the same slot instead of replacing it', () => {
    const merged = mergeThemeComponents(
      { MuiCard: { styleOverrides: { root: { color: 'red' } } } },
      { MuiCard: { styleOverrides: { root: { backgroundColor: 'blue' } } } },
    )
    expect(merged?.MuiCard?.styleOverrides?.root).toEqual({
      color: 'red',
      backgroundColor: 'blue',
    })
  })

  it('composes two style callbacks at the same slot', () => {
    const merged = mergeThemeComponents(
      { MuiCard: { styleOverrides: { root: () => ({ color: 'red' }) } } },
      {
        MuiCard: {
          styleOverrides: { root: () => ({ backgroundColor: 'blue' }) },
        },
      },
    )
    const root = merged?.MuiCard?.styleOverrides?.root as
      | ((props: unknown) => unknown)
      | undefined
    expect(root?.({})).toEqual([{ color: 'red' }, { backgroundColor: 'blue' }])
  })

  it('concatenates theme variants declared by different sources', () => {
    const merged = mergeThemeComponents(
      { MuiCard: { variants: [{ props: { variant: 'outlined' }, style: {} }] } },
      { MuiCard: { variants: [{ props: { variant: 'elevation' }, style: {} }] } },
    )
    expect(merged?.MuiCard?.variants).toHaveLength(2)
  })
})
