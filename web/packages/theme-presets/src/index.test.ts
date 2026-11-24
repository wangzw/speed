import { describe, expect, it } from 'vitest'
import { defaultTokens, deepMerge } from '@speed/tokens'
import type { SpeedTokens } from '@speed/tokens'
import {
  asciiThemePreset,
  classicThemePreset,
  compactThemePreset,
  defaultThemePreset,
  glassThemePreset,
  modernThemePreset,
  themePresets,
} from './index'
import type { ThemePresetTokens } from './index'

type ColorMode = 'light' | 'dark'

const ROLES = ['page', 'container', 'overlay', 'header'] as const

const forbiddenBreakpoints: ThemePresetTokens = {
  // @ts-expect-error breakpoints are a layout contract, not a style
  breakpoints: { values: { md: 900 } },
}
const forbiddenZIndex: ThemePresetTokens = {
  // @ts-expect-error z-index is a stacking contract, not a style
  zIndex: { values: { drawer: 1400 } },
}
void forbiddenBreakpoints
void forbiddenZIndex

interface Rgb {
  readonly r: number
  readonly g: number
  readonly b: number
}

function parseHex(hex: string): Rgb {
  const match = /^#([0-9a-fA-F]{6})$/.exec(hex)
  if (match === null) {
    throw new Error(`expected a #RRGGBB color, got ${hex}`)
  }
  const value = Number.parseInt(match[1]!.slice(0, 6), 16)
  return {
    r: (value >> 16) & 0xff,
    g: (value >> 8) & 0xff,
    b: value & 0xff,
  }
}

function composite(fill: Rgb, opacity: number, backdrop: Rgb): Rgb {
  const blend = (front: number, back: number): number =>
    Math.round(opacity * front + (1 - opacity) * back)
  return {
    r: blend(fill.r, backdrop.r),
    g: blend(fill.g, backdrop.g),
    b: blend(fill.b, backdrop.b),
  }
}

function linear(channel: number): number {
  const value = channel / 255
  return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4
}

function luminance(color: Rgb): number {
  return 0.2126 * linear(color.r) + 0.7152 * linear(color.g) + 0.0722 * linear(color.b)
}

function contrastRatio(first: Rgb, second: Rgb): number {
  const a = luminance(first)
  const b = luminance(second)
  const lighter = Math.max(a, b)
  const darker = Math.min(a, b)
  return (lighter + 0.05) / (darker + 0.05)
}

function inheritedFill(tokens: SpeedTokens, mode: ColorMode, role: (typeof ROLES)[number]): string {
  if (mode === 'light') {
    if (role === 'page') return tokens.color.background.default
    if (role === 'header') return tokens.color.neutral[50]
    return tokens.color.background.paper
  }
  if (role === 'page' || role === 'header') return tokens.color.neutral[900]
  return tokens.color.neutral[800]
}

function bodyText(tokens: SpeedTokens, mode: ColorMode): string {
  return mode === 'light' ? tokens.color.text.primary : tokens.color.neutral[50]
}

function secondaryText(tokens: SpeedTokens, mode: ColorMode): string {
  return mode === 'light' ? tokens.color.text.secondary : tokens.color.neutral[300]
}

function semanticText(
  tokens: SpeedTokens,
  mode: ColorMode,
  role: keyof SpeedTokens['color']['semantic'],
): string {
  const tone = tokens.color.semantic[role]
  return mode === 'light' ? tone.main : tone.light
}

function backdropStops(tokens: SpeedTokens, mode: ColorMode): Rgb[] {
  const page = parseHex(inheritedFill(tokens, mode, 'page'))
  const canvas = tokens.color.background.canvas
  if (canvas === undefined) {
    return [page]
  }
  return [parseHex(canvas[mode].from), parseHex(canvas[mode].to), page]
}

