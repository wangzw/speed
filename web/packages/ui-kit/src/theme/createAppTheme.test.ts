/**
 * Theme-factory contract tests: the token-to-MUI mapping (every mapped
 * surface pinned against the default tokens), the three-layer override
 * semantics (later layers win per key, untouched branches stay shared),
 * copy-on-write (no input ever mutates), and hostile-override inertness.
 *
 * These run without a DOM: createAppTheme is a pure function.
 */

import { describe, expect, it } from 'vitest'
import { defaultTokens } from '@speed/tokens'
import type { TokensOverride } from '@speed/tokens'
import { createAppTheme } from './createAppTheme.js'
import { REDUCED_MOTION_QUERY } from './reduced-motion.js'

const DEFAULT_BEFORE = JSON.stringify(defaultTokens)
// Pristine snapshot taken at module load: the shared-branch enforcement
// tests prove the assembled defaults stay byte-identical no matter what a
// merged result's consumer tries to write through them.
const PRISTINE_ERROR_MAIN = defaultTokens.color.semantic.error.main

/** The six semantic roles every color-mode assertion sweeps. */
const SEMANTIC_ROLES = [
  'primary',
  'secondary',
  'error',
  'warning',
  'info',
  'success',
] as const

describe('createAppTheme token-to-theme mapping', () => {
  const { theme } = createAppTheme()

  it('maps every semantic role key for key onto the palette', () => {
    for (const role of ['primary', 'secondary', 'error', 'warning', 'info', 'success'] as const) {
      const token = defaultTokens.color.semantic[role]
      const paletteRole = theme.palette[role]
      expect(paletteRole.main).toBe(token.main)
      expect(paletteRole.light).toBe(token.light)
      expect(paletteRole.dark).toBe(token.dark)
      expect(paletteRole.contrastText).toBe(token.contrastText)
    }
    expect(theme.palette.mode).toBe('light')
  })

  it('aliases the neutral ramp onto the grey scale, same-number steps and A-tones', () => {
    const neutral = defaultTokens.color.neutral
    for (const tone of [50, 100, 200, 300, 400, 500, 600, 700, 800, 900] as const) {
      expect(theme.palette.grey[tone]).toBe(neutral[tone])
    }
    // MUI's own aliasing: A-tones repeat the same-number step.
    expect(theme.palette.grey.A100).toBe(neutral[100])
    expect(theme.palette.grey.A200).toBe(neutral[200])
    expect(theme.palette.grey.A400).toBe(neutral[400])
    expect(theme.palette.grey.A700).toBe(neutral[700])
    // Tone 950 has no MUI slot; it stays token-only.
    expect(Object.hasOwn(theme.palette.grey, 950)).toBe(false)
  })

  it('maps text, background and divider directly', () => {
    expect(theme.palette.text.primary).toBe(defaultTokens.color.text.primary)
    expect(theme.palette.text.secondary).toBe(defaultTokens.color.text.secondary)
    expect(theme.palette.text.disabled).toBe(defaultTokens.color.text.disabled)
    expect(theme.palette.background.default).toBe(defaultTokens.color.background.default)
    expect(theme.palette.background.paper).toBe(defaultTokens.color.background.paper)
    expect(theme.palette.divider).toBe(defaultTokens.color.divider)
  })

  it('uses the sans stack as fontFamily and the token weights as named weights', () => {
    expect(theme.typography.fontFamily).toBe(defaultTokens.typography.fontFamily.sans)
    expect(theme.typography.fontWeightRegular).toBe(defaultTokens.typography.fontWeight.regular)
    expect(theme.typography.fontWeightMedium).toBe(defaultTokens.typography.fontWeight.medium)
    expect(theme.typography.fontWeightBold).toBe(defaultTokens.typography.fontWeight.bold)
  })

  it("exposes the token tree's mono stack as fontFamilyMono", () => {
    expect(theme.typography.fontFamilyMono).toBe(
      defaultTokens.typography.fontFamily.mono,
    )
  })

  it("follows a project override of the mono stack", () => {
    const overridden = createAppTheme({
      typography: { fontFamily: { mono: "'Fira Code', monospace" } },
    }).theme
    expect(overridden.typography.fontFamilyMono).toBe("'Fira Code', monospace")
  })

  it('maps heading variants onto the 5xl..lg steps in rem, tight and semibold', () => {
    const sizes = defaultTokens.typography.fontSize
    const cases = [
      ['h1', sizes['5xl']],
      ['h2', sizes['4xl']],
      ['h3', sizes['3xl']],
      ['h4', sizes['2xl']],
      ['h5', sizes.xl],
      ['h6', sizes.lg],
    ] as const
    for (const [variant, px] of cases) {
      const style = theme.typography[variant]
      expect(style.fontSize).toBe(`${px / 16}rem`)
      expect(style.fontWeight).toBe(defaultTokens.typography.fontWeight.semibold)
      expect(style.lineHeight).toBe(defaultTokens.typography.lineHeight.tight)
    }
  })

  it('maps body/supporting variants with their documented roles', () => {
    const sizes = defaultTokens.typography.fontSize
    const weights = defaultTokens.typography.fontWeight
    const body1 = theme.typography.body1
    expect(body1.fontSize).toBe(`${sizes.md / 16}rem`)
    expect(body1.fontWeight).toBe(weights.regular)
    expect(body1.lineHeight).toBe(defaultTokens.typography.lineHeight.normal)
    expect(theme.typography.body2.fontSize).toBe(`${sizes.sm / 16}rem`)
    expect(theme.typography.subtitle1.fontWeight).toBe(weights.medium)
    expect(theme.typography.subtitle2.fontWeight).toBe(weights.medium)
    expect(theme.typography.button.fontWeight).toBe(weights.medium)
    expect(theme.typography.button.fontSize).toBe(`${sizes.sm / 16}rem`)
    expect(theme.typography.caption.fontSize).toBe(`${sizes.xs / 16}rem`)
    expect(theme.typography.caption.fontWeight).toBe(weights.regular)
    expect(theme.typography.overline.fontSize).toBe(`${sizes.xs / 16}rem`)
    expect(theme.typography.overline.fontWeight).toBe(weights.semibold)
    expect(theme.typography.overline.letterSpacing).toBe(defaultTokens.typography.letterSpacing.wide)
  })

  it('sets the spacing unit from the token (theme.spacing(n) === n x unit)', () => {
    expect(theme.spacing(1)).toBe('8px')
    expect(theme.spacing(2)).toBe('16px')
    expect(theme.spacing(0.5)).toBe('4px')
  })

  it('maps shape, breakpoints and zIndex values directly', () => {
    expect(theme.shape.borderRadius).toBe(defaultTokens.shape.borderRadius)
    expect(theme.breakpoints.values).toEqual(defaultTokens.breakpoints.values)
    expect(theme.zIndex.appBar).toBe(defaultTokens.zIndex.values.appBar)
    expect(theme.zIndex.modal).toBe(defaultTokens.zIndex.values.modal)
    expect(theme.zIndex.tooltip).toBe(defaultTokens.zIndex.values.tooltip)
  })

  it('builds a 25-entry shadow ramp with none at 0 and token slots floored below', () => {
    const shadows = theme.shadows
    expect(shadows).toHaveLength(25)
    expect(shadows[0]).toBe('none')
    const tokenShadows = defaultTokens.shadows
    expect(shadows[1]).toBe(tokenShadows[1])
    expect(shadows[2]).toBe(tokenShadows[2])
    expect(shadows[4]).toBe(tokenShadows[4])
    expect(shadows[8]).toBe(tokenShadows[8])
    expect(shadows[16]).toBe(tokenShadows[16])
    expect(shadows[24]).toBe(tokenShadows[24])
    // Unlisted elevations floor onto the nearest token slot at or below.
    expect(shadows[3]).toBe(tokenShadows[2])
    expect(shadows[5]).toBe(tokenShadows[4])
    expect(shadows[9]).toBe(tokenShadows[8])
    expect(shadows[20]).toBe(tokenShadows[16])
    // The ramp never dips: elevation growth is monotonic.
    for (let i = 2; i < shadows.length; i += 1) {
      expect(shadows[i]).not.toBe('none')
    }
  })
})

