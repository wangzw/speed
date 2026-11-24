
import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { ThemeProvider, alpha } from '@mui/material/styles'
import Divider from '@mui/material/Divider'
import { emittedStyleText } from '@speed/test-utils/emitted-css'
import { createAppTheme } from './createAppTheme.js'

const DARK_RULE_ALPHA = 0.16

const MIN_DARK_RULE_CONTRAST = 1.5

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

function parseColor(value: string): Rgba {
  const match = /rgba?\(([^)]+)\)/.exec(value)
  if (match === null) {
    throw new Error(`unparsed colour: ${value}`)
  }
  const parts = (match[1] ?? '').split(/[,\s/]+/).filter(Boolean).map(Number)
  const [r = 0, g = 0, b = 0, a = 1] = parts
  return { r, g, b, a }
}

function compositeOver(foreground: Rgba, background: Rgba): Rgba {
  const mix = (fg: number, bg: number) => fg * foreground.a + bg * (1 - foreground.a)
  return {
    r: mix(foreground.r, background.r),
    g: mix(foreground.g, background.g),
    b: mix(foreground.b, background.b),
    a: 1,
  }
}

function linearChannel(channel: number): number {
  const value = channel / 255
  return value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
}

function relativeLuminance(color: Rgba): number {
  return (
    0.2126 * linearChannel(color.r) +
    0.7152 * linearChannel(color.g) +
    0.0722 * linearChannel(color.b)
  )
}

function contrastRatio(first: Rgba, second: Rgba): number {
  const [lighter, darker] = [relativeLuminance(first), relativeLuminance(second)].sort(
    (left, right) => right - left,
  ) as [number, number]
  return (lighter + 0.05) / (darker + 0.05)
}

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
