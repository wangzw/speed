/** A semantic role's tonal family: main, light and dark variants plus the text color that reads on them. */
export interface SemanticColor {
  readonly main: string
  readonly light: string
  readonly dark: string
  readonly contrastText: string
}

/** The UI roles every speed application can use. */
export type SemanticRole = 'primary' | 'secondary' | 'error' | 'warning' | 'info' | 'success'

/** Palette of the six semantic roles. */
export type SemanticPalette = { readonly [Role in SemanticRole]: SemanticColor }

/** Neutral ramp steps. */
export type NeutralTone =
  | 50
  | 100
  | 200
  | 300
  | 400
  | 500
  | 600
  | 700
  | 800
  | 900
  | 950

/** The neutral ramp, darkest at 950. */
export type NeutralScale = { readonly [Tone in NeutralTone]: string }

/** One color mode's page canvas gradient stops. */
export interface CanvasGradientTokens {
  readonly from: string
  readonly to: string
}

/** The page canvas gradient, one stop pair per color mode. */
export interface CanvasTokens {
  readonly light: CanvasGradientTokens
  readonly dark: CanvasGradientTokens
}

/** The color section of the speed token tree (SpeedTokens["color"]). */
export interface ColorTokens {
  readonly semantic: SemanticPalette
  readonly neutral: NeutralScale
  readonly text: {
    readonly primary: string
    readonly secondary: string
    readonly disabled: string
  }
  readonly background: {
    readonly default: string
    readonly paper: string
    /** The page canvas gradient; absent means no gradient behind the page. */
    readonly canvas?: CanvasTokens
  }
  readonly divider: string
}

export const colorTokens: ColorTokens = {
  semantic: {
    // Every main clears 4.5:1 as a white-label background and as text on the
    // light surfaces; warning, info and success use the 700 tones for that.
    primary: { main: '#2563EB', light: '#60A5FA', dark: '#1D4ED8', contrastText: '#FFFFFF' },
    secondary: { main: '#7C3AED', light: '#A78BFA', dark: '#6D28D9', contrastText: '#FFFFFF' },
    error: { main: '#DC2626', light: '#F87171', dark: '#B91C1C', contrastText: '#FFFFFF' },
    warning: { main: '#B45309', light: '#FBBF24', dark: '#92400E', contrastText: '#FFFFFF' },
    info: { main: '#0369A1', light: '#38BDF8', dark: '#075985', contrastText: '#FFFFFF' },
    success: { main: '#15803D', light: '#4ADE80', dark: '#166534', contrastText: '#FFFFFF' },
  },
  neutral: {
    50: '#F8FAFC',
    100: '#F1F5F9',
    200: '#E2E8F0',
    300: '#CBD5E1',
    400: '#94A3B8',
    500: '#64748B',
    600: '#475569',
    700: '#334155',
    800: '#1E293B',
    900: '#0F172A',
    950: '#020617',
  },
  text: {
    primary: '#0F172A',
    secondary: '#475569',
    disabled: '#94A3B8',
  },
  background: {
    default: '#F8FAFC',
    paper: '#FFFFFF',
  },
  divider: '#E2E8F0',
}
