/**
 * Theme presets: named, ready-made token overrides.
 *
 * @speed/tokens owns the mechanism -- one default tree, deepMerge, and the
 * types. This package is the shelf: each preset is a TokensOverride, that
 * is, a diff against the default tree, and nothing more. The assembly
 * order is defaultTokens -> preset -> projectTokens -> tenantOverrides, so
 * a project's brand colors still beat a preset and tenant overrides still
 * beat both.
 *
 * Two contracts shape the shape below:
 *
 * - The package is pure data. It imports types only, with no runtime
 *   dependency, no React, no MUI and no i18n. Display names are product
 *   copy and belong to the host's locale resources, so a preset carries an
 *   id and tokens, never a label.
 * - Layout and stacking are not style. `breakpoints` is a functional
 *   contract pinned by the layout design, and `zIndex` is depended on by
 *   the side-panel and dialog ordering; a preset that moved either would
 *   break chrome rather than restyle it. `ThemePresetTokens` excludes both
 *   at the type level.
 */

import type { TokensOverride } from '@speed/tokens'

/** What a preset may change: everything except the layout and stacking contracts. */
export type ThemePresetTokens = Omit<TokensOverride, 'breakpoints' | 'zIndex'>

/** One named style: a token diff relative to the default tree. */
export interface ThemePreset {
  readonly id: string
  readonly tokens: ThemePresetTokens
}

/** Recursively freeze an object in place, idempotently, and return it. */
function deepFreeze<T>(value: T): T {
  if (typeof value === 'object' && value !== null) {
    for (const child of Object.values(value as Record<string, unknown>)) {
      deepFreeze(child)
    }
    Object.freeze(value)
  }
  return value
}

/** Assemble one preset and freeze it: presets are shared data, never mutable state. */
function createPreset(id: string, tokens: ThemePresetTokens): ThemePreset {
  return deepFreeze({ id, tokens })
}

/** The baseline: no diff at all, byte-identical to the default token tree. */
export const defaultThemePreset: ThemePreset = createPreset('default', {})

/**
 * Classic: the traditional admin-console look -- small radii, hard rules
 * and shadows, tighter leading, a deeper primary.
 */
export const classicThemePreset: ThemePreset = createPreset('classic', {
  color: {
    divider: '#CBD5E1',
    semantic: {
      primary: {
        main: '#1D4ED8',
        light: '#3B82F6',
        dark: '#1E3A8A',
        contrastText: '#FFFFFF',
      },
    },
  },
  typography: {
    lineHeight: {
      tight: 1.15,
      normal: 1.4,
      relaxed: 1.6,
    },
  },
  shape: {
    borderRadius: 4,
  },
  shadows: {
    1: '0 1px 0 0 rgba(15, 23, 42, 0.18)',
    2: '0 2px 0 0 rgba(15, 23, 42, 0.16), 0 1px 0 0 rgba(15, 23, 42, 0.12)',
    4: '0 3px 0 0 rgba(15, 23, 42, 0.18), 0 1px 0 0 rgba(15, 23, 42, 0.10)',
    8: '0 4px 0 0 rgba(15, 23, 42, 0.20)',
    16: '0 6px 0 0 rgba(15, 23, 42, 0.22)',
    24: '0 8px 0 0 rgba(15, 23, 42, 0.24)',
  },
})

/**
 * Modern: the fashion-forward product look -- large radii, soft wide
 * shadows, looser leading, a brighter accent.
 */
export const modernThemePreset: ThemePreset = createPreset('modern', {
  color: {
    semantic: {
      primary: {
        main: '#4F46E5',
        light: '#818CF8',
        dark: '#4338CA',
        contrastText: '#FFFFFF',
      },
    },
  },
  typography: {
    lineHeight: {
      tight: 1.35,
      normal: 1.65,
      relaxed: 1.9,
    },
  },
  shape: {
    borderRadius: 14,
  },
  shadows: {
    1: '0 2px 8px 0 rgba(15, 23, 42, 0.06)',
    2: '0 4px 12px -2px rgba(15, 23, 42, 0.08), 0 2px 6px -2px rgba(15, 23, 42, 0.06)',
    4: '0 8px 24px -4px rgba(15, 23, 42, 0.10), 0 4px 10px -4px rgba(15, 23, 42, 0.08)',
    8: '0 16px 40px -8px rgba(15, 23, 42, 0.12), 0 8px 20px -8px rgba(15, 23, 42, 0.10)',
    16: '0 24px 60px -12px rgba(15, 23, 42, 0.14), 0 12px 28px -12px rgba(15, 23, 42, 0.10)',
    24: '0 32px 80px -16px rgba(15, 23, 42, 0.18)',
  },
})

/** Compact: information density -- a smaller type scale, tighter leading, a 6px spacing base. */
export const compactThemePreset: ThemePreset = createPreset('compact', {
  typography: {
    fontSize: {
      xs: 11,
      sm: 12,
      md: 14,
      lg: 16,
      xl: 18,
      '2xl': 22,
      '3xl': 26,
      '4xl': 30,
      '5xl': 40,
    },
    lineHeight: {
      tight: 1.15,
      normal: 1.35,
      relaxed: 1.5,
    },
  },
  spacing: {
    unit: 6,
  },
})