describe('createAppTheme reduced motion', () => {
  it('asks MUI to follow the operating system preference', () => {
    const { theme } = createAppTheme()
    expect(theme.motion.reducedMotion).toBe('system')
  })

  it('stops the skeleton at its root under reduced motion', () => {
    // Skeleton animates through keyframes rather than transitions, so the
    // theme states the gate on the component root as well as on motion.
    const { theme } = createAppTheme()
    const root = theme.components?.MuiSkeleton?.styleOverrides?.root as
      | Record<string, unknown>
      | undefined
    expect(root?.[REDUCED_MOTION_QUERY]).toEqual({ animation: 'none' })
  })
})

describe('createAppTheme color modes', () => {
  it('defaults to light: the token tree surface colors map directly', () => {
    const { theme } = createAppTheme()
    expect(theme.palette.mode).toBe('light')
    expect(theme.palette.background.default).toBe(
      defaultTokens.color.background.default,
    )
    expect(theme.palette.background.paper).toBe(
      defaultTokens.color.background.paper,
    )
    expect(theme.palette.text.primary).toBe(defaultTokens.color.text.primary)
  })

  it('reads the neutral ramp for surfaces in dark mode and the light tones for roles', () => {
    const { theme } = createAppTheme(undefined, undefined, 'dark')
    const neutral = defaultTokens.color.neutral
    expect(theme.palette.mode).toBe('dark')
    expect(theme.palette.background.default).toBe(neutral[900])
    expect(theme.palette.background.paper).toBe(neutral[800])
    expect(theme.palette.text.primary).toBe(neutral[50])
    expect(theme.palette.text.secondary).toBe(neutral[300])
    expect(theme.palette.text.disabled).toBe(neutral[500])
    expect(theme.palette.divider).toBe(neutral[700])
    // The semantic roles read their tonal family from the other end in
    // dark mode: main is the role's light tone (meant for dark surfaces)
    // and the label flips to the neutral ramp's dark end.
    for (const role of SEMANTIC_ROLES) {
      expect(theme.palette[role].main).toBe(defaultTokens.color.semantic[role].light)
      expect(theme.palette[role].contrastText).toBe(neutral[900])
    }
    // The neutral ramp maps onto grey identically in both modes.
    expect(theme.palette.grey[100]).toBe(neutral[100])
  })
})

