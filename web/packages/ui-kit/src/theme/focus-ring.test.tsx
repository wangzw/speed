
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ThemeProvider } from '@mui/material/styles'
import Button from '@mui/material/Button'
import Link from '@mui/material/Link'
import type { Theme } from '@mui/material/styles'
import { emittedStyleText } from '@speed/test-utils/emitted-css'
import { createAppTheme } from './createAppTheme.js'
import { focusRingComponents } from './focus-ring.js'
import { touchFloorComponents } from './touch-floor.js'
import { mergeThemeComponents } from './surface.js'
import { POINTER_COARSE_QUERY, TOUCH_TARGET_MIN_PX } from '../internal/pointer.js'

function classesWithFocusOutline(css: string, outline: string): string[] {
  const names: string[] = []
  for (const rule of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const [, selector = '', declarations = ''] = rule
    if (!selector.includes('Mui-focusVisible')) continue
    if (!declarations.includes(outline)) continue
    for (const name of selector.matchAll(/\.([A-Za-z0-9_-]+)/g)) {
      const className = name[1]
      if (className !== undefined) names.push(className)
    }
  }
  return names
}

describe('theme focus ring', () => {
  it('draws a 2px primary outline on ButtonBase and Link in the focus-visible state', () => {
    const { theme } = createAppTheme()
    render(
      <ThemeProvider theme={theme}>
        <Button>focusable button</Button>
        <Link href="#target">focusable link</Link>
      </ThemeProvider>,
    )
    const outline = `2px solid ${theme.palette.primary.main}`
    const css = emittedStyleText()
    const carriers = classesWithFocusOutline(css, outline)
    expect(carriers.length).toBeGreaterThan(0)
    expect(css).toContain('outline-offset:2px')
    const button = screen.getByRole('button', { name: 'focusable button' })
    const link = screen.getByRole('link', { name: 'focusable link' })
    expect(carriers.some((name) => button.classList.contains(name))).toBe(true)
    expect(carriers.some((name) => link.classList.contains(name))).toBe(true)
  })

  it('keeps the touch-target floor when both sources fill the ButtonBase and Link root slots', () => {
    const merged = mergeThemeComponents(touchFloorComponents, focusRingComponents)
    const { theme } = createAppTheme()
    for (const component of ['MuiButtonBase', 'MuiLink'] as const) {
      const root = merged?.[component]?.styleOverrides?.root as
        | ((props: { theme: Theme }) => unknown)
        | undefined
      expect(typeof root).toBe('function')
      const contributions = root?.({ theme }) as unknown[]
      expect(contributions).toHaveLength(2)
      expect(contributions[0]).toMatchObject({
        [POINTER_COARSE_QUERY]: {
          minWidth: TOUCH_TARGET_MIN_PX,
          minHeight: TOUCH_TARGET_MIN_PX,
        },
      })
      expect(contributions[1]).toEqual({
        '&.Mui-focusVisible': {
          outline: `2px solid ${theme.palette.primary.main}`,
          outlineOffset: 2,
        },
      })
    }
  })
})