function surfaceBodies(tokens: SpeedTokens, mode: ColorMode, role: (typeof ROLES)[number]): Rgb[] {
  const canvas = tokens.color.background.canvas
  if (role === 'page' && canvas !== undefined) {
    return [parseHex(canvas[mode].from), parseHex(canvas[mode].to)]
  }
  const surface = tokens.surface[role]
  if (surface.fill === 'transparent') {
    return surfaceBodies(tokens, mode, role === 'header' ? 'container' : 'page')
  }
  const fill = parseHex(surface.fill ?? inheritedFill(tokens, mode, role))
  const opacity = surface.opacity
  return opacity === undefined || opacity >= 1
    ? [fill]
    : backdropStops(tokens, mode).map((stop) => composite(fill, opacity, stop))
}

function splitLayers(value: string): string[] {
  const layers: string[] = []
  let depth = 0
  let current = ''
  for (const char of value) {
    if (char === '(') depth += 1
    if (char === ')') depth -= 1
    if (char === ',' && depth === 0) {
      layers.push(current)
      current = ''
      continue
    }
    current += char
  }
  layers.push(current)
  return layers
}

function isTextShadow(value: string): boolean {
  const length = '(?:0|-?\\d+(?:\\.\\d+)?px)'
  const layer = new RegExp(
    `^${length}\\s+${length}(?:\\s+${length})?\\s+(?:#[0-9a-fA-F]{3,8}|rgba?\\([^)]*\\)|[a-zA-Z]+)$`,
  )
  return splitLayers(value).every((part) => layer.test(part.trim()))
}

describe('the shipped shelf', () => {
  it('ships default, classic, modern, compact, glass and ascii, baseline first', () => {
    expect(themePresets.map((preset) => preset.id)).toEqual([
      'default',
      'classic',
      'modern',
      'compact',
      'glass',
      'ascii',
    ])
    expect(themePresets[0]).toBe(defaultThemePreset)
  })

  it('uses unique ids', () => {
    const ids = themePresets.map((preset) => preset.id)
    expect(new Set(ids).size).toBe(ids.length)
  })

  it('ships the baseline as an empty diff', () => {
    expect(defaultThemePreset.tokens).toEqual({})
  })

  it('freezes the shelf and every preset', () => {
    expect(Object.isFrozen(themePresets)).toBe(true)
    for (const preset of themePresets) {
      expect(Object.isFrozen(preset)).toBe(true)
      expect(Object.isFrozen(preset.tokens)).toBe(true)
    }
  })
})

describe('the allowed override surface', () => {
  it('never carries breakpoints or zIndex', () => {
    for (const preset of themePresets) {
      expect('breakpoints' in preset.tokens).toBe(false)
      expect('zIndex' in preset.tokens).toBe(false)
    }
  })

  it('leaves the breakpoints and zIndex branches shared with the defaults', () => {
    for (const preset of themePresets) {
      const tokens = deepMerge(defaultTokens, preset.tokens)
      expect(tokens.breakpoints).toBe(defaultTokens.breakpoints)
      expect(tokens.zIndex).toBe(defaultTokens.zIndex)
    }
  })
})

describe('every preset', () => {
  it('merges onto the default tree into a complete token tree without throwing', () => {
    for (const preset of themePresets) {
      const build = (): SpeedTokens => deepMerge(defaultTokens, preset.tokens)
      expect(build, preset.id).not.toThrow()
      const tokens = build()
      for (const section of [
        'color',
        'typography',
        'spacing',
        'shape',
        'breakpoints',
        'zIndex',
        'shadows',
        'surface',
      ] as const) {
        expect(tokens[section], `${preset.id}.${section}`).toBeDefined()
      }
    }
  })
})

