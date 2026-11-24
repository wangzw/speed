/**
 * Theme presets: named, ready-made token overrides. Each preset is a
 * TokensOverride, a diff against the default tree; display names belong to
 * the host's locale resources.
 */

import type { TokensOverride } from '@speed/tokens'

/** What a preset may change: everything except the layout and stacking contracts. */
export type ThemePresetTokens = Omit<TokensOverride, 'breakpoints' | 'zIndex'>

/** One named style: a token diff relative to the default tree. */
export interface ThemePreset {
  readonly id: string
  readonly tokens: ThemePresetTokens
}

function deepFreeze<T>(value: T): T {
  if (typeof value === 'object' && value !== null) {
    for (const child of Object.values(value as Record<string, unknown>)) {
      deepFreeze(child)
    }
    Object.freeze(value)
  }
  return value
}

function createPreset(id: string, tokens: ThemePresetTokens): ThemePreset {
  return deepFreeze({ id, tokens })
}

/** The baseline: no diff at all, byte-identical to the default token tree. */
export const defaultThemePreset: ThemePreset = createPreset('default', {})

/** Classic: the traditional admin-console look. */
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

/** Modern: the fashion-forward product look. */
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
 * Glass: liquid glass -- large radii, translucent surfaces over a canvas
 * gradient, blur and saturation. Raised surfaces declare opacity but no fill, so
 * each mode inherits its paper; a white fill would drop dark text below 4.5:1.
 */
export const glassThemePreset: ThemePreset = createPreset('glass', {
  color: {
    background: {
      canvas: {
        light: { from: '#C7D2FE', to: '#FBCFE8' },
        dark: { from: '#4338CA', to: '#9D174D' },
      },
    },
    text: {
      secondary: '#1E293B',
    },
    semantic: {
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
    container: {
      opacity: 0.5,
      borderColor: 'rgba(15, 23, 42, 0.30)',
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
    header: {
      // The table header is the panel's own glass: a fill or backdrop filter
      // here would stack a second layer inside the panel's blur.
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

const ASCII_FONT_STACK =
  "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', 'Courier New', 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', monospace"

/** ASCII: the terminal look -- one monospaced face, no radii, hard green rules, no shadows, a text glow. */
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
