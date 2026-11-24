import { createTheme } from '@mui/material/styles'
import { describe, expect, it } from 'vitest'
import { defaultTokens, deepMerge, type TokensOverride } from './index'

const HEX_COLOR = /^#[0-9A-F]{6}$/i

function relativeLuminance(hex: string): number {
  const value = hex.slice(1)
  const linear = (offset: number): number => {
    const channel = Number.parseInt(value.slice(offset, offset + 2), 16) / 255
    return channel <= 0.03928
      ? channel / 12.92
      : ((channel + 0.055) / 1.055) ** 2.4
  }
  return 0.2126 * linear(0) + 0.7152 * linear(2) + 0.0722 * linear(4)
}

function contrastRatio(a: string, b: string): number {
  const brighter = Math.max(relativeLuminance(a), relativeLuminance(b))
  const darker = Math.min(relativeLuminance(a), relativeLuminance(b))
  return (brighter + 0.05) / (darker + 0.05)
}

const DEFAULT_BEFORE = JSON.stringify(defaultTokens)
const PRISTINE_ERROR_MAIN = defaultTokens.color.semantic.error.main

describe('defaultTokens', () => {
  it('ships every semantic role with main, light, dark and contrastText hex colors', () => {
    const roles = [
      'primary',
      'secondary',
      'error',
      'warning',
      'info',
      'success',
    ] as const
    for (const role of roles) {
      const color = defaultTokens.color.semantic[role]
      for (const key of ['main', 'light', 'dark', 'contrastText'] as const) {
        expect(HEX_COLOR.test(color[key]), `${role}.${key}`).toBe(true)
      }
    }
  })

  it('ships the full neutral ramp as hex colors, darkest at 950', () => {
    const tones = [50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 950] as const
    for (const tone of tones) {
      const value = defaultTokens.color.neutral[tone]
      expect(HEX_COLOR.test(value), `neutral.${tone}`).toBe(true)
    }
    expect(defaultTokens.color.neutral[50]).toBe('#F8FAFC')
    expect(defaultTokens.color.neutral[950]).toBe('#020617')
  })

  it('ships the text, background and divider colors as hex literals', () => {
    for (const value of Object.values(defaultTokens.color.text)) {
      expect(HEX_COLOR.test(value)).toBe(true)
    }
    for (const value of [
      defaultTokens.color.background.default,
      defaultTokens.color.background.paper,
    ]) {
      expect(HEX_COLOR.test(value)).toBe(true)
    }
    expect(HEX_COLOR.test(defaultTokens.color.divider)).toBe(true)
  })

  it('ships Latin-first font stacks that end in a CJK-capable fallback and a generic family', () => {
    const { sans, mono } = defaultTokens.typography.fontFamily
    expect(sans.startsWith('ui-sans-serif, system-ui')).toBe(true)
    expect(sans).toContain("'PingFang SC'")
    expect(sans).toContain("'Microsoft YaHei'")
    expect(sans.endsWith('sans-serif')).toBe(true)
    expect(mono).toContain('monospace')
    expect(mono.endsWith('monospace')).toBe(true)
  })

  it('ships a strictly increasing font-size scale of positive pixel numbers', () => {
    const steps = ['xs', 'sm', 'md', 'lg', 'xl', '2xl', '3xl', '4xl', '5xl'] as const
    const sizes = steps.map((step) => defaultTokens.typography.fontSize[step])
    for (let i = 0; i < sizes.length - 1; i += 1) {
      expect(sizes[i]).toBeGreaterThan(0)
      expect(sizes[i + 1]).toBeGreaterThan(sizes[i]!)
    }
    expect(defaultTokens.typography.fontSize.xs).toBe(12)
    expect(defaultTokens.typography.fontSize['5xl']).toBe(48)
  })

  it('ships the standard font weights and unitless line heights above 1', () => {
    expect(defaultTokens.typography.fontWeight).toEqual({
      regular: 400,
      medium: 500,
      semibold: 600,
      bold: 700,
    })
    for (const value of Object.values(defaultTokens.typography.lineHeight)) {
      expect(value).toBeGreaterThan(1)
    }
    for (const value of Object.values(defaultTokens.typography.letterSpacing)) {
      expect(value.length).toBeGreaterThan(0)
    }
  })

  it('ships an 8px spacing unit and a single 8px border radius', () => {
    expect(defaultTokens.spacing.unit).toBe(8)
    expect(defaultTokens.shape.borderRadius).toBe(8)
  })

  it('ships the MUI breakpoint keys and values unchanged', () => {
    const { values } = createTheme({}).breakpoints
    expect(defaultTokens.breakpoints.values).toEqual(values)
  })

  it('ships the MUI z-index slots with the MUI default values', () => {
    const mui = createTheme({})
    expect(defaultTokens.zIndex.values).toEqual(mui.zIndex)
  })

  it('anchors the spacing and border-radius claims on the installed MUI theme', () => {
    const mui = createTheme({})
    expect(mui.spacing(1)).toBe(`${defaultTokens.spacing.unit}px`)
    expect(mui.shape.borderRadius).toBe(4)
    expect(defaultTokens.shape.borderRadius).not.toBe(mui.shape.borderRadius)
  })

  it('ships the four surface roles with the documented defaults', () => {
    expect(Object.keys(defaultTokens.surface).sort()).toEqual([
      'container',
      'header',
      'overlay',
      'page',
    ])
    expect(defaultTokens.surface).toEqual({
      page: {},
      container: { borderWidth: 1 },
      overlay: {},
      header: {},
    })
  })

  it('carries no surface color literals: an omitted field inherits its slot', () => {
    for (const role of Object.values(defaultTokens.surface)) {
      expect('fill' in role).toBe(false)
      expect('borderColor' in role).toBe(false)
      expect('elevation' in role).toBe(false)
      expect('backdropFilter' in role).toBe(false)
    }
    expect(defaultTokens.surface.container.borderWidth).toBe(1)
  })

  it('ships a layered rgba box-shadow for every elevation slot', () => {
    const elevations = [1, 2, 4, 8, 16, 24] as const
    for (const elevation of elevations) {
      const shadow = defaultTokens.shadows[elevation]
      expect(shadow.length).toBeGreaterThan(0)
      expect(shadow).toContain('rgba(')
      expect(shadow).toContain('px')
    }
    expect(Object.keys(defaultTokens.shadows).map(Number).sort((a, b) => a - b)).toEqual([
      1, 2, 4, 8, 16, 24,
    ])
  })

  it('accepts partial project overrides and rejects shape drift at compile time', () => {
    const override: TokensOverride = {
      color: { semantic: { primary: { main: '#000000' } } },
      zIndex: { values: { drawer: 1300 } },
    }
    const merged = deepMerge(defaultTokens, override)
    expect(merged.color.semantic.primary.main).toBe('#000000')
    expect(merged.color.semantic.primary.light).toBe(
      defaultTokens.color.semantic.primary.light,
    )
    expect(merged.zIndex.values.drawer).toBe(1300)

    // @ts-expect-error unknown token sections are not overridable
    deepMerge(defaultTokens, { palette: { primary: { main: '#000000' } } })
    // @ts-expect-error token field types are enforced
    deepMerge(defaultTokens, { shape: { borderRadius: '8px' } })
    // @ts-expect-error overriding with an object where a string belongs is a shape error
    deepMerge(defaultTokens, { color: { divider: { main: '#000000' } } })
    // @ts-expect-error surface border widths are numbers, not CSS strings
    deepMerge(defaultTokens, { surface: { container: { borderWidth: '1px' } } })
  })
})