describe('classic and modern are visibly distinct', () => {
  it('differs from the default and from each other in radius, shadows and leading', () => {
    const classic = deepMerge(defaultTokens, classicThemePreset.tokens)
    const modern = deepMerge(defaultTokens, modernThemePreset.tokens)

    expect(classic.shape.borderRadius).not.toBe(defaultTokens.shape.borderRadius)
    expect(modern.shape.borderRadius).not.toBe(defaultTokens.shape.borderRadius)
    expect(classic.shape.borderRadius).not.toBe(modern.shape.borderRadius)

    expect(classic.shadows).not.toEqual(defaultTokens.shadows)
    expect(modern.shadows).not.toEqual(defaultTokens.shadows)
    expect(classic.shadows).not.toEqual(modern.shadows)

    expect(classic.typography.lineHeight).not.toEqual(defaultTokens.typography.lineHeight)
    expect(modern.typography.lineHeight).not.toEqual(defaultTokens.typography.lineHeight)
    expect(classic.typography.lineHeight).not.toEqual(modern.typography.lineHeight)

    expect(classic.spacing.unit).toBeLessThan(defaultTokens.spacing.unit)
    expect(modern.spacing.unit).toBeGreaterThan(defaultTokens.spacing.unit)
    expect(classic.color.semantic.primary.main).not.toBe(
      modern.color.semantic.primary.main,
    )
  })
})

describe('compact', () => {
  it('shrinks the type scale, tightens leading and drops the spacing base to 6', () => {
    const tokens = deepMerge(defaultTokens, compactThemePreset.tokens)
    expect(tokens.spacing.unit).toBe(6)
    expect(tokens.typography.fontSize.md).toBeLessThan(defaultTokens.typography.fontSize.md)
    expect(tokens.typography.lineHeight.normal).toBeLessThan(
      defaultTokens.typography.lineHeight.normal,
    )
  })
})

