# @speed/theme-presets

Named, ready-made theme styles as pure data: six `ThemePreset` values that
each carry a token diff relative to `@speed/tokens`' default tree. No React,
no MUI, no i18n, and no runtime dependency of its own -- consumers merge a
preset's `tokens` through `deepMerge`.

## Contents

| Export | What it is |
|---|---|
| `ThemePreset` | `{ id, tokens }`; one named style |
| `ThemePresetTokens` | `Omit<TokensOverride, 'breakpoints' \| 'zIndex'>`; the sections a preset may change |
| `defaultThemePreset` | `{ id: 'default', tokens: {} }`; the baseline |
| `classicThemePreset` | Traditional admin console: 4px radius, hard rules and shadows, tight leading, a deeper primary |
| `modernThemePreset` | Fashion-forward product look: 14px radius, soft wide shadows, loose leading, a brighter accent |
| `compactThemePreset` | Information density: a smaller type scale, tight leading, a 6px spacing base |
| `glassThemePreset` | Liquid glass: large radius, translucent white surfaces, backdrop blur and saturation, a two-mode canvas gradient, soft shadows |
| `asciiThemePreset` | ASCII terminal: one monospaced face (with CJK fallbacks), zero radius, near-black surfaces with hard green rules, green/amber roles, no shadows, a green text glow |
| `themePresets` | The shipped shelf, `default` first |

## Using a preset

A preset is not applied by this package. The assembly layer merges it
between the default tree and the project layer:

```ts
import { defaultTokens, deepMerge } from '@speed/tokens'
import type { TokensOverride } from '@speed/tokens'
import { glassThemePreset } from '@speed/theme-presets'

const projectTokens: TokensOverride = {
  // ...your brand, applied over the preset
}
const tokens = deepMerge(defaultTokens, glassThemePreset.tokens, projectTokens)
```

Order is `defaultTokens -> preset -> projectTokens -> tenantOverrides`:
a customer's brand colors still beat the preset, and tenant overrides still
beat both. The `ui-kit` theme provider owns that wiring; this package only
ships the data.

## What a preset may and may not change

`ThemePresetTokens` excludes `breakpoints` and `zIndex` **at the type
level**. Those are functional contracts, not style:

- `breakpoints` is pinned by the layout design (`md` is the shell's own
  breakpoint).
- `zIndex` is depended on by the side-panel and dialog ordering.

A style that moved either would break chrome instead of restyling it.
Compile-time rejection is the first guard; the package's tests also walk
the shipped shelf and pin that the merged tree's `breakpoints` and `zIndex`
branches stay shared with `defaultTokens` by identity.

Two further boundaries:

- **No display names.** Labels are product copy and live in the host's
  locale resources (the demo host's `locales/*.json`), never here. The
  package registers no i18n namespace and imports no i18n.
- **No component overrides.** A preset writes design values (colors,
  radii, shadows, surface fields); mapping those values onto MUI
  components is the `ui-kit` adapter's job.

## The surface extensions presets rely on

Glass and ASCII are expressed through the surface system from
`@speed/tokens`:

- `surface.<role>.opacity` makes a resolved fill translucent; the adapter
  applies the alpha to the already mode-resolved `#RRGGBB` fill, so one
  value is correct in both modes. Non-hex fills are left untouched.
- `surface.<role>.backdropFilter`, `borderColor`, `borderWidth` and
  `elevation` are painted by the existing surface component mapping.
- `color.background.canvas.{light,dark}` supplies the page gradient the
  adapter paints as the body's background image. It paints above
  `surface.page.fill`, so the glass preset uses the canvas as the page
  and sets no page fill; a page fill would be invisible beneath an
  opaque gradient.
- `typography.textGlow` becomes the body text shadow.

Presets do not introduce a second effects mechanism; they only fill those
slots.

## Adding a style

A new style is just another `ThemePreset` handed to the assembly. A
customer does not need to change this package: compose their own preset
data and pass it to the theme provider. The shipped six are a starting
shelf, not a closed enum.

To add one here, append a `createPreset(id, tokens)` value (and register it
in `themePresets`), keep the id unique, and add the corresponding display
name to every host locale resource that offers the preset.

## Development

From `web/packages/theme-presets`:

```sh
pnpm lint
pnpm typecheck
pnpm test
pnpm build
```

`build` first builds `@speed/tokens`, because the emitted declaration keeps
the bare `@speed/tokens` type import and needs its `dist/index.d.ts`. There
are no locales in this package: it ships no user-facing text.