/**
 * Glass: liquid glass -- large radii, translucent surfaces over a colored
 * canvas gradient, backdrop blur and saturation, soft shadows.
 *
 * The raised surfaces declare an opacity but no fill, so each mode
 * inherits its own paper color before the alpha is applied: white in
 * light mode, the dark paper tone in dark mode. A hardcoded white fill
 * would composite to a mid-grey on the dark canvas and drop the dark
 * palette's light text below 4.5:1; inheriting the mode-resolved paper
 * keeps one token value correct on both pages. The light-mode error tone
 * is deepened because the pastel canvas stops already cost the default
 * red most of its headroom.
 */
export const glassThemePreset: ThemePreset = createPreset('glass', {
  color: {
    background: {
      canvas: {
        light: { from: '#EEF2FF', to: '#FCE7F3' },
        dark: { from: '#0B1020', to: '#151033' },
      },
    },
    semantic: {
      // The pastel canvas is slightly darker than white, which costs the
      // link tone the last of its text headroom; the error tone loses more.
      primary: {
        main: '#1D4ED8',
        light: '#60A5FA',
        dark: '#1E40AF',
        contrastText: '#FFFFFF',
      },
      error: {
        main: '#B91C1C',
        light: '#F87171',
        dark: '#991B1B',
        contrastText: '#FFFFFF',
      },
    },
  },
  shape: {
    borderRadius: 20,
  },
  surface: {
    // The canvas is the page background: it paints above the body's
    // background color, so a page fill would be invisible under an opaque
    // gradient. The translucency that makes the glass read lives on the
    // raised surfaces below, over each mode's own paper tone.
    container: {
      opacity: 0.36,
      borderColor: 'rgba(255, 255, 255, 0.65)',
      borderWidth: 1,
      backdropFilter: 'blur(18px) saturate(180%)',
      elevation: 2,
    },
    overlay: {
      opacity: 0.36,
      borderColor: 'rgba(255, 255, 255, 0.72)',
      borderWidth: 1,
      backdropFilter: 'blur(28px) saturate(180%)',
      elevation: 8,
    },
    header: {
      opacity: 0.3,
      borderColor: 'rgba(255, 255, 255, 0.6)',
      borderWidth: 1,
      backdropFilter: 'blur(18px) saturate(160%)',
    },
  },
  shadows: {
    1: '0 2px 10px rgba(15, 23, 42, 0.06)',
    2: '0 6px 20px rgba(15, 23, 42, 0.08)',
    4: '0 10px 30px rgba(15, 23, 42, 0.10)',
    8: '0 18px 44px rgba(15, 23, 42, 0.12)',
    16: '0 28px 64px rgba(15, 23, 42, 0.14)',
    24: '0 40px 90px rgba(15, 23, 42, 0.18)',
  },
})

/**
 * The mono stack plus CJK fallbacks, ending in the generic monospace
 * family: Chinese text stays monospaced instead of falling back to a
 * proportional face.
 */
const ASCII_FONT_STACK =
  "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', monospace"

/**
 * ASCII: the terminal look -- one monospaced face everywhere, no radii,
 * near-black surfaces with hard green rules, green and amber semantic
 * roles, no shadows, and a green text glow.
 */
export const asciiThemePreset: ThemePreset = createPreset('ascii', {
  color: {
    background: {
      default: '#050A06',
      paper: '#08120B',
    },
    text: {
      primary: '#D1FAE5',
      secondary: '#86EFAC',
      disabled: '#4ADE80',
    },
    divider: '#166534',
    semantic: {
      primary: {
        main: '#22C55E',
        light: '#4ADE80',
        dark: '#15803D',
        contrastText: '#020617',
      },
      secondary: {
        main: '#F59E0B',
        light: '#FBBF24',
        dark: '#B45309',
        contrastText: '#020617',
      },
      // The near-black surfaces turn the default light-mode error and info
      // tones into unreadable text. The bright terminal tones below keep
      // both roles at 4.5:1 or better on every surface role (the light
      // tone is what dark mode paints as its main).
      error: {
        main: '#F87171',
        light: '#FCA5A5',
        dark: '#EF4444',
        contrastText: '#020617',
      },
      info: {
        main: '#38BDF8',
        light: '#7DD3FC',
        dark: '#0EA5E9',
        contrastText: '#020617',
      },
      success: {
        main: '#22C55E',
        light: '#4ADE80',
        dark: '#15803D',
        contrastText: '#020617',
      },
      warning: {
        main: '#F59E0B',
        light: '#FBBF24',
        dark: '#B45309',
        contrastText: '#020617',
      },
    },
  },
  typography: {
    fontFamily: {
      sans: ASCII_FONT_STACK,
    },
    textGlow: '0 0 6px rgba(74, 222, 128, 0.65)',
  },
  shape: {
    borderRadius: 0,
  },
  surface: {
    page: {
      fill: '#050A06',
      borderColor: '#22C55E',
      borderWidth: 1,
    },
    container: {
      fill: '#08120B',
      borderColor: '#22C55E',
      borderWidth: 1,
    },
    overlay: {
      fill: '#0B1A0F',
      borderColor: '#22C55E',
      borderWidth: 1,
    },
    header: {
      fill: '#050A06',
      borderColor: '#22C55E',
      borderWidth: 1,
    },
  },
  shadows: {
    1: 'none',
    2: 'none',
    4: 'none',
    8: 'none',
    16: 'none',
    24: 'none',
  },
})

/** The shipped shelf, baseline first (the default-preset fallback reads presets[0]). */
export const themePresets: readonly ThemePreset[] = Object.freeze([
  defaultThemePreset,
  classicThemePreset,
  modernThemePreset,
  compactThemePreset,
  glassThemePreset,
  asciiThemePreset,
])