describe('defaultTokens optional effect fields', () => {
  it('leaves fill opacity, text glow and the canvas gradient absent by default', () => {
    for (const role of Object.values(defaultTokens.surface)) {
      expect('opacity' in role).toBe(false)
    }
    expect('textGlow' in defaultTokens.typography).toBe(false)
    expect('canvas' in defaultTokens.color.background).toBe(false)
  })

  it('accepts the optional fields as deep overrides without replacing their sections', () => {
    const glow = '0 0 6px rgba(74, 222, 128, 0.8)'
    const merged = deepMerge(defaultTokens, {
      surface: { overlay: { fill: '#101820', opacity: 0.6 } },
      typography: { textGlow: glow },
      color: {
        background: {
          canvas: {
            light: { from: '#F8FAFC', to: '#DBEAFE' },
            dark: { from: '#020617', to: '#0F172A' },
          },
        },
      },
    })
    expect(merged.surface.overlay).toEqual({ fill: '#101820', opacity: 0.6 })
    expect(merged.typography.textGlow).toBe(glow)
    expect(merged.color.background.canvas).toEqual({
      light: { from: '#F8FAFC', to: '#DBEAFE' },
      dark: { from: '#020617', to: '#0F172A' },
    })
    expect(merged.color.background.default).toBe(defaultTokens.color.background.default)
    expect(merged.surface.container.borderWidth).toBe(1)
  })

  it('rejects a partial canvas override at compile time', () => {
    deepMerge(defaultTokens, {
      color: {
        background: {
          // @ts-expect-error the canvas needs both modes
          canvas: { light: { from: '#FFFFFF', to: '#FFFFFF' } },
        },
      },
    })
    deepMerge(defaultTokens, {
      color: {
        background: {
          canvas: {
            // @ts-expect-error every stop pair needs both ends
            light: { from: '#FFFFFF' },
            dark: { from: '#020617', to: '#0F172A' },
          },
        },
      },
    })
    deepMerge(defaultTokens, {
      // @ts-expect-error an absent canvas is valid; a partial one is not
      color: { background: { canvas: { light: { from: '#FFFFFF' } } } },
    })
  })

  it('enforces the optional fields at compile time', () => {
    deepMerge(defaultTokens, { surface: { page: { opacity: 0.5 } } })
    // @ts-expect-error opacity is a number, not a CSS string
    deepMerge(defaultTokens, { surface: { page: { opacity: '0.5' } } })
    // @ts-expect-error the glow is a CSS text-shadow string
    deepMerge(defaultTokens, { typography: { textGlow: 1 } })
    deepMerge(defaultTokens, {
      // @ts-expect-error canvas stops are color strings
      color: { background: { canvas: { light: { from: 1 } } } },
    })
  })
})

