# AGENTS.md — @speed/theme-presets

## What this package is

A shelf of named theme styles as pure data. Each `ThemePreset` is a
`TokensOverride`-shaped diff against `@speed/tokens`' default tree, and the
assembly layer merges it as `defaultTokens -> preset -> projectTokens ->
tenantOverrides`. This package owns the data; `ui-kit` owns the adapter and
the provider wiring.

## Non-negotiable rules

- **Pure data, forever.** No React, no MUI, no i18n, and no runtime
  dependency. The only import allowed is `import type { TokensOverride }
  from '@speed/tokens'` (and type-only imports from this package's own
  files). If a preset needs a new value, add it to the token tree and to
  the adapter, not a helper here.
- **Never override `breakpoints` or `zIndex`.** They are functional
  contracts (layout breakpoints, overlay stacking), not style. The
  `ThemePresetTokens` type excludes them, and the tests assert the shipped
  shelf leaves both branches shared with `defaultTokens`. A type-level
  `@ts-expect-error` in the suite pins that the exclusion is real; never
  loosen it to let a preset through.
- **No display names, no locale registration.** Labels are host product
  copy (the demo host's `locales/*.json`). The package registers no i18n
  namespace and must never gain a `locales/` directory.
- **No component overrides.** A preset writes design values only.
  Turning values into MUI `components` overrides is the `ui-kit` theme
  adapter's job; a preset that named a `Mui*` slot would couple the data
  layer to one toolkit.
- **Presets are frozen.** `createPreset` deep-freezes its result. Presets
  are shared across a process; a mutable one would leak a host's edit into
  every other consumer. Never return a preset without the freeze.
- **Order is a contract.** `themePresets` keeps `default` first because
  the provider's `defaultPreset` fallback reads `presets[0]` when a
  deployment names an unknown id.

## Adding or changing a preset

1. Write the `tokens` diff and wrap it in `createPreset(id, tokens)`; add
   the value to `themePresets`.
2. Keep ids unique and lowercase, hyphenated for multi-word names, as the
   shipped ones are.
3. Make sure the shipped value still meets the contrast contract: body text
   on every surface role, in both color modes, at least 4.5:1. A
   translucent fill is composited over both stops of that mode's canvas
   gradient and the worse stop is the one that counts.
4. Add the display name to every host locale resource that offers the
   preset list (the package itself stays text-free).
5. Run `pnpm lint`, `pnpm typecheck`, `pnpm test`, `pnpm build` — all
   clean.

## Testing

`src/index.test.ts` is the package's only suite, and it exercises the
shipped data through the one sanctioned path,
`deepMerge(defaultTokens, preset.tokens)`. It pins the shelf's order
(`default` first, the fallback the provider reads), unique ids,
deep-frozen presets, the exclusion of `breakpoints`/`zIndex` (a pair of
`@ts-expect-error` assignments, so the typecheck leg fails if the
exclusion ever loosens, plus the merged tree's identity sharing those
two branches with `defaultTokens`), every preset merging into a complete
token tree, the visible distinction among classic/modern/compact, glass's
translucent and blurred roles with a two-mode canvas, ascii's
monospace-with-CJK stack, zero radius, near-black ruled surfaces, legal
text shadow and all-`none` shadows, and the contrast contract: body text
and semantic text roles at least 4.5:1 on every surface role in both
modes, plus the 3:1 focus-ring floor, with a translucent fill composited
over both canvas stops and the page slot, the worse result counted.

The package cannot import the ui-kit adapter (that would pull React and
MUI into pure data), so the suite restates the adapter's resolution rules
-- which fill a role inherits, which text color a mode uses, how many
canvas stops a fill composites over. When the adapter's rules change,
this suite changes with them; otherwise the contrast promise is measured
against a stale model.

## Checking this package

The package config is modelled on `@speed/tokens`: `tsconfig.json` maps
`@speed/tokens` onto the sibling's live source for typecheck,
`tsconfig.build.json` emits ESM declarations, and `vitest.config.ts` takes
its timeout and coverage from the workspace preset and its aliases from the
workspace package map (`web/scripts/speed-aliases.mjs`). Register a new
specifier in that one map; never hand-copy aliases into this package.