describe('glass', () => {
  it('makes container and overlay translucent, blurred and softly elevated', () => {
    for (const role of ['container', 'overlay'] as const) {
      const surface = glassThemePreset.tokens.surface?.[role]
      expect(surface?.backdropFilter, role).toBeTruthy()
      expect(surface?.opacity, role).toBeLessThan(1)
    }
  })

  it('gives the raised surfaces a border that reads on either page', () => {    for (const role of ['container', 'overlay'] as const) {
      const surface = glassThemePreset.tokens.surface?.[role]
      expect(surface?.borderColor, role).toBeTruthy()
      expect(surface?.borderColorDark, role).toBeTruthy()
      expect(surface?.borderColor, role).not.toBe(surface?.borderColorDark)
    }
  })

  it('ships both stops of the canvas gradient for both modes', () => {    const canvas = glassThemePreset.tokens.color?.background?.canvas
    for (const mode of ['light', 'dark'] as const) {
      expect(canvas?.[mode]?.from, mode).toMatch(/^#[0-9a-fA-F]{6}$/)
      expect(canvas?.[mode]?.to, mode).toMatch(/^#[0-9a-fA-F]{6}$/)
    }
  })
})

describe('ascii', () => {
  const ascii = deepMerge(defaultTokens, asciiThemePreset.tokens)

  it('leads with the monospace stack and keeps a CJK fallback before the generic family', () => {
    const monoFamilies = defaultTokens.typography.fontFamily.mono.replace(/,\s*monospace\s*$/, '')
    const sans = ascii.typography.fontFamily.sans
    expect(sans.startsWith(monoFamilies)).toBe(true)
    expect(sans).toContain("'PingFang SC'")
    expect(sans).toContain("'Hiragino Sans GB'")
    expect(sans.trim().endsWith('monospace')).toBe(true)
  })

  it('drops every radius and every shadow', () => {
    expect(ascii.shape.borderRadius).toBe(0)
    expect(Object.values(ascii.shadows)).toEqual(['none', 'none', 'none', 'none', 'none', 'none'])
  })

  it('gives every surface role a hard green rule and inherits its fill', () => {
    for (const role of ['page', 'container', 'overlay', 'header'] as const) {
      const surface = ascii.surface[role]
      expect(surface.fill, role).toBeUndefined()
      expect(surface.borderWidth, role).toBe(1)
      expect(surface.borderColor, role).toBe('#16A34A')
    }
  })

  it('paints the two modes as different terminal papers', () => {
    const lightPage = parseHex(inheritedFill(ascii, 'light', 'page'))
    const darkPage = parseHex(inheritedFill(ascii, 'dark', 'page'))
    expect(luminance(lightPage)).toBeGreaterThan(0.8)
    expect(luminance(darkPage)).toBeLessThan(0.02)
    expect(luminance(parseHex(ascii.color.text.primary))).toBeLessThan(0.05)
    expect(luminance(parseHex(ascii.color.neutral[50]))).toBeGreaterThan(0.7)
  })

  it('carries a legal text glow', () => {
    expect(ascii.typography.textGlow).toBeDefined()
    expect(isTextShadow(ascii.typography.textGlow!)).toBe(true)
  })
})

describe('contrast', () => {
  const SEMANTIC_ROLES = [
    'primary',
    'secondary',
    'error',
    'warning',
    'info',
    'success',
  ] as const

  function worstOn(tokens: SpeedTokens, mode: ColorMode, color: string): number {
    const ratios = ROLES.flatMap((role) =>
      surfaceBodies(tokens, mode, role).map((body) =>
        contrastRatio(parseHex(color), body),
      ),
    )
    return Math.min(...ratios)
  }

  for (const preset of themePresets) {
    it(`${preset.id} keeps body text at 4.5:1 on every surface in both modes`, () => {
      const tokens = deepMerge(defaultTokens, preset.tokens)
      for (const mode of ['light', 'dark'] as const) {
        for (const [label, color] of [
          ['primary', bodyText(tokens, mode)],
          ['secondary', secondaryText(tokens, mode)],
        ] as const) {
          const ratios = ROLES.flatMap((role) =>
            surfaceBodies(tokens, mode, role).map((body) =>
              contrastRatio(parseHex(color), body),
            ),
          )
          expect(
            Math.min(...ratios),
            `${preset.id}: ${mode} mode, ${label} text`,
          ).toBeGreaterThanOrEqual(4.5)
        }
      }
    })

    it(`${preset.id} keeps semantic text roles readable in both modes`, () => {
      const tokens = deepMerge(defaultTokens, preset.tokens)
      for (const mode of ['light', 'dark'] as const) {
        for (const role of SEMANTIC_ROLES) {
          expect(
            worstOn(tokens, mode, semanticText(tokens, mode, role)),
            `${preset.id}: ${mode} mode, ${role} text`,
          ).toBeGreaterThanOrEqual(4.5)
        }
      }
    })

    it(`${preset.id} keeps filled-control labels at 4.5:1`, () => {
      const tokens = deepMerge(defaultTokens, preset.tokens)
      for (const role of SEMANTIC_ROLES) {
        expect(
          contrastRatio(
            parseHex(tokens.color.semantic[role].contrastText),
            parseHex(semanticText(tokens, 'light', role)),
          ),
          `${preset.id}: light mode, ${role} filled label`,
        ).toBeGreaterThanOrEqual(4.5)
        expect(
          contrastRatio(
            parseHex(tokens.color.neutral[900]),
            parseHex(semanticText(tokens, 'dark', role)),
          ),
          `${preset.id}: dark mode, ${role} filled label`,
        ).toBeGreaterThanOrEqual(4.5)
      }
    })

    it(`${preset.id} keeps the focus ring at 3:1 on every surface`, () => {
      const tokens = deepMerge(defaultTokens, preset.tokens)
      for (const mode of ['light', 'dark'] as const) {
        expect(
          worstOn(tokens, mode, semanticText(tokens, mode, 'primary')),
          `${preset.id}: ${mode} mode focus ring`,
        ).toBeGreaterThanOrEqual(3)
      }
    })
  }
})