describe('defaultTokens light-mode contrast', () => {
  const roles = [
    'primary',
    'secondary',
    'error',
    'warning',
    'info',
    'success',
  ] as const

  it('keeps every semantic role label at 4.5:1 or better against its main', () => {
    for (const role of roles) {
      const { main, contrastText } = defaultTokens.color.semantic[role]
      expect(contrastRatio(contrastText, main), role).toBeGreaterThanOrEqual(4.5)
    }
  })
})

describe('defaultTokens surface freeze', () => {
  it('deep-freezes the surface roles assembled into the default tree', () => {
    expect(Object.isFrozen(defaultTokens.surface)).toBe(true)
    expect(Object.isFrozen(defaultTokens.surface.page)).toBe(true)
    expect(Object.isFrozen(defaultTokens.surface.container)).toBe(true)
    const writable = defaultTokens as unknown as {
      surface: { page: { fill?: string } }
    }
    expect(() => {
      writable.surface.page.fill = '#000000'
    }).toThrow(TypeError)
  })
})

describe('defaultTokens shared-branch write enforcement', () => {
  it('throws when a write would reach the frozen defaults through a shared branch', () => {
    const merged = deepMerge(defaultTokens, {
      color: { semantic: { primary: { main: '#000000' } } },
    })
    const writable = merged as unknown as {
      color: { semantic: { error: { main: string } } }
      typography: { fontFamily: { sans: string } }
    }
    const writeDeepSharedBranch = (): void => {
      writable.color.semantic.error.main = '#DEADBEEF'
    }
    const writeTopLevelSharedBranch = (): void => {
      writable.typography.fontFamily.sans = '"Helvetica Neue"'
    }
    expect(writeDeepSharedBranch).toThrow(TypeError)
    expect(writeTopLevelSharedBranch).toThrow(TypeError)
  })

  it('keeps the defaults byte-identical after a write attempt through a shared branch', () => {
    const merged = deepMerge(defaultTokens, {
      color: { semantic: { primary: { main: '#000000' } } },
    })
    const writable = merged as unknown as {
      color: { semantic: { error: { main: string } } }
    }
    let attemptThrew = false
    try {
      writable.color.semantic.error.main = '#DEADBEEF'
    } catch {
      attemptThrew = true
    }
    expect(defaultTokens.color.semantic.error.main).toBe(PRISTINE_ERROR_MAIN)
    expect(JSON.stringify(defaultTokens)).toBe(DEFAULT_BEFORE)
    expect(attemptThrew).toBe(true)
  })

  it('reads only its own values in a later independent merge after a write attempt', () => {
    const merged = deepMerge(defaultTokens, {
      color: { semantic: { primary: { main: '#000000' } } },
    })
    const writable = merged as unknown as {
      color: { semantic: { error: { main: string } } }
    }
    let attemptThrew = false
    try {
      writable.color.semantic.error.main = '#DEADBEEF'
    } catch {
      attemptThrew = true
    }
    const tenantB = deepMerge(defaultTokens, { zIndex: { values: { modal: 1600 } } })
    expect(tenantB.zIndex.values.modal).toBe(1600)
    expect(tenantB.color.semantic.error.main).toBe(PRISTINE_ERROR_MAIN)
    expect(tenantB.typography.fontFamily.sans).toBe(
      defaultTokens.typography.fontFamily.sans,
    )
    expect(attemptThrew).toBe(true)
  })
})