/**
 * WCAG 2 relative luminance of an sRGB hex color, per the WCAG definition
 * (channels linearized, then weighted 0.2126/0.7152/0.0722).
 */
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

/** WCAG contrast ratio between two hex colors, from 1:1 to 21:1. */
function contrastRatio(a: string, b: string): number {
  const brighter = Math.max(relativeLuminance(a), relativeLuminance(b))
  const darker = Math.min(relativeLuminance(a), relativeLuminance(b))
  return (brighter + 0.05) / (darker + 0.05)
}

describe('createAppTheme dark-mode contrast', () => {
  // Every semantic role, measured against the surfaces it actually lands
  // on: a role's main is a fill/border/icon on the dark paper (non-text,
  // 3:1 floor) and its contrastText is the label inside that fill (text,
  // 4.5:1 floor).
  const { theme } = createAppTheme(undefined, undefined, 'dark')
  const paper = theme.palette.background.paper

  it('keeps every role main at 3:1 or better against the dark paper', () => {
    for (const role of SEMANTIC_ROLES) {
      expect(contrastRatio(theme.palette[role].main, paper)).toBeGreaterThanOrEqual(3)
    }
  })

  it('keeps every filled-control label at 4.5:1 or better against its fill', () => {
    for (const role of SEMANTIC_ROLES) {
      const { main, contrastText } = theme.palette[role]
      expect(contrastRatio(contrastText, main)).toBeGreaterThanOrEqual(4.5)
    }
  })

  it('keeps every filled-control label readable on the hover fill MUI paints', () => {
    // MUI's contained controls (Button, filled Chip, Fab) swap the fill to
    // palette[role].dark on hover while the same contrastText label stays
    // on top, so the dark slot is measured too, not just main.
    for (const role of SEMANTIC_ROLES) {
      const { main, dark, contrastText } = theme.palette[role]
      expect(contrastRatio(contrastText, dark)).toBeGreaterThanOrEqual(4.5)
      // The hover fill still steps away from the resting fill.
      expect(relativeLuminance(dark)).toBeGreaterThan(relativeLuminance(main))
    }
  })
})

