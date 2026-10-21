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
 * Classic: the traditional admin-console look -- nearly square corners, a
 * denser spacing base, hard rules and shadows, tighter leading, a navy
 * primary on a steel secondary.
 */
export const classicThemePreset: ThemePreset = createPreset('classic', {
  color: {
    divider: '#94A3B8',
    semantic: {
      primary: {
        main: '#1E3A8A',
        light: '#60A5FA',
        dark: '#172554',
        contrastText: '#FFFFFF',
      },
      secondary: {
        main: '#475569',
        light: '#94A3B8',
        dark: '#334155',
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
  spacing: {
    unit: 7,
  },
  shape: {
    borderRadius: 2,
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
 * Modern: the fashion-forward product look -- large radii, an airier
 * spacing base, soft wide shadows, looser leading, a violet primary on a
 * rose secondary.
 */
export const modernThemePreset: ThemePreset = createPreset('modern', {
  color: {
    semantic: {
      primary: {
        main: '#7C3AED',
        light: '#A78BFA',
        dark: '#6D28D9',
        contrastText: '#FFFFFF',
      },
      secondary: {
        main: '#BE185D',
        light: '#F472B6',
        dark: '#9D174D',
        contrastText: '#FFFFFF',
      },
    },
  },
  typography: {
    lineHeight: {
      tight: 1.35,
      normal: 1.7,
      relaxed: 1.95,
    },
  },
  spacing: {
    unit: 9,
  },
  shape: {
    borderRadius: 20,
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
      // The light stops stay pastel so the page itself reads light; the
      // dark stops stay vivid so the raised surfaces have a visible tint
      // to show through. The composite is what the contrast test pins.
      canvas: {
        light: { from: '#C7D2FE', to: '#FBCFE8' },
        dark: { from: '#4338CA', to: '#9D174D' },
      },
    },
    text: {
      // The saturated canvas leaves the default secondary ink too light.
      secondary: '#1E293B',
    },
    semantic: {
      // The saturated canvas costs every role most of its headroom, so the
      // light palette uses the deepest tone of each family as main; dark
      // mode paints the light tone instead and keeps the vivid accents.
      primary: {
        main: '#1E1B4B',
        light: '#C7D2FE',
        dark: '#0F0A2E',
        contrastText: '#FFFFFF',
      },
      secondary: {
        main: '#2E1065',
        light: '#DDD6FE',
        dark: '#1E0B45',
        contrastText: '#FFFFFF',
      },
      error: {
        main: '#450A0A',
        light: '#FECACA',
        dark: '#2A0606',
        contrastText: '#FFFFFF',
      },
      info: {
        main: '#041C2C',
        light: '#BAE6FD',
        dark: '#02121C',
        contrastText: '#FFFFFF',
      },
      success: {
        main: '#052E16',
        light: '#BBF7D0',
        dark: '#022C22',
        contrastText: '#FFFFFF',
      },
      warning: {
        main: '#451A03',
        light: '#FDE68A',
        dark: '#2A1002',
        contrastText: '#FFFFFF',
      },
    },
  },
  shape: {
    borderRadius: 24,
  },
  surface: {
    // The canvas is the page background: it paints above the body's
    // background color, so a page fill would be invisible under an opaque
    // gradient. The translucency that makes the glass read lives on the
    // raised surfaces below, over each mode's own paper tone.
    container: {
      opacity: 0.4,
      borderColor: 'rgba(15, 23, 42, 0.22)',
      borderColorDark: 'rgba(255, 255, 255, 0.55)',
      borderWidth: 1,
      backdropFilter: 'blur(22px) saturate(180%)',
      elevation: 2,
    },
    overlay: {
      opacity: 0.44,
      borderColor: 'rgba(15, 23, 42, 0.26)',
      borderColorDark: 'rgba(255, 255, 255, 0.62)',
      borderWidth: 1,
      backdropFilter: 'blur(32px) saturate(180%)',
      elevation: 8,
    },
    // The standing navigation column is chrome, not a card: it stays more
    // transparent than the content surfaces, so the canvas colour -- not a
    // washed white -- carries the column and it reads as part of the page.
    nav: {
      opacity: 0.25,
      borderColor: 'rgba(15, 23, 42, 0.22)',
      borderColorDark: 'rgba(255, 255, 255, 0.55)',
      borderWidth: 1,
      backdropFilter: 'blur(22px) saturate(180%)',
    },
    header: {
      // The table header is the panel's own glass, not a tinted band: a
      // transparent fill lets the card show through, and the header's
      // weight plus the row divider separate it from the body. A fill or
      // a backdrop filter here would stack a second layer inside the
      // panel's own blur -- the filter samples the wrong backdrop and the
      // row reads as a darker strip.
      fill: 'transparent',
      borderColor: 'rgba(15, 23, 42, 0.22)',
      borderColorDark: 'rgba(255, 255, 255, 0.5)',
      borderWidth: 1,
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
 * hard green rules, green and amber semantic roles, no shadows and a
 * green text glow. The two color modes are the two terminal papers: a
 * pale green sheet with dark green ink in light mode, a near-black
 * screen with pale green ink in dark mode. The surface roles declare no
 * fill, so each mode inherits its own paper and header tone.
 */
export const asciiThemePreset: ThemePreset = createPreset('ascii', {
  color: {
    background: {
      default: '#F0FDF4',
      paper: '#DCFCE7',
    },
    text: {
      primary: '#052E16',
      secondary: '#166534',
      disabled: '#4D7C0F',
    },
    divider: '#16A34A',
    // The ramp carries the dark terminal: dark mode paints its surfaces
    // from its dark end and its text from its light end, so the same
    // green family serves both modes. Light mode's header surface reads
    // neutral 50 as well, which keeps it on the same sheet.
    neutral: {
      50: '#DCFCE7',
      100: '#BBF7D0',
      200: '#86EFAC',
      300: '#86EFAC',
      400: '#4ADE80',
      500: '#22C55E',
      600: '#16A34A',
      700: '#166534',
      800: '#08120B',
      900: '#050A06',
      950: '#020403',
    },
    semantic: {
      // Each family carries both papers: `main` reads on the light
      // sheet, and dark mode paints `light` as its main on the black
      // screen.
      primary: {
        main: '#15803D',
        light: '#4ADE80',
        dark: '#166534',
        contrastText: '#F0FDF4',
      },
      secondary: {
        main: '#B45309',
        light: '#FBBF24',
        dark: '#92400E',
        contrastText: '#F0FDF4',
      },
      error: {
        main: '#B91C1C',
        light: '#FCA5A5',
        dark: '#991B1B',
        contrastText: '#F0FDF4',
      },
      info: {
        main: '#0369A1',
        light: '#7DD3FC',
        dark: '#075985',
        contrastText: '#F0FDF4',
      },
      success: {
        main: '#15803D',
        light: '#4ADE80',
        dark: '#166534',
        contrastText: '#F0FDF4',
      },
      warning: {
        main: '#B45309',
        light: '#FBBF24',
        dark: '#92400E',
        contrastText: '#F0FDF4',
      },
    },
  },
  typography: {
    fontFamily: {
      sans: ASCII_FONT_STACK,
    },
    textGlow: '0 0 4px rgba(34, 197, 94, 0.45)',
  },
  shape: {
    borderRadius: 0,
  },
  surface: {
    page: {
      borderColor: '#16A34A',
      borderWidth: 1,
    },
    container: {
      borderColor: '#16A34A',
      borderWidth: 1,
    },
    overlay: {
      borderColor: '#16A34A',
      borderWidth: 1,
    },
    header: {
      borderColor: '#16A34A',
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