describe('createAppTheme layering', () => {
  it('applies project tokens over the defaults and tenant overrides over the project', () => {
    const project: TokensOverride = {
      color: {
        semantic: {
          primary: { main: '#111111' },
          secondary: { main: '#222222' },
        },
      },
      spacing: { unit: 4 },
    }
    const tenant: TokensOverride = {
      color: { semantic: { primary: { main: '#333333' } } },
    }
    const { theme } = createAppTheme(project, tenant)
    expect(theme.palette.primary.main).toBe('#333333')
    expect(theme.palette.secondary.main).toBe('#222222')
    expect(theme.spacing(1)).toBe('4px')
    // Untouched branches keep the defaults.
    expect(theme.palette.error.main).toBe(defaultTokens.color.semantic.error.main)
  })

  it('returns the merged token tree alongside the theme', () => {
    const { tokens } = createAppTheme({ spacing: { unit: 12 } })
    expect(tokens.spacing.unit).toBe(12)
    expect(tokens.color.semantic.primary.main).toBe(
      defaultTokens.color.semantic.primary.main,
    )
  })

  it('shares untouched branches with defaultTokens by identity', () => {
    const { tokens } = createAppTheme()
    expect(tokens.color.semantic.error).toBe(defaultTokens.color.semantic.error)
    expect(tokens.typography.fontWeight).toBe(defaultTokens.typography.fontWeight)
    expect(tokens.color.semantic.primary).toBe(defaultTokens.color.semantic.primary)
    expect(tokens).not.toBe(defaultTokens)
  })

  it('never mutates its inputs, even through later writes into rebuilt branches', () => {
    const project: TokensOverride = { color: { semantic: { primary: { main: '#123456' } } } }
    const first = createAppTheme(project)
    expect(first.theme.palette.primary.main).toBe('#123456')
    // Branches the project layer rebuilt (primary here) are plain objects
    // owned by this result alone: a write there is local, unlike a write
    // through a shared branch, which the deep-frozen defaults refuse (see
    // the shared-branch write enforcement suite below). Prove the sources
    // stay byte-identical.
    const merged = first.tokens as unknown as {
      color: { semantic: { primary: { main: string } } }
    }
    merged.color.semantic.primary.main = '#DEADBEEF'
    expect(JSON.stringify(defaultTokens)).toBe(DEFAULT_BEFORE)
    expect(JSON.stringify(project)).toBe('{"color":{"semantic":{"primary":{"main":"#123456"}}}}')
  })

  it('keeps hostile __proto__ override keys inert', () => {
    const hostile = JSON.parse('{"__proto__": {"polluted": true}}') as TokensOverride
    const { tokens } = createAppTheme(hostile)
    expect(Object.getPrototypeOf(tokens)).toBe(Object.prototype)
    expect(({} as Record<string, unknown>).polluted).toBeUndefined()
    // The hostile key landed as an own property, neither dropped nor
    // merged into the prototype chain.
    expect(Object.prototype.hasOwnProperty.call(tokens, '__proto__')).toBe(true)
    // The theme itself stays sane.
    expect(JSON.stringify(defaultTokens)).toBe(DEFAULT_BEFORE)
  })
})

describe('createAppTheme shared-branch write enforcement', () => {
  it('throws when a write through an untouched branch would reach the frozen defaults', () => {
    const { tokens } = createAppTheme({
      color: { semantic: { primary: { main: '#123456' } } },
    })
    // Writable view: the cast stands in for the JS-consumer trigger channel
    // that bypasses the type layer's readonly seal (the same idiom the
    // "never mutates its inputs" control test uses).
    const writable = tokens as unknown as {
      color: { semantic: { error: { main: string } } }
    }
    // The project layer rebuilt only the primary branch; the error branch
    // of the result is still the defaultTokens node (copy-on-write
    // sharing), which is deep-frozen at assembly -- so a write through it
    // must throw in strict mode, not silently pollute the shared default
    // tree every later tenant merge starts from.
    const writeUntouchedBranch = (): void => {
      writable.color.semantic.error.main = '#DEADBEEF'
    }
    expect(writeUntouchedBranch).toThrow(TypeError)
  })

  it('keeps the defaults byte-identical after a write attempt through a shared branch', () => {
    const tenantA = createAppTheme({
      color: { semantic: { primary: { main: '#123456' } } },
    })
    const writable = tenantA.tokens as unknown as {
      color: { semantic: { error: { main: string } } }
    }
    let attemptThrew = false
    try {
      // The branch tenant A never overrode: the write must not land
      // anywhere shared. Refusing loudly is the deep freeze's job (pinned
      // by the throws-when test above); this test pins the outcome.
      writable.color.semantic.error.main = '#DEADBEEF'
    } catch {
      attemptThrew = true
    }
    // No pollution: the branch the write aimed at still holds the pristine
    // value, and the assembled defaults are byte-identical to the snapshot
    // taken at module load.
    expect(defaultTokens.color.semantic.error.main).toBe(PRISTINE_ERROR_MAIN)
    expect(JSON.stringify(defaultTokens)).toBe(DEFAULT_BEFORE)
    expect(attemptThrew).toBe(true)
  })

  it('reads only its own values in a later tenant merge after a write attempt', () => {
    const tenantA = createAppTheme({
      color: { semantic: { primary: { main: '#123456' } } },
    })
    const writable = tenantA.tokens as unknown as {
      color: { semantic: { error: { main: string } } }
    }
    let attemptThrew = false
    try {
      writable.color.semantic.error.main = '#DEADBEEF'
    } catch {
      attemptThrew = true
    }
    // No leak: a later factory call for tenant B reads its own override
    // and pristine defaults -- never the value tenant A tried to write.
    const tenantB = createAppTheme({ zIndex: { values: { modal: 1600 } } })
    expect(tenantB.tokens.zIndex.values.modal).toBe(1600)
    expect(tenantB.tokens.color.semantic.error.main).toBe(PRISTINE_ERROR_MAIN)
    expect(tenantB.tokens.typography.fontFamily.sans).toBe(
      defaultTokens.typography.fontFamily.sans,
    )
    expect(attemptThrew).toBe(true)
  })
})
